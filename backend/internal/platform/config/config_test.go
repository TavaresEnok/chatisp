package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsMissingRequiredWithoutSecrets(t *testing.T) {
	t.Setenv("CHATISP_ENV", "production")
	t.Setenv("CHATISP_DATABASE_URL", "postgres://user:from-process-env@127.0.0.1/chatisp")

	_, err := Load(nil)
	if err == nil {
		t.Fatal("esperava falha")
	}
	message := err.Error()
	if !strings.Contains(message, "configuração obrigatória ausente") {
		t.Fatalf("mensagem: %s", message)
	}
	if !strings.Contains(message, "CHATISP_ENV") || !strings.Contains(message, "CHATISP_DATABASE_URL") {
		t.Fatalf("mensagem: %s", message)
	}
	if strings.Contains(message, "from-process-env") || strings.Contains(message, "postgres://") {
		t.Fatalf("mensagem expôs valor: %s", message)
	}
}

func TestLoadInvalidValueDoesNotEchoSecret(t *testing.T) {
	secret := "super-secret-value"
	_, err := Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=postgres://user:" + secret + "@127.0.0.1:5432/chatisp",
		"CHATISP_SHUTDOWN_TIMEOUT=" + secret,
	})
	if err == nil {
		t.Fatal("esperava falha")
	}
	message := err.Error()
	if strings.Contains(message, secret) || strings.Contains(message, "postgres://") {
		t.Fatalf("mensagem expôs segredo: %s", message)
	}
	if !strings.Contains(message, "CHATISP_SHUTDOWN_TIMEOUT") {
		t.Fatalf("mensagem: %s", message)
	}

	_, err = Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=postgres://user:" + secret + "@127.0.0.1:5432/chatisp",
		"CHATISP_HTTP_ADDR=nao-e-endereco",
	})
	if err == nil {
		t.Fatal("esperava falha de endereço")
	}
	message = err.Error()
	if strings.Contains(message, secret) || strings.Contains(message, "postgres://") {
		t.Fatalf("mensagem expôs segredo: %s", message)
	}
	if !strings.Contains(message, "CHATISP_HTTP_ADDR") {
		t.Fatalf("mensagem: %s", message)
	}
}

func TestLoadAcceptsMinimalConfig(t *testing.T) {
	cfg, err := Load([]string{
		"CHATISP_ENV=test",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Fatalf("endereços padrão inesperados: %#v", cfg)
	}
	if cfg.LogLevel != "info" || cfg.MigrationsDir != "db/migrations" {
		t.Fatalf("padrões inesperados: %#v", cfg)
	}
}

func TestValidateRejectsBadDatabaseURLWithoutValue(t *testing.T) {
	cfg := Config{
		Env:             "development",
		HTTPAddr:        "127.0.0.1:8080",
		MetricsAddr:     "127.0.0.1:9090",
		DatabaseURL:     "mysql://user:super-secret-value@127.0.0.1/db",
		LogLevel:        "info",
		ShutdownTimeout: 15_000_000_000,
		MigrationsDir:   "db/migrations",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("esperava falha")
	}
	if strings.Contains(err.Error(), "super-secret-value") || strings.Contains(err.Error(), "mysql://") {
		t.Fatalf("mensagem expôs valor: %s", err)
	}
	if !strings.Contains(err.Error(), "CHATISP_DATABASE_URL") {
		t.Fatalf("mensagem: %s", err)
	}
}

func TestOptionalIdentityConfigDoesNotEchoSecrets(t *testing.T) {
	secret := "super-secret-smtp"
	_, err := Load([]string{
		"CHATISP_ENV=test",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp",
		"CHATISP_APP_DATABASE_URL=postgres://app:" + secret + "@127.0.0.1:5432/chatisp",
		"CHATISP_SMTP_PASSWORD=" + secret,
		"CHATISP_PANEL_ORIGINS=https://user:" + secret + "@painel.example/extra",
	})
	if err == nil {
		t.Fatal("esperava falha")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("mensagem expôs segredo: %s", err)
	}
	if !strings.Contains(err.Error(), "CHATISP_PLATFORM_DATABASE_URL") || !strings.Contains(err.Error(), "CHATISP_PANEL_ORIGINS") || !strings.Contains(err.Error(), "CHATISP_SMTP_ADDR") {
		t.Fatalf("mensagem: %s", err)
	}

	cfg, err := Load([]string{
		"CHATISP_ENV=test",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp",
		"CHATISP_APP_DATABASE_URL=postgres://app:local@127.0.0.1:5432/chatisp",
		"CHATISP_PLATFORM_DATABASE_URL=postgres://platform:local@127.0.0.1:5432/chatisp",
		"CHATISP_SMTP_ADDR=127.0.0.1:2525",
		"CHATISP_SMTP_FROM=painel@example.com",
		"CHATISP_PANEL_ORIGINS=https://painel.example, http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.PanelOrigins) != 2 || cfg.PanelOrigins[0] != "https://painel.example" {
		t.Fatalf("origens: %#v", cfg.PanelOrigins)
	}
}
