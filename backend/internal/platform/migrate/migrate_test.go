package migrate

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/logging"
)

func TestApplyRejectsMissingDirectoryBeforeConnect(t *testing.T) {
	cfg := validConfig(t)
	cfg.MigrationsDir = t.TempDir() + "/ausente"
	err := Apply(context.Background(), cfg, logging.New("info", &bytes.Buffer{}))
	if err == nil {
		t.Fatal("esperava falha")
	}
	if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("mensagem expôs segredo: %s", err)
	}
	if !strings.Contains(err.Error(), "diretório de migrações indisponível") {
		t.Fatalf("mensagem: %s", err)
	}
}

func TestApplyConnectionFailureDoesNotLeakSecret(t *testing.T) {
	cfg := validConfig(t)
	cfg.MigrationsDir = t.TempDir()
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Apply(ctx, cfg, logging.New("info", &logs))
	if err == nil {
		t.Fatal("esperava falha de conexão")
	}
	combined := err.Error() + "\n" + logs.String()
	if strings.Contains(combined, "super-secret-value") || strings.Contains(combined, "postgres://") {
		t.Fatalf("saída expôs segredo: %s", combined)
	}
	if !strings.Contains(err.Error(), "falha ao conectar ao banco para migração") {
		t.Fatalf("mensagem: %s", err)
	}
}

func validConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load([]string{
		"CHATISP_ENV=test",
		"CHATISP_DATABASE_URL=postgres://user:super-secret-value@127.0.0.1:1/chatisp?sslmode=disable",
		"CHATISP_MIGRATIONS_DIR=" + t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
