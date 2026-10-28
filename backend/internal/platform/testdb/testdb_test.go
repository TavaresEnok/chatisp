package testdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func TestCachePathSurvivesReadOnlyHome(t *testing.T) {
	t.Setenv("HOME", "/home/enok")
	t.Setenv("XDG_CACHE_HOME", "")

	dir, err := cachePath()
	if err != nil {
		t.Fatalf("cache: %s", err)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %s", err)
	}
	if filepath.Join(base, cacheDirName) != dir {
		t.Fatalf("cache %s fora de UserCacheDir %s", dir, base)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cache não foi criado: %s", err)
	}
}

func TestBundleVersionIsExplicit1860(t *testing.T) {
	if BundleVersion != "18.6.0" {
		t.Fatalf("pin do binário: %s", BundleVersion)
	}
	if BundleVersion == string(embeddedpostgres.V18) {
		t.Fatalf("o pin coincidiu com o default da biblioteca (%s)", embeddedpostgres.V18)
	}
}

func TestStartRunsPostgreSQL18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := Start(ctx)
	if err != nil {
		t.Fatalf("PostgreSQL de teste não subiu: %s", err)
	}
	t.Cleanup(env.Close)
	if env.AdminURL == "" {
		t.Fatal("conexão administrativa ausente")
	}
	if strings.Contains(t.Name(), "postgres://") {
		t.Fatal("nome do teste inesperado")
	}
	if env.ServerVersionNum/10000 != 18 {
		t.Fatalf("server_version %s (%d)", env.ServerVersion, env.ServerVersionNum)
	}
	if env.Embedded {
		if env.BundleVersion != "18.6.0" || env.ServerVersionNum != 180006 {
			t.Fatalf("embutido %s server_version %s (%d)", env.BundleVersion, env.ServerVersion, env.ServerVersionNum)
		}
		if strings.HasPrefix(env.ServerVersion, "18.6.1") {
			t.Fatalf("subiu o default da biblioteca: %s", env.ServerVersion)
		}
		cache, err := cachePath()
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(cache, zonkyCacheName()))
		if err != nil || info.IsDir() || info.Size() < 1_000_000 {
			t.Fatalf("artefato zonky ausente: %v", err)
		}
		if strings.Contains(zonkyRepository, "theseus") || !strings.Contains(zonkyRepository, "repo1.maven.org/maven2") {
			t.Fatalf("repositório dos binários: %s", zonkyRepository)
		}
	}
}

func TestRedactCredentialsTerminates(t *testing.T) {
	input := "falha postgresql://postgres:senha@127.0.0.1:5432/db extra postgresql://u:p@localhost/x fim postgresql://127.0.0.1/plain"
	done := make(chan string, 1)
	go func() {
		done <- redact(input, "senha")
	}()
	select {
	case got := <-done:
		if strings.Contains(got, "senha") || strings.Contains(got, "u:p") || strings.Contains(got, "postgres:") {
			t.Fatalf("credencial restante: %s", got)
		}
		if strings.Count(got, "postgresql://[redacted]@") != 2 {
			t.Fatalf("redação: %s", got)
		}
		if !strings.Contains(got, "postgresql://127.0.0.1/plain") {
			t.Fatalf("url sem credencial alterada: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("redação de URL não terminou")
	}
}
