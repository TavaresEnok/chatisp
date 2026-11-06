package publicapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsTrailingAndOversizedBodies(t *testing.T) {
	for _, body := range []string{`{"name":"a"}{"name":"b"}`, `{"name":"a"}` + strings.Repeat(" ", 8192), ""} {
		var v contactBody
		if err := decodeJSON(httptest.NewRequest("POST", "/", strings.NewReader(body)), &v); err == nil {
			t.Fatalf("corpo inválido aceito: %q", body[:min(len(body), 30)])
		}
	}
}

func TestTextJSONLimitAcceptsEscapedBodyAndStillBoundsInput(t *testing.T) {
	body := `{"body":"` + strings.Repeat(`\u0061`, publicTextLimit) + `","client_request_id":"x","expected_version":1}`
	var parsed textBody
	if err := decodeJSONLimit(httptest.NewRequest("POST", "/", strings.NewReader(body)), &parsed, publicTextJSONLimit); err != nil || len(parsed.Body) != publicTextLimit {
		t.Fatalf("texto escapado válido recusado: %v", err)
	}
	body += strings.Repeat(" ", publicTextJSONLimit)
	if err := decodeJSONLimit(httptest.NewRequest("POST", "/", strings.NewReader(body)), &parsed, publicTextJSONLimit); err == nil {
		t.Fatal("JSON acima do limite aceito")
	}
}
