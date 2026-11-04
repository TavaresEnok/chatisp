package opsalert

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestListWithoutServiceIsUnavailable(t *testing.T) {
	router := chi.NewRouter()
	Mount(router, Dependencies{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/panel/ops-alerts", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatal(response.Header().Get("Content-Type"))
	}
}
