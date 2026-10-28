package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestObserveCollapsesUnsafeLabels(t *testing.T) {
	meter := New()
	meter.Observe("GET", "/healthz", 200, time.Millisecond)
	meter.Observe("TRACE", "/contatos/5511999999999?token=abc", 200, time.Millisecond)
	meter.Observe("GET", "/schemas/{schema}", 200, time.Millisecond)

	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "5511999999999") || strings.Contains(text, "token") || strings.Contains(text, "TRACE") {
		t.Fatalf("label insegura:\n%s", text)
	}
	if !strings.Contains(text, `method="other"`) || !strings.Contains(text, `route="unmatched"`) {
		t.Fatalf("colapso ausente:\n%s", text)
	}
	if !strings.Contains(text, `route="/healthz"`) || !strings.Contains(text, `route="/schemas/{schema}"`) {
		t.Fatalf("rotas de baixa cardinalidade ausentes:\n%s", text)
	}
	if !strings.Contains(text, `chatisp_build_info`) || !strings.Contains(text, `version="0.1.0"`) {
		t.Fatalf("build info ausente:\n%s", text)
	}
}
