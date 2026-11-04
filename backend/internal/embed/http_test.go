package embed

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestPutOrigemDoNavegadorRecusada(t *testing.T) {
	router := chi.NewRouter()
	Mount(router, Dependencies{TrustedOrigins: []string{"https://painel.example"}, Panels: New(nil)})
	req := httptest.NewRequest(http.MethodPut, "/api/panel/embed", strings.NewReader(`{"allowed_origin":"https://erp.example","panel_url":"https://outro.example/painel"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "outro.example") || strings.Contains(rec.Body.String(), "evil.example") {
		t.Fatalf("resposta ecoou origem: %s", rec.Body.String())
	}
}

func TestPutRecusaCampoDeMensagem(t *testing.T) {
	router := chi.NewRouter()
	Mount(router, Dependencies{TrustedOrigins: []string{"https://painel.example"}, Panels: New(nil)})
	req := httptest.NewRequest(http.MethodPut, "/api/panel/embed", strings.NewReader(`{"allowed_origin":"https://erp.example","panel_url":"https://erp.example/painel","body":"texto da mensagem","cpf":"000"}`))
	req.Header.Set("Origin", "https://painel.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "texto da mensagem") || strings.Contains(rec.Body.String(), "000") {
		t.Fatalf("ecoou corpo: %s", rec.Body.String())
	}
}
