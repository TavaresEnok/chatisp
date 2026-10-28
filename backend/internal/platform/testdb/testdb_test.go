package testdb

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestNPMPackagePinIsExact(t *testing.T) {
	module, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(module, "tools", "pg", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Private         bool              `json:"private"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Private {
		t.Fatal("backend/tools/pg/package.json precisa ser privado")
	}
	got := doc.DevDependencies[NPMPackage]
	if got != NPMPackageVersion {
		t.Fatalf("devDependency %s = %q", NPMPackage, got)
	}
	if strings.ContainsAny(got, "^~><*") || strings.Contains(got, " ") {
		t.Fatalf("versão não é exata: %s", got)
	}
	mod, err := os.ReadFile(filepath.Join(module, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(mod)
	if strings.Contains(text, "fergusstrange/embedded-postgres") || strings.Contains(text, "xi2/xz") {
		t.Fatal("go.mod ainda pede download de binário em tempo de teste")
	}
	if _, err := os.Stat(filepath.Join(module, "vendor", "github.com", "fergusstrange")); !os.IsNotExist(err) {
		t.Fatal("vendor ainda contém embedded-postgres")
	}
	if _, err := os.Stat(filepath.Join(module, "vendor", "github.com", "xi2")); !os.IsNotExist(err) {
		t.Fatal("vendor ainda contém xi2/xz")
	}
}

func TestMissingBinariesMentionNPM(t *testing.T) {
	err := hydrateSymlinks(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "npm ci --prefix backend/tools/pg") {
		t.Fatalf("erro: %v", err)
	}
}

func TestHydrateSymlinksAreIdempotentUnderConcurrency(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "native", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "libpq.so.5.18"), []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]linkSpec{{
		Source: "native/lib/libpq.so.5.18",
		Target: "native/lib/libpq.so.5",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "native", "pg-symlinks.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- hydrateSymlinks(root)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := hydrateSymlinks(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(lib, "libpq.so.5"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "libpq.so.5.18" {
		t.Fatalf("symlink: %s", got)
	}
}

func TestHydrateRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "native"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]linkSpec{{
		Source: "native/../../etc/passwd",
		Target: "native/libpq.so.5",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "native", "pg-symlinks.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hydrateSymlinks(root); err == nil {
		t.Fatal("symlink fora do pacote deveria ser recusado")
	}
}

func TestLongDataDirUsesShortSocketPath(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, strings.Repeat("d", 90))
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	run, cleanup, err := chooseRunDir(real)
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(run, socketFileName)
	if len(socket) > maxUnixSocketPath {
		t.Fatalf("socket longo demais (%d): %s", len(socket), socket)
	}
	if len(filepath.Join(real, socketFileName)) <= maxUnixSocketPath {
		if run != real {
			t.Fatalf("diretório curto foi substituído: %s", run)
		}
		return
	}
	if run == real {
		t.Fatal("diretório longo manteve o socket no caminho original")
	}
	target, err := os.Readlink(run)
	if err != nil {
		t.Fatal(err)
	}
	if target != real {
		t.Fatalf("symlink aponta para %s", target)
	}
}

func TestStartRunsPostgreSQL18(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env, err := Start(ctx)
	if err != nil {
		t.Fatalf("PostgreSQL de teste não subiu: %s", err)
	}
	t.Cleanup(env.Close)
	if !env.Embedded || env.BundleVersion != BundleVersion || env.ServerVersionNum != EmbeddedServerVersionNum {
		t.Fatalf("embutido %s server_version %s (%d)", env.BundleVersion, env.ServerVersion, env.ServerVersionNum)
	}
	if !strings.HasPrefix(env.ServerVersion, "18.4") {
		t.Fatalf("server_version %s", env.ServerVersion)
	}
	parsed, err := url.Parse(env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	socketDir := parsed.Query().Get("host")
	if socketDir == "" {
		t.Fatal("DSN sem diretório de socket")
	}
	if _, err := os.Stat(filepath.Join(socketDir, socketFileName)); err != nil {
		t.Fatalf("socket ausente: %s", err)
	}

	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var (
		listen   string
		buffers  string
		dsm      string
		fsync    string
		addrNull bool
		exts     int
	)
	if err := conn.QueryRow(ctx, `SHOW listen_addresses`).Scan(&listen); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SHOW shared_buffers`).Scan(&buffers); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SHOW dynamic_shared_memory_type`).Scan(&dsm); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SHOW fsync`).Scan(&fsync); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT inet_server_addr() IS NULL`).Scan(&addrNull); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_available_extensions WHERE name IN ('unaccent', 'pg_trgm')`).Scan(&exts); err != nil {
		t.Fatal(err)
	}
	if listen != "" || buffers != "16MB" || dsm != "mmap" || fsync != "off" || !addrNull || exts != 2 {
		t.Fatalf("listen=%q buffers=%q dsm=%q fsync=%q unix=%v ext=%d", listen, buffers, dsm, fsync, addrNull, exts)
	}
	env.Close()
	if _, err := os.Lstat(socketDir); !os.IsNotExist(err) {
		t.Fatalf("diretório de dados permaneceu após Close: %v", err)
	}
}

func TestInvalidExternalURLFails(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "http://example.invalid/db")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Start(ctx); err == nil {
		t.Fatal("URL inválida deveria falhar")
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
