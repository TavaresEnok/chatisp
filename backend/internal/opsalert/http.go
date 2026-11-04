package opsalert

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// Dependencies liga o alerta ao banco e à sessão já existentes.
type Dependencies struct {
	Auth           *auth.Service
	DB             *db.Store
	TrustedOrigins []string
	Now            func() time.Time
}

// Router é o mínimo do roteador HTTP para montar as rotas fora do OpenAPI.
type Router interface {
	Method(method, pattern string, h http.Handler)
}

// Mount publica a leitura e a gravação dos limites. As rotas não entram no
// documento OpenAPI gerado.
func Mount(r Router, deps Dependencies) {
	h := handler{deps: deps}
	r.Method(http.MethodGet, "/api/panel/ops-alerts", http.HandlerFunc(h.list))
	r.Method(http.MethodPut, "/api/panel/ops-alerts/settings", http.HandlerFunc(h.save))
}

type handler struct{ deps Dependencies }

type settingsInput struct {
	JobAgeSeconds     int   `json:"job_age_seconds"`
	WebhookAgeSeconds int   `json:"webhook_age_seconds"`
	ExpectedVersion   int64 `json:"expected_version"`
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r, false)
	if !ok {
		return
	}
	var view View
	err := h.deps.DB.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = Refresh(ctx, tx, principal.TenantID, h.now())
		return err
	})
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	writeJSON(w, view)
}

func (h handler) save(w http.ResponseWriter, r *http.Request) {
	if !originAllowed(h.deps.TrustedOrigins, r.Header.Get("Origin")) {
		writeProblem(w, http.StatusForbidden, "origem não permitida")
		return
	}
	principal, ok := h.principal(w, r, true)
	if !ok {
		return
	}
	var body settingsInput
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "limites inválidos")
		return
	}
	var view View
	err := h.deps.DB.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = SaveLimits(ctx, tx, principal.TenantID, body.JobAgeSeconds, body.WebhookAgeSeconds, body.ExpectedVersion, h.now())
		return err
	})
	if errors.Is(err, ErrInvalid) {
		writeProblem(w, http.StatusUnprocessableEntity, "informe limites entre 60 e 604800 segundos")
		return
	}
	if errors.Is(err, ErrConflict) {
		writeProblem(w, http.StatusConflict, "outra gravação alterou os limites")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	writeJSON(w, view)
}

func (h handler) principal(w http.ResponseWriter, r *http.Request, write bool) (auth.Principal, bool) {
	if h.deps.Auth == nil || h.deps.DB == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return auth.Principal{}, false
	}
	token := ""
	if cookie, err := r.Cookie("__Host-session"); err == nil {
		token = cookie.Value
	}
	principal, err := h.deps.Auth.Authenticate(r.Context(), token, r.Header.Get("X-CSRF-Token"), write)
	if err != nil {
		writeAuth(w, err)
		return auth.Principal{}, false
	}
	if principal.IsSuperadmin || principal.EnrollmentOnly || principal.TenantID == uuid.Nil || principal.MembershipID == uuid.Nil || principal.Role != "admin" {
		writeProblem(w, http.StatusForbidden, "acesso negado")
		return auth.Principal{}, false
	}
	return principal, true
}

func (h handler) now() time.Time {
	if h.deps.Now != nil {
		return h.deps.Now().UTC()
	}
	return time.Now().UTC()
}

func originAllowed(origins []string, origin string) bool {
	for _, allowed := range origins {
		if allowed != "" && allowed == origin {
			return true
		}
	}
	return false
}

func writeAuth(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProblem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeJSON(w http.ResponseWriter, view View) {
	if view.Alerts == nil {
		view.Alerts = []Alert{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(view)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(map[string]any{
		"type":   problemType(status),
		"title":  problemTitle(status),
		"status": status,
		"detail": detail,
	})
}

func problemType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "urn:chatisp:problem:unauthorized"
	case http.StatusForbidden:
		return "urn:chatisp:problem:forbidden"
	case http.StatusConflict:
		return "urn:chatisp:problem:conflict"
	case http.StatusUnprocessableEntity:
		return "urn:chatisp:problem:invalid"
	case http.StatusServiceUnavailable:
		return "urn:chatisp:problem:unavailable"
	default:
		return "about:blank"
	}
}

func problemTitle(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "Não autenticado"
	case http.StatusForbidden:
		return "Acesso negado"
	case http.StatusConflict:
		return "Conflito"
	case http.StatusUnprocessableEntity:
		return "Dados inválidos"
	case http.StatusServiceUnavailable:
		return "Serviço indisponível"
	default:
		return "Erro"
	}
}
