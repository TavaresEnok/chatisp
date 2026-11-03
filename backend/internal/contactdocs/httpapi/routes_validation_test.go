package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatisp/internal/contactdocs/httpapi"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestValidationErrorOmitsDocument(t *testing.T) {
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	httpapi.Register(humachi.New(router, cfg), httpapi.Dependencies{})
	contactID := uuid.NewString()
	secrets := []string{
		cpfDigits,
		cnpjDigits,
		otherCPFDigits,
		"529.982.247-25",
		"11.222.333/0001-81",
		"390.533.447-05",
		"5.2998224725e+10",
		"1.1222333000181e+13",
	}
	cases := []struct {
		name, method, path, body string
	}{
		{name: "busca com documento numérico", method: http.MethodPost, path: "/api/panel/contacts/document-search", body: `{"document":52998224725}`},
		{name: "busca com cnpj numérico", method: http.MethodPost, path: "/api/panel/contacts/document-search", body: `{"document":11222333000181}`},
		{name: "put sem request_id e com cpf", method: http.MethodPut, path: "/api/panel/contacts/" + contactID + "/document", body: `{"document":"529.982.247-25"}`},
		{name: "put sem request_id e com cnpj", method: http.MethodPut, path: "/api/panel/contacts/" + contactID + "/document", body: `{"expected_version":1,"document":"11.222.333/0001-81"}`},
		{name: "put com documento objeto", method: http.MethodPut, path: "/api/panel/contacts/" + contactID + "/document", body: `{"request_id":"` + uuid.NewString() + `","expected_version":1,"document":{"cpf":"39053344705"}}`},
		{name: "json incompleto com cpf", method: http.MethodPost, path: "/api/panel/contacts/document-search", body: `{"document":"52998224725"`},
		{name: "json incompleto com cnpj", method: http.MethodDelete, path: "/api/panel/contacts/" + contactID + "/document", body: `{"request_id":"` + uuid.NewString() + `","document":"11222333000181"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postRaw(t, router, tc.method, tc.path, tc.body)
			if status < 400 || status >= 500 || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusServiceUnavailable {
				t.Fatalf("status %d body %s", status, raw)
			}
			for _, secret := range secrets {
				if strings.Contains(raw, secret) {
					t.Fatalf("erro ecoou %s em %s", secret, raw)
				}
			}
			if strings.Contains(raw, `"value"`) {
				t.Fatalf("erro ainda tem value: %s", raw)
			}
		})
	}

	status, raw := postRaw(t, router, http.MethodGet, "/api/panel/contacts/"+contactID+"/document", "")
	if status != http.StatusServiceUnavailable || !strings.Contains(raw, "indisponível") {
		t.Fatalf("get sem serviço: %d %s", status, raw)
	}
}

func postRaw(t *testing.T, handler http.Handler, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://panel.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
