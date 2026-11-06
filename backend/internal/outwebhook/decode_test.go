package outwebhook

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodePanelRejectsTrailingAndOversizedBodies(t *testing.T) {
	for _, body := range []string{`{"url":"https://example.com"}{}`, `{"url":"https://example.com"}` + strings.Repeat(" ", 8192), ""} {
		var v struct {
			URL string `json:"url"`
		}
		if err := decodePanel(httptest.NewRequest("POST", "/", strings.NewReader(body)), &v); err == nil {
			t.Fatalf("corpo inválido aceito: %q", body[:min(len(body), 30)])
		}
	}
}
