package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"chatisp/internal/attachments"
	attachmentHTTP "chatisp/internal/attachments/httpapi"
	attendanceHttpAPI "chatisp/internal/attendance/httpapi"
	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/businesshours"
	businessHoursHTTP "chatisp/internal/businesshours/httpapi"
	"chatisp/internal/cannedreplies"
	channelConfig "chatisp/internal/channel/configuration"
	channelHttpAPI "chatisp/internal/channel/httpapi"
	"chatisp/internal/channelpolicy"
	channelPolicyHTTP "chatisp/internal/channelpolicy/httpapi"
	"chatisp/internal/collision"
	collisionHTTP "chatisp/internal/collision/httpapi"
	"chatisp/internal/contactdirectory"
	contactHTTP "chatisp/internal/contactdirectory/httpapi"
	"chatisp/internal/contactdocs"
	contactDocsHTTP "chatisp/internal/contactdocs/httpapi"
	"chatisp/internal/lifecycle"
	lifecycleHTTP "chatisp/internal/lifecycle/httpapi"
	"chatisp/internal/notifications"
	notificationHTTP "chatisp/internal/notifications/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/platform/version"
	"chatisp/internal/presence"
	presenceHTTP "chatisp/internal/presence/httpapi"
	"chatisp/internal/sectors"
	sectorHTTP "chatisp/internal/sectors/httpapi"
	"chatisp/internal/triage"
	triageHTTP "chatisp/internal/triage/httpapi"
)

// ReadyFunc reports whether the process can accept traffic.
// A non-nil error is not shown to the client.
type ReadyFunc func(context.Context) error

// Dependencies wires the HTTP API. Nil metrics are created internally.
// A nil Auth still registers the panel routes and answers 503.
type Dependencies struct {
	Ready          ReadyFunc
	Metrics        *metrics.Metrics
	Logger         *slog.Logger
	Auth           *auth.Service
	TrustedOrigins []string
	Attendance     attendanceService.Service
	Collision      *collision.Service
	Channels       *channelConfig.Service
	Sectors        sectors.Service
	Contacts       contactdirectory.Service
	Notifications  *notifications.Service
	Realtime       http.Handler
	Webhook        http.Handler
	CannedReplies  cannedreplies.Service
	ChannelPolicy  *channelpolicy.Service
	Attachments    *attachments.Service
	BusinessHours  businesshours.Service
	Documents      contactdocs.Service
	Presence       presence.Service
	Lifecycle      lifecycle.Service
	Triage         triage.Service
	// Store is the application database used by distribution. Nil answers 503.
	Store *db.Store
}

// API is the public HTTP handler and its generated OpenAPI document.
type API struct {
	Handler http.Handler
	Huma    huma.API
	Metrics *metrics.Metrics
}

type healthBody struct {
	Status string `json:"status" enum:"ok" doc:"ok quando o processo está vivo." example:"ok"`
}

type healthOutput struct {
	Body healthBody
}

type readyBody struct {
	Status string `json:"status" enum:"ready" doc:"ready quando o processo pode receber tráfego." example:"ready"`
}

type readyOutput struct {
	Body readyBody
}

// New builds the chi router, Huma operations and Prometheus middleware.
func New(deps Dependencies) *API {
	if deps.Logger == nil {
		deps.Logger = logging.New("info", io.Discard)
	}
	if deps.Metrics == nil {
		deps.Metrics = metrics.New()
	}
	if deps.Ready == nil {
		deps.Ready = func(context.Context) error { return nil }
	}

	router := chi.NewRouter()
	router.Use(metricsMiddleware(deps.Metrics))
	router.Use(recoverMiddleware(deps.Logger))
	router.Use(func(next http.Handler) http.Handler { return panelSafety(next) })
	router.Use(protectOrigins(deps.TrustedOrigins, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		Write(w, http.StatusForbidden, "origem não permitida")
	})))

	config := huma.DefaultConfig("Chat ISP", version.Version)
	config.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", func(t reflect.Type, hint string) string {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.PkgPath() == "chatisp/internal/attendance/store" {
			return "RealtimeStore" + t.Name()
		}
		if t.PkgPath() == "chatisp/internal/contactdirectory" && t.Name() != "" {
			return "Directory" + t.Name()
		}
		switch {
		case strings.Contains(t.PkgPath(), "chatisp/internal/cannedreplies"):
			return "Canned" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/channelpolicy"):
			return "ChannelPolicy" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/attachments"):
			return "Attachment" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/businesshours"):
			return "BusinessHours" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/contactdocs"):
			return "ContactDocument" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/lifecycle"):
			return "Lifecycle" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/triage"):
			return "Triage" + t.Name()
		case strings.Contains(t.PkgPath(), "chatisp/internal/presence"):
			return "Presence" + t.Name()
		}
		return huma.DefaultSchemaNamer(t, hint)
	})
	// Keep the document limited to the operations below. The default hooks add
	// schema links and a docs page that this skeleton does not expose.
	config.CreateHooks = nil
	config.DocsPath = ""
	config.SchemasPath = ""
	config.OpenAPI.Info.Description = "API HTTP do Chat ISP. Erros usam RFC 9457 (application/problem+json) e não incluem stack trace nem segredos."
	config.OpenAPI.Servers = []*huma.Server{{
		URL:         "/",
		Description: "Instância atual",
	}}
	config.OnAddOperation = append(config.OnAddOperation, localizeProblemResponses)

	api := humachi.New(router, config)
	huma.Register(api, huma.Operation{
		OperationID:   "get-healthz",
		Method:        http.MethodGet,
		Path:          "/healthz",
		Summary:       "Verifica se o processo está vivo",
		Description:   "Não consulta dependências. Use /readyz para saber se o processo pode receber tráfego.",
		Tags:          []string{"operacao"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusInternalServerError},
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		return &healthOutput{Body: healthBody{Status: "ok"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "get-readyz",
		Method:        http.MethodGet,
		Path:          "/readyz",
		Summary:       "Verifica se o processo pode receber tráfego",
		Description:   "Responde 503 enquanto o processo está encerrando ou ainda não ficou pronto.",
		Tags:          []string{"operacao"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusServiceUnavailable},
	}, func(ctx context.Context, _ *struct{}) (*readyOutput, error) {
		if err := deps.Ready(ctx); err != nil {
			return nil, huma.Error503ServiceUnavailable("serviço indisponível")
		}
		return &readyOutput{Body: readyBody{Status: "ready"}}, nil
	})
	registerPanel(api, deps)
	registerPlatform(api, deps)
	attendanceHttpAPI.Register(api, attendanceHttpAPI.Dependencies{
		Auth:           deps.Auth,
		Attendance:     deps.Attendance,
		TrustedOrigins: deps.TrustedOrigins,
	})
	collisionHTTP.Register(api, collisionHTTP.Dependencies{Auth: deps.Auth, Collision: deps.Collision, TrustedOrigins: deps.TrustedOrigins})
	channelHttpAPI.Register(api, channelHttpAPI.Dependencies{
		Auth:           deps.Auth,
		Channels:       deps.Channels,
		TrustedOrigins: deps.TrustedOrigins,
	})
	sectorHTTP.Register(api, sectorHTTP.Dependencies{Auth: deps.Auth, Sectors: deps.Sectors, TrustedOrigins: deps.TrustedOrigins})
	contactHTTP.Register(api, contactHTTP.Dependencies{Auth: deps.Auth, Contacts: deps.Contacts, TrustedOrigins: deps.TrustedOrigins})
	notificationHTTP.Register(api, notificationHTTP.Dependencies{Auth: deps.Auth, Notifications: deps.Notifications, TrustedOrigins: deps.TrustedOrigins})
	cannedreplies.Register(api, cannedreplies.Dependencies{Auth: deps.Auth, CannedReplies: deps.CannedReplies, TrustedOrigins: deps.TrustedOrigins})
	channelPolicyHTTP.Register(api, channelPolicyHTTP.Dependencies{Auth: deps.Auth, Policy: deps.ChannelPolicy, TrustedOrigins: deps.TrustedOrigins})
	attachmentHTTP.Register(api, attachmentHTTP.Dependencies{Auth: deps.Auth, Attachments: deps.Attachments, TrustedOrigins: deps.TrustedOrigins})
	businessHoursHTTP.Register(api, businessHoursHTTP.Dependencies{Auth: deps.Auth, BusinessHours: deps.BusinessHours, TrustedOrigins: deps.TrustedOrigins})
	contactDocsHTTP.Register(api, contactDocsHTTP.Dependencies{Auth: deps.Auth, Documents: deps.Documents, TrustedOrigins: deps.TrustedOrigins})
	presenceHTTP.Register(api, presenceHTTP.Dependencies{Auth: deps.Auth, Presence: deps.Presence, TrustedOrigins: deps.TrustedOrigins})
	lifecycleHTTP.Register(api, lifecycleHTTP.Dependencies{Auth: deps.Auth, Lifecycle: deps.Lifecycle, TrustedOrigins: deps.TrustedOrigins})
	triageHTTP.Register(api, triageHTTP.Dependencies{Auth: deps.Auth, Triage: deps.Triage, TrustedOrigins: deps.TrustedOrigins})
	registerOccupancy(api, deps)
	registerDistribution(api, deps)
	registerRealtimeContract(api)

	realtimeHandler := deps.Realtime
	if realtimeHandler == nil {
		realtimeHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Write(w, http.StatusServiceUnavailable, "serviço indisponível")
		})
	}
	router.Handle("/api/panel/realtime", realtimeHandler)
	router.Handle("/api/panel/ws", realtimeHandler)

	webhookHandler := deps.Webhook
	if webhookHandler == nil {
		webhookHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Write(w, http.StatusServiceUnavailable, "serviço indisponível")
		})
	}
	router.Handle("/api/webhooks/meta", webhookHandler)
	router.Handle("/webhooks/meta", webhookHandler)
	router.Handle("/meta/webhook", webhookHandler)

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		Write(w, http.StatusNotFound, "o caminho solicitado não existe")
	})
	// Chi drops the allowed-method list when MethodNotAllowed replaces the
	// default handler. RFC 9110 requires Allow on 405, so set it first.
	router.MethodNotAllowed(methodNotAllowed(router))

	return &API{Handler: router, Huma: api, Metrics: deps.Metrics}
}

// OpenAPIJSON returns the canonical, indented OpenAPI document.
func OpenAPIJSON() ([]byte, error) {
	api := New(Dependencies{})
	raw, err := json.Marshal(api.Huma.OpenAPI())
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// knownMethods is the stable Allow order. Chi's own list follows map iteration.
var knownMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
	http.MethodConnect,
	http.MethodTrace,
	"QUERY",
}

func methodNotAllowed(routes chi.Routes) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if allow := allowedMethods(routes, requestPath(r)); allow != "" {
			w.Header().Set("Allow", allow)
		}
		Write(w, http.StatusMethodNotAllowed, "método não permitido para este caminho")
	}
}

func allowedMethods(routes chi.Routes, path string) string {
	allowed := make([]string, 0, 2)
	for _, method := range knownMethods {
		if routes.Match(chi.NewRouteContext(), method, path) {
			allowed = append(allowed, method)
		}
	}
	return strings.Join(allowed, ", ")
}

func requestPath(r *http.Request) string {
	if r.URL != nil && r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	if r.URL != nil && r.URL.Path != "" {
		return r.URL.Path
	}
	return "/"
}

func localizeProblemResponses(_ *huma.OpenAPI, op *huma.Operation) {
	if op == nil || op.Responses == nil {
		return
	}
	for code, response := range op.Responses {
		if response == nil {
			continue
		}
		switch code {
		case "200":
			response.Description = "Sucesso"
		case "400":
			response.Description = "Requisição inválida"
		case "401":
			response.Description = "Não autenticado"
		case "403":
			response.Description = "Acesso negado"
		case "404":
			response.Description = "Recurso não encontrado"
		case "409":
			response.Description = "Conflito"
		case "422":
			response.Description = "Dados inválidos"
		case "429":
			response.Description = "Muitas tentativas"
		case "500":
			response.Description = "Erro interno"
		case "503":
			response.Description = "Serviço indisponível"
		case "default":
			response.Description = "Erro"
		}
	}
}

func metricsMiddleware(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			started := time.Now()
			next.ServeHTTP(recorder, r)
			if m != nil {
				m.Observe(r.Method, routePattern(r), recorder.status, time.Since(started))
			}
		})
	}
}

func routePattern(r *http.Request) string {
	route := chi.RouteContext(r.Context())
	if route == nil {
		return "unmatched"
	}
	pattern := route.RoutePattern()
	if pattern == "" {
		// Chi leaves the pattern empty for unknown paths. Never fall back to
		// the raw URL: it would make a high-cardinality Prometheus label.
		return "unmatched"
	}
	return pattern
}

func recoverMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil || recovered == http.ErrAbortHandler {
					return
				}
				log.Error("panic recuperado", "component", "http")
				if log.Enabled(r.Context(), slog.LevelDebug) {
					log.Debug("stack de panic omitido da resposta", "component", "http", "bytes", len(debug.Stack()))
				}
				Write(w, http.StatusInternalServerError, "ocorreu um erro interno")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.wrote {
		s.status = status
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
