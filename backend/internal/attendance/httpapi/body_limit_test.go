package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestTextBodyLimitsAllowEscapedSendAndFarewell(t *testing.T) {
	r := chi.NewRouter()
	Register(humachi.New(r, huma.DefaultConfig("test", "1")), Dependencies{})
	base := "/api/panel/inboxes/" + uuid.NewString() + "/conversations/" + uuid.NewString()
	payload, err := json.Marshal(map[string]any{"body": strings.Repeat("<", 16000), "client_request_id": "x", "expected_version": 1})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string, body []byte) int {
		req := httptest.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	farewell, err := json.Marshal(map[string]any{"farewell_body": strings.Repeat("<", 16000), "farewell_request_id": "x", "client_request_id": "close", "expected_version": 1, "reason": "fim", "result": "resolved", "summary": "fim"})
	if err != nil {
		t.Fatal(err)
	}
	// Missing service returns 503 after Huma accepts and reads the body.
	if got := request("/messages", payload); got != http.StatusServiceUnavailable {
		t.Fatalf("texto escapado: %d", got)
	}
	if got := request("/close", farewell); got != http.StatusServiceUnavailable {
		t.Fatalf("despedida escapada: %d", got)
	}
	if got := request("/assume", payload); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("limite assumir: %d", got)
	}
	if got := request("/close", append(bytes.Clone(farewell), bytes.Repeat([]byte(" "), 128<<10)...)); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("limite fechamento: %d", got)
	}
	if got := request("/messages", append(payload, bytes.Repeat([]byte(" "), 128<<10)...)); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("limite mensagens: %d", got)
	}
}
