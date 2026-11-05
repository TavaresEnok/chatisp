package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestJSONLogRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	log := New("info", &buf)
	secret := "super-secret-value"
	dsn := "postgres://user:" + secret + "@127.0.0.1:5432/chatisp"
	log.Info("evento",
		"database_url", dsn,
		"token", secret,
		"detail", "Authorization: Bearer "+secret,
		"error", errors.New("password="+secret),
		"component", "api",
	)
	line := buf.String()
	if strings.Contains(line, secret) || strings.Contains(line, "postgres://") {
		t.Fatalf("log expôs segredo: %s", line)
	}
	if !strings.Contains(line, "[redacted]") {
		t.Fatalf("log sem redação: %s", line)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["msg"] != "evento" {
		t.Fatalf("msg = %v", payload["msg"])
	}
	if payload["component"] != "api" {
		t.Fatalf("component = %v", payload["component"])
	}
}

func TestMessageWithDSNIsRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := New("debug", &buf)
	log.Info("postgres://user:super-secret-value@127.0.0.1/chatisp")
	if strings.Contains(buf.String(), "super-secret-value") {
		t.Fatalf("mensagem expôs segredo: %s", buf.String())
	}
}

func TestTenantFromContextIsLoggedAndOtherwiseAbsent(t *testing.T) {
	var out bytes.Buffer
	log := New("info", &out)
	tenant := uuid.New()
	log.InfoContext(WithTenant(context.Background(), tenant), "com tenant", "component", "teste")
	log.Info("sem tenant", "component", "teste")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("linhas: %q", out.String())
	}
	if !strings.Contains(lines[0], `"tenant_id":"`+tenant.String()+`"`) {
		t.Fatalf("tenant_id ausente: %s", lines[0])
	}
	if strings.Contains(lines[1], "tenant_id") {
		t.Fatalf("tenant_id sem contexto: %s", lines[1])
	}
}
