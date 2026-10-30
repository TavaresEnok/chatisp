package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/metrics"
)

func TestHealthReadyAndProblem(t *testing.T) {
	secret := "super-secret-value"
	meter := metrics.New()
	var logs bytes.Buffer
	api := New(Dependencies{
		Metrics: meter,
		Logger:  logging.New("debug", &logs),
		Ready: func(context.Context) error {
			return errors.New("postgres://user:" + secret + "@127.0.0.1/chatisp")
		},
	})

	health := httptest.NewRecorder()
	api.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status %d", health.Code)
	}
	assertStatusField(t, health.Body.Bytes(), "ok")

	ready := httptest.NewRecorder()
	api.Handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status %d body %s", ready.Code, ready.Body.String())
	}
	assertProblem(t, ready, http.StatusServiceUnavailable)
	if strings.Contains(ready.Body.String(), secret) || strings.Contains(ready.Body.String(), "postgres://") {
		t.Fatalf("ready expôs segredo: %s", ready.Body.String())
	}

	missing := httptest.NewRecorder()
	api.Handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/contatos/5511999999999", nil))
	assertProblem(t, missing, http.StatusNotFound)
	if strings.Contains(missing.Body.String(), "5511999999999") {
		t.Fatalf("404 ecoou o caminho: %s", missing.Body.String())
	}

	method := httptest.NewRecorder()
	api.Handler.ServeHTTP(method, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	assertProblem(t, method, http.StatusMethodNotAllowed)
	assertAllow(t, method.Header(), []string{http.MethodGet}, []string{http.MethodPost})

	spec := httptest.NewRecorder()
	api.Handler.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if spec.Code != http.StatusOK {
		t.Fatalf("openapi status %d", spec.Code)
	}
	if !strings.Contains(spec.Header().Get("Content-Type"), "application/openapi+json") {
		t.Fatalf("content-type %s", spec.Header().Get("Content-Type"))
	}
	var document map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	paths, _ := document["paths"].(map[string]any)
	if _, ok := paths["/healthz"]; !ok {
		t.Fatalf("paths sem /healthz: %v", document["paths"])
	}
	if _, ok := paths["/readyz"]; !ok {
		t.Fatalf("paths sem /readyz")
	}

	metricsBody := httptest.NewRecorder()
	api.Handler.ServeHTTP(metricsBody, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertProblem(t, metricsBody, http.StatusNotFound)

	scraped, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	text := string(scraped)
	if strings.Contains(text, "5511999999999") || strings.Contains(text, secret) {
		t.Fatalf("métricas com alta cardinalidade ou segredo: %s", text)
	}
	if !strings.Contains(text, `route="/healthz"`) || !strings.Contains(text, `route="unmatched"`) {
		t.Fatalf("rotas inesperadas nas métricas:\n%s", text)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("log expôs segredo: %s", logs.String())
	}
}

func TestMethodNotAllowedSetsAllow(t *testing.T) {
	api := New(Dependencies{})

	health := httptest.NewRecorder()
	api.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	assertProblem(t, health, http.StatusMethodNotAllowed)
	assertAllow(t, health.Header(), []string{http.MethodGet}, []string{http.MethodPost, http.MethodPut, http.MethodHead})
	if got := health.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, esperado GET", got)
	}

	ready := httptest.NewRecorder()
	api.Handler.ServeHTTP(ready, httptest.NewRequest(http.MethodDelete, "/readyz", nil))
	assertProblem(t, ready, http.StatusMethodNotAllowed)
	assertAllow(t, ready.Header(), []string{http.MethodGet}, []string{http.MethodDelete})

	spec := httptest.NewRecorder()
	api.Handler.ServeHTTP(spec, httptest.NewRequest(http.MethodPut, "/openapi.json", nil))
	assertProblem(t, spec, http.StatusMethodNotAllowed)
	assertAllow(t, spec.Header(), []string{http.MethodGet}, []string{http.MethodPut})

	router := chi.NewRouter()
	router.Method(http.MethodGet, "/itens/{id}", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Method(http.MethodPut, "/itens/{id}", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.MethodNotAllowed(methodNotAllowed(router))
	several := httptest.NewRecorder()
	router.ServeHTTP(several, httptest.NewRequest(http.MethodPost, "/itens/42", nil))
	assertProblem(t, several, http.StatusMethodNotAllowed)
	assertAllow(t, several.Header(), []string{http.MethodGet, http.MethodPut}, []string{http.MethodPost, http.MethodDelete})
	if got := several.Header().Get("Allow"); got != "GET, PUT" {
		t.Fatalf("Allow = %q, esperado GET, PUT", got)
	}

	missing := httptest.NewRecorder()
	api.Handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/nao-existe", nil))
	assertProblem(t, missing, http.StatusNotFound)
	if missing.Header().Get("Allow") != "" {
		t.Fatalf("404 anunciou Allow: %q", missing.Header().Get("Allow"))
	}
}

func TestRecovererHidesPanic(t *testing.T) {
	var logs bytes.Buffer
	handler := recoverMiddleware(logging.New("info", &logs))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("super-secret-value")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assertProblem(t, recorder, http.StatusInternalServerError)
	body := recorder.Body.String()
	if strings.Contains(body, "super-secret-value") || strings.Contains(body, "goroutine") || strings.Contains(body, "panic") {
		t.Fatalf("corpo expôs panic: %s", body)
	}
	if strings.Contains(logs.String(), "super-secret-value") || strings.Contains(logs.String(), "goroutine") {
		t.Fatalf("log expôs panic: %s", logs.String())
	}
}

func TestOpenAPIIsDeterministic(t *testing.T) {
	first, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := OpenAPIJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("OpenAPI divergiu na geração %d", i)
		}
	}
	if !bytes.Contains(first, []byte(`"/healthz"`)) || !bytes.Contains(first, []byte(`"/readyz"`)) {
		t.Fatalf("documento sem rotas: %s", first)
	}
}

func assertProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status %d, esperado %d, corpo %s", recorder.Code, status, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("content-type %s", recorder.Header().Get("Content-Type"))
	}
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != status || problem.Title == "" || problem.Type == "" || problem.Detail == "" {
		t.Fatalf("problema incompleto: %#v", problem)
	}
	lower := strings.ToLower(recorder.Body.String())
	if strings.Contains(lower, "stack") || strings.Contains(lower, "goroutine") {
		t.Fatalf("problema contém stack: %s", recorder.Body.String())
	}
}

func assertAllow(t *testing.T, header http.Header, want []string, forbid []string) {
	t.Helper()
	got := header.Get("Allow")
	if got == "" {
		t.Fatal("Allow ausente")
	}
	listed := map[string]bool{}
	for _, part := range strings.Split(got, ",") {
		listed[strings.TrimSpace(part)] = true
	}
	for _, method := range want {
		if !listed[method] {
			t.Fatalf("Allow %q não inclui %s", got, method)
		}
	}
	for _, method := range forbid {
		if listed[method] {
			t.Fatalf("Allow %q inclui %s", got, method)
		}
	}
}

func assertStatusField(t *testing.T, body []byte, want string) {
	t.Helper()
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != want {
		t.Fatalf("status %q, corpo %s", payload.Status, body)
	}
}

func TestReadyWhenAvailable(t *testing.T) {
	api := New(Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	request = request.WithContext(context.Background())
	api.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		body, _ := io.ReadAll(recorder.Body)
		t.Fatalf("status %d corpo %s", recorder.Code, body)
	}
	assertStatusField(t, recorder.Body.Bytes(), "ready")
}

func TestWebhookAndRealtimeRouting(t *testing.T) {
	var webhookCalled, realtimeCalled bool
	webhookStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("webhook-ok"))
	})
	realtimeStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		realtimeCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("realtime-ok"))
	})

	api := New(Dependencies{
		Webhook:  webhookStub,
		Realtime: realtimeStub,
	})

	// Test webhook route
	reqWh := httptest.NewRequest(http.MethodPost, "/api/webhooks/meta", strings.NewReader("test"))
	recWh := httptest.NewRecorder()
	api.Handler.ServeHTTP(recWh, reqWh)
	if recWh.Code != http.StatusOK || !webhookCalled {
		t.Fatalf("webhook status %d, called %v", recWh.Code, webhookCalled)
	}

	// Test realtime route
	reqRt := httptest.NewRequest(http.MethodGet, "/api/panel/realtime", nil)
	recRt := httptest.NewRecorder()
	api.Handler.ServeHTTP(recRt, reqRt)
	if recRt.Code != http.StatusOK || !realtimeCalled {
		t.Fatalf("realtime status %d, called %v", recRt.Code, realtimeCalled)
	}

	// Test nil dependencies respond 503
	apiNil := New(Dependencies{})
	recNilWh := httptest.NewRecorder()
	apiNil.Handler.ServeHTTP(recNilWh, reqWh)
	if recNilWh.Code != http.StatusServiceUnavailable {
		t.Fatalf("webhook nil code: %d", recNilWh.Code)
	}

	recNilRt := httptest.NewRecorder()
	apiNil.Handler.ServeHTTP(recNilRt, reqRt)
	if recNilRt.Code != http.StatusServiceUnavailable {
		t.Fatalf("realtime nil code: %d", recNilRt.Code)
	}
}
