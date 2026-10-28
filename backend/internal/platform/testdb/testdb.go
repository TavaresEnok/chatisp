// Package testdb starts PostgreSQL for tests.
// The application process must not import this package.
//
// Binaries come from the npm package installed in backend/tools/pg.
// The verifier runs npm ci --ignore-scripts, so the postinstall that would
// recreate library symlinks does not run. This package recreates them.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// BundleVersion is the PostgreSQL release shipped by the npm package.
	// The server reports it as 18.4; server_version_num is 180004.
	BundleVersion = "18.4.0"

	// NPMPackage is the exact devDependency that vendors the linux-x64 binaries.
	NPMPackage = "@embedded-postgres/linux-x64"
	// NPMPackageVersion is the exact version. It must stay free of range syntax.
	NPMPackageVersion = "18.4.0-beta.17"

	// EmbeddedServerVersionNum is server_version_num for PostgreSQL 18.4.
	EmbeddedServerVersionNum = 180004

	// EnvDatabaseURL selects an external server and disables the embedded one.
	EnvDatabaseURL = "CHATISP_TEST_DATABASE_URL"

	postgres18Major   = 18
	maxUnixSocketPath = 107
	socketFileName    = ".s.PGSQL.5432"
	npmCiHint         = "npm ci --prefix backend/tools/pg --no-audit --no-fund"
	sharedBuffers     = "16MB"
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
// Otherwise PostgreSQL 18.4 from the npm package is started on a Unix socket.
// Failures are errors, never a silent skip.
func Start(ctx context.Context) (*Env, error) {
	if raw, ok := os.LookupEnv(EnvDatabaseURL); ok && strings.TrimSpace(raw) != "" {
		return startExternal(ctx, strings.TrimSpace(raw))
	}
	return startEmbedded(ctx)
}

// Close stops an embedded server and removes its data directory.
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
	pkg, err := packageDir()
	if err != nil {
		return nil, err
	}
	if err := hydrateSymlinks(pkg); err != nil {
		return nil, err
	}
	initdbBin, postgresBin, err := executableBins(pkg)
	if err != nil {
		return nil, err
	}
	parent, err := dataParent()
	if err != nil {
		return nil, err
	}
	real, err := os.MkdirTemp(parent, "pg-")
	if err != nil {
		return nil, errors.New("falha ao criar diretório temporário do PostgreSQL")
	}
	logPath := real + ".log"
	runDir, removeLink, err := chooseRunDir(real)
	if err != nil {
		_ = os.RemoveAll(real)
		return nil, err
	}
	cleanupFiles := func() {
		if removeLink != nil {
			removeLink()
		}
		_ = os.RemoveAll(real)
		_ = os.Remove(logPath)
	}
	if err := runInitDB(ctx, initdbBin, runDir); err != nil {
		cleanupFiles()
		return nil, err
	}
	proc, err := startPostgres(postgresBin, runDir, logPath)
	if err != nil {
		cleanupFiles()
		return nil, err
	}
	stop := func() {
		proc.stop()
		cleanupFiles()
	}
	dsn := adminDSN(runDir)
	if err := waitReady(ctx, proc, dsn); err != nil {
		detail := clip(readTail(logPath))
		stop()
		if detail != "" {
			return nil, fmt.Errorf("falha ao iniciar PostgreSQL 18.4 embutido: %s", detail)
		}
		return nil, err
	}
	version, num, err := readServerVersion(ctx, dsn)
	if err != nil || num != EmbeddedServerVersionNum {
		stop()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("PostgreSQL embutido subiu %s (%d); o teste exige %s", version, num, BundleVersion)
	}
	return &Env{
		AdminURL:         dsn,
		Embedded:         true,
		ServerVersion:    version,
		ServerVersionNum: num,
		BundleVersion:    BundleVersion,
		stop:             stop,
	}, nil
}

func runInitDB(ctx context.Context, bin, dataDir string) error {
	cmd := exec.CommandContext(ctx, bin, "-D", dataDir, "-U", "postgres", "-A", "trust", "-E", "UTF8", "--no-sync")
	cmd.Env = processEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("falha ao inicializar PostgreSQL 18.4: tempo esgotado")
		}
		detail := clip(string(out))
		if detail == "" {
			detail = "sem saída do initdb"
		}
		return fmt.Errorf("falha ao inicializar PostgreSQL 18.4: %s", detail)
	}
	return nil
}

type pgProc struct {
	cmd    *exec.Cmd
	log    *os.File
	exited chan struct{}
}

func startPostgres(bin, dataDir, logPath string) (*pgProc, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, errors.New("falha ao preparar o log do PostgreSQL de teste")
	}
	cmd := exec.Command(bin,
		"-D", dataDir,
		"-c", "listen_addresses=",
		"-c", "unix_socket_directories="+dataDir,
		"-c", "shared_buffers="+sharedBuffers,
		"-c", "dynamic_shared_memory_type=mmap",
		"-c", "fsync=off",
	)
	cmd.Env = processEnv()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, errors.New("falha ao iniciar PostgreSQL 18.4 embutido")
	}
	proc := &pgProc{cmd: cmd, log: logFile, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		close(proc.exited)
	}()
	return proc, nil
}

func (p *pgProc) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}

func waitReady(ctx context.Context, proc *pgProc, dsn string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		dialCtx, cancel := context.WithTimeout(ctx, time.Second)
		conn, err := pgx.Connect(dialCtx, dsn)
		cancel()
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("falha ao iniciar PostgreSQL 18.4 embutido: tempo esgotado")
		case <-proc.exited:
			return errors.New("falha ao iniciar PostgreSQL 18.4 embutido: o processo encerrou")
		case <-ticker.C:
		}
	}
}

func adminDSN(dataDir string) string {
	u := url.URL{
		Scheme: "postgresql",
		User:   url.User("postgres"),
		Host:   "127.0.0.1",
		Path:   "/postgres",
	}
	q := url.Values{}
	q.Set("host", dataDir)
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

// chooseRunDir keeps the socket inside the data directory.
// A path longer than the Unix socket limit is exposed through a short symlink
// in the temp directory. The data files stay in the workspace: /tmp is a small tmpfs.
func chooseRunDir(real string) (string, func(), error) {
	if len(filepath.Join(real, socketFileName)) <= maxUnixSocketPath {
		return real, func() {}, nil
	}
	name, err := randomHex(8)
	if err != nil {
		return "", nil, errors.New("falha ao preparar o diretório do PostgreSQL de teste")
	}
	link := filepath.Join(os.TempDir(), "cisp"+name)
	if err := os.Symlink(real, link); err != nil {
		return "", nil, errors.New("falha ao preparar o diretório do PostgreSQL de teste")
	}
	if len(filepath.Join(link, socketFileName)) > maxUnixSocketPath {
		_ = os.Remove(link)
		return "", nil, errors.New("caminho do socket Unix do PostgreSQL de teste excede o limite")
	}
	return link, func() { _ = os.Remove(link) }, nil
}

func dataParent() (string, error) {
	module, err := moduleRoot()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(filepath.Dir(module), ".tmp"),
		filepath.Join(module, ".tmp"),
	}
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		probe := filepath.Join(dir, ".write-probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			continue
		}
		_ = os.Remove(probe)
		return dir, nil
	}
	return "", errors.New("falha ao criar diretório temporário do PostgreSQL")
}

func packageDir() (string, error) {
	module, err := moduleRoot()
	if err != nil {
		return "", missingBinaries()
	}
	dir := filepath.Join(module, "tools", "pg", "node_modules", "@embedded-postgres", "linux-x64")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", missingBinaries()
	}
	return dir, nil
}

func executableBins(pkg string) (string, string, error) {
	initdb := filepath.Join(pkg, "native", "bin", "initdb")
	postgres := filepath.Join(pkg, "native", "bin", "postgres")
	for _, bin := range []string{initdb, postgres} {
		info, err := os.Stat(bin)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return "", "", missingBinaries()
		}
	}
	return initdb, postgres, nil
}

func missingBinaries() error {
	return errors.New("binários do PostgreSQL de teste ausentes. Rode: " + npmCiHint)
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", errors.New("módulo Go não encontrado")
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("módulo Go não encontrado")
		}
		dir = parent
	}
}

type linkSpec struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func hydrateSymlinks(pkgRoot string) error {
	manifest := filepath.Join(pkgRoot, "native", "pg-symlinks.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return missingBinaries()
	}
	var specs []linkSpec
	if err := json.Unmarshal(raw, &specs); err != nil || len(specs) == 0 {
		return errors.New("manifesto de symlinks do PostgreSQL de teste inválido")
	}
	unlock, err := lockFile(filepath.Join(pkgRoot, "native", ".hydrate.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	for _, spec := range specs {
		if err := ensureLink(pkgRoot, spec); err != nil {
			return err
		}
	}
	return nil
}

func ensureLink(pkgRoot string, spec linkSpec) error {
	source, err := inside(pkgRoot, spec.Source)
	if err != nil {
		return err
	}
	target, err := inside(pkgRoot, spec.Target)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		return missingBinaries()
	}
	rel, err := filepath.Rel(filepath.Dir(target), source)
	if err != nil || rel == "" || rel == "." || filepath.IsAbs(rel) || escapes(rel) {
		return errors.New("symlink do PostgreSQL de teste recusado")
	}
	if existing, readErr := os.Readlink(target); readErr == nil && existing == rel {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".symlink-*")
	if err != nil {
		return errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpName)
	if err := os.Symlink(rel, tmpName); err != nil {
		return errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		if existing, readErr := os.Readlink(target); readErr == nil && existing == rel {
			return nil
		}
		return errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	return nil
}

func inside(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || escapes(rel) {
		return "", errors.New("symlink do PostgreSQL de teste recusado")
	}
	clean := filepath.Clean(rel)
	if escapes(clean) {
		return "", errors.New("symlink do PostgreSQL de teste recusado")
	}
	full := filepath.Join(root, clean)
	back, err := filepath.Rel(root, full)
	if err != nil || escapes(back) {
		return "", errors.New("symlink do PostgreSQL de teste recusado")
	}
	return full, nil
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func lockFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, errors.New("falha ao preparar os binários do PostgreSQL de teste")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func processEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+3)
	for _, item := range base {
		if strings.HasPrefix(item, "LC_ALL=") || strings.HasPrefix(item, "LANG=") || strings.HasPrefix(item, "LC_") {
			continue
		}
		out = append(out, item)
	}
	return append(out, "LC_ALL=C", "LANG=C", "TZ=UTC")
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

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func readTail(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func clip(text string) string {
	text = redact(text, "")
	if len(text) > 2000 {
		text = text[len(text)-2000:]
	}
	return strings.TrimSpace(text)
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
