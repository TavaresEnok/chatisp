// Package testdb starts PostgreSQL for tests.
// The application process must not import this package.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
)

const (
	// BundleVersion is the explicit PostgreSQL 18.6 packaging pin.
	// The library default tracks a later packaging revision and is rejected.
	BundleVersion = "18.6.0"

	// EnvDatabaseURL selects an external server and disables the embedded one.
	EnvDatabaseURL = "CHATISP_TEST_DATABASE_URL"

	cacheDirName = "chatisp-embedded-pg"

	// zonkyRepository is the Maven Central root used by embedded-postgres v1.
	// Artifact paths are io/zonky/test/postgres/embedded-postgres-binaries-*.
	// The theseus-rs bundles pinned by embedded-postgres v2 are not used.
	zonkyRepository = "https://repo1.maven.org/maven2"

	postgres18Major = 18
	postgres186Num  = 180006
)

// Env is a PostgreSQL 18 server usable by tests.
// AdminURL is a superuser DSN kept in memory. Do not log Env.
type Env struct {
	AdminURL         string
	Embedded         bool
	ServerVersion    string
	ServerVersionNum int
	BundleVersion    string

	stop func()
}

// Start boots PostgreSQL 18. A defined CHATISP_TEST_DATABASE_URL is used as-is.
// Otherwise PostgreSQL 18.6.0 is embedded on a free port. Failures are errors,
// never a silent skip.
func Start(ctx context.Context) (*Env, error) {
	if raw, ok := os.LookupEnv(EnvDatabaseURL); ok && strings.TrimSpace(raw) != "" {
		return startExternal(ctx, strings.TrimSpace(raw))
	}
	return startEmbedded(ctx)
}

// Close stops an embedded server and removes its temporary data directory.
func (e *Env) Close() {
	if e == nil || e.stop == nil {
		return
	}
	e.stop()
	e.stop = nil
}

func startExternal(ctx context.Context, raw string) (*Env, error) {
	if !validPostgresURL(raw) {
		return nil, errors.New("CHATISP_TEST_DATABASE_URL inválida")
	}
	version, num, err := readServerVersion(ctx, raw)
	if err != nil {
		return nil, err
	}
	if num/10000 != postgres18Major {
		return nil, fmt.Errorf("CHATISP_TEST_DATABASE_URL precisa ser PostgreSQL 18 (server_version %s)", version)
	}
	return &Env{
		AdminURL:         raw,
		Embedded:         false,
		ServerVersion:    version,
		ServerVersionNum: num,
	}, nil
}

func startEmbedded(ctx context.Context) (*Env, error) {
	if BundleVersion == string(embeddedpostgres.V18) {
		return nil, errors.New("versão embutida recusada: o default da biblioteca não é 18.6.0")
	}
	if BundleVersion != "18.6.0" {
		return nil, errors.New("versão embutida recusada")
	}

	password, err := randomPassword()
	if err != nil {
		return nil, errors.New("falha ao preparar o PostgreSQL embutido")
	}
	cacheDir, err := cachePath()
	if err != nil {
		return nil, err
	}
	unlock, err := lockCache(cacheDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// /tmp on this host is a small noexec mount. Keep the data directory beside
	// the cached binaries so initdb can map shared libraries.
	workParent := filepath.Join(cacheDir, "work")
	if err := os.MkdirAll(workParent, 0o755); err != nil {
		return nil, errors.New("falha ao criar diretório temporário do PostgreSQL")
	}
	workDir, err := os.MkdirTemp(workParent, "pg-")
	if err != nil {
		return nil, errors.New("falha ao criar diretório temporário do PostgreSQL")
	}
	port, err := freePort()
	if err != nil {
		_ = os.RemoveAll(workDir)
		return nil, err
	}

	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.PostgresVersion(BundleVersion)).
		Port(port).
		Database("chatisp").
		Username("postgres").
		Password(password).
		Locale("C").
		Encoding("UTF8").
		StartTimeout(3 * time.Minute).
		CachePath(cacheDir).
		RuntimePath(workDir).
		DataPath(filepath.Join(workDir, "data")).
		BinaryRepositoryURL(zonkyRepository).
		Logger(io.Discard)
	pg := embeddedpostgres.NewDatabase(cfg)
	cleanup := func() {
		_ = pg.Stop()
		_ = os.RemoveAll(workDir)
	}
	if err := pg.Start(); err != nil {
		cleanup()
		detail := redact(err.Error(), password)
		if detail == "" {
			detail = "sem log do processo"
		}
		return nil, fmt.Errorf("falha ao iniciar PostgreSQL 18.6.0 embutido: %s", detail)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return nil, errors.New("falha ao iniciar PostgreSQL 18.6.0 embutido: tempo esgotado")
	}
	info, statErr := os.Stat(filepath.Join(cacheDir, zonkyCacheName()))
	if statErr != nil || info.IsDir() || info.Size() < 1_000_000 {
		cleanup()
		return nil, errors.New("PostgreSQL embutido não usou o artefato zonky")
	}

	adminURL := adminDSN("postgres", password, "chatisp", port)
	if !validPostgresURL(adminURL) {
		cleanup()
		return nil, errors.New("PostgreSQL embutido não publicou uma porta livre")
	}
	version, num, err := readServerVersion(ctx, adminURL)
	if err != nil {
		cleanup()
		return nil, err
	}
	if strings.HasPrefix(version, "18.6.1") || num != postgres186Num {
		cleanup()
		return nil, fmt.Errorf("PostgreSQL embutido subiu %s (%d); o teste exige 18.6.0", version, num)
	}
	return &Env{
		AdminURL:         adminURL,
		Embedded:         true,
		ServerVersion:    version,
		ServerVersionNum: num,
		BundleVersion:    BundleVersion,
		stop:             cleanup,
	}, nil
}

func cachePath() (string, error) {
	if dir, err := usableUserCache(); err == nil {
		return dir, nil
	}
	// The default user cache is often on a read-only root (for example
	// /home/<user>/.cache). Keep resolving the directory through
	// os.UserCacheDir by pointing XDG_CACHE_HOME at a writable, executable
	// directory next to the module.
	home, err := moduleCacheHome()
	if err != nil {
		return "", errors.New("não foi possível criar o cache do PostgreSQL embutido")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", errors.New("não foi possível criar o cache do PostgreSQL embutido")
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("não foi possível criar o cache do PostgreSQL embutido")
	}
	if err := os.Setenv("XDG_CACHE_HOME", home); err != nil {
		return "", errors.New("não foi possível criar o cache do PostgreSQL embutido")
	}
	dir, err := usableUserCache()
	if err != nil {
		return "", errors.New("não foi possível criar o cache do PostgreSQL embutido")
	}
	return dir, nil
}

func usableUserCache() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" || !filepath.IsAbs(base) {
		return "", errors.New("cache de usuário indisponível para o PostgreSQL embutido")
	}
	dir := filepath.Join(base, cacheDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if !directoryIsExecutable(dir) {
		return "", errors.New("cache do PostgreSQL embutido sem permissão de execução")
	}
	return dir, nil
}

func moduleCacheHome() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return filepath.Join(dir, ".cache"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("módulo Go não encontrado")
		}
		dir = parent
	}
}

func directoryIsExecutable(dir string) bool {
	probe := filepath.Join(dir, ".exec-probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return false
	}
	defer os.Remove(probe)
	cmd := exec.Command(probe)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func readServerVersion(ctx context.Context, adminURL string) (string, int, error) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", 0, errors.New("falha ao conectar ao PostgreSQL de teste")
	}
	defer conn.Close(ctx)

	var version string
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&version); err != nil {
		return "", 0, errors.New("falha ao ler a versão do PostgreSQL de teste")
	}
	var numRaw string
	if err := conn.QueryRow(ctx, `SHOW server_version_num`).Scan(&numRaw); err != nil {
		return "", 0, errors.New("falha ao ler a versão do PostgreSQL de teste")
	}
	num, err := strconv.Atoi(strings.TrimSpace(numRaw))
	if err != nil {
		return "", 0, errors.New("versão do PostgreSQL de teste ilegível")
	}
	return strings.TrimSpace(version), num, nil
}

func validPostgresURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return false
	}
	return parsed.Host != ""
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func redact(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	text = strings.ReplaceAll(text, "postgres://", "postgresql://")
	var b strings.Builder
	b.Grow(len(text))
	for text != "" {
		start := strings.Index(text, "postgresql://")
		if start < 0 {
			b.WriteString(text)
			break
		}
		b.WriteString(text[:start])
		rest := text[start+len("postgresql://"):]
		at := strings.Index(rest, "@")
		end := strings.IndexAny(rest, " \n\t\r")
		if at < 0 || (end >= 0 && at > end) {
			b.WriteString("postgresql://")
			text = rest
			continue
		}
		b.WriteString("postgresql://[redacted]@")
		text = rest[at+1:]
	}
	out := b.String()
	if len(out) > 2000 {
		out = out[len(out)-2000:]
	}
	return strings.TrimSpace(out)
}

func zonkyCacheName() string {
	arch := runtime.GOARCH
	if runtime.GOOS == "linux" {
		if arch == "arm64" {
			arch += "v8"
		} else if arch == "arm" {
			arch += "32v7"
		}
		if _, err := os.Stat("/etc/alpine-release"); err == nil {
			arch += "-alpine"
		}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		arch = "arm64v8"
	}
	return fmt.Sprintf("embedded-postgres-binaries-%s-%s-%s.txz", runtime.GOOS, arch, BundleVersion)
}

func adminDSN(user, password, database string, port uint32) string {
	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))),
		Path:     "/" + database,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

func freePort() (uint32, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.New("falha ao escolher porta livre para o PostgreSQL embutido")
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, errors.New("falha ao escolher porta livre para o PostgreSQL embutido")
	}
	return uint32(addr.Port), nil
}

func lockCache(cacheDir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(cacheDir, ".zonky.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, errors.New("falha ao preparar o cache do PostgreSQL embutido")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, errors.New("falha ao preparar o cache do PostgreSQL embutido")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
