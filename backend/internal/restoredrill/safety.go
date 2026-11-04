package restoredrill

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maintenanceDB = "postgres"

// realCheckout is the canonical working tree. Disposable embedded clusters may
// live under its .tmp/testdb, but no other data directory or dump may.
const realCheckout = "/home/enok/srv_projetos/workspace/chat-isp"

var dbNameRE = regexp.MustCompile(`^chatisp_restore_drill_[0-9a-f]{32}$`)

// guardDestination checks the URL and the dump directory before any connection.
// The drill never reads CHATISP_DATABASE_URL.
func guardDestination(adminURL, dumpDir string) error {
	if _, ok := os.LookupEnv("CHATISP_DATABASE_URL"); ok {
		return errors.New("ensaio recusado: CHATISP_DATABASE_URL está definida; o ensaio não usa banco da aplicação")
	}
	if _, ok := os.LookupEnv("CHATISP_TEST_DATABASE_URL"); ok {
		return errors.New("ensaio recusado: CHATISP_TEST_DATABASE_URL está definida; o ensaio não segue URL externa")
	}
	socket, err := socketDir(adminURL)
	if err != nil {
		return err
	}
	if err := disposableDataDir(socket); err != nil {
		return err
	}
	return guardDumpDir(dumpDir)
}

// guard refuses every connection that is not the maintenance database of a
// throwaway cluster. The drill never reads CHATISP_DATABASE_URL.
func guard(ctx context.Context, conn *pgx.Conn, adminURL, dumpDir string) error {
	if err := guardDestination(adminURL, dumpDir); err != nil {
		return err
	}

	var current string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&current); err != nil || current != maintenanceDB {
		return errors.New("ensaio recusado: a conexão inicial precisa ser o banco de manutenção")
	}
	var super bool
	if err := conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || !super {
		return errors.New("ensaio recusado: o ensaio exige o superusuário do cluster descartável")
	}
	var dataDir string
	if err := conn.QueryRow(ctx, `SHOW data_directory`).Scan(&dataDir); err != nil {
		return errors.New("ensaio recusado: diretório de dados ilegível")
	}
	if err := disposableDataDir(dataDir); err != nil {
		return err
	}
	socket, err := socketDir(adminURL)
	if err != nil {
		return err
	}
	var listening string
	if err := conn.QueryRow(ctx, `SHOW unix_socket_directories`).Scan(&listening); err != nil {
		return errors.New("ensaio recusado: socket Unix ilegível")
	}
	if !socketListed(listening, socket) {
		return errors.New("ensaio recusado: a conexão não está no socket do cluster descartável")
	}
	var existing string
	err = conn.QueryRow(ctx, `
		SELECT datname FROM pg_database
		WHERE datname = 'chatisp' OR datname LIKE 'chatisp%'
		LIMIT 1
	`).Scan(&existing)
	if err == nil {
		return errors.New("ensaio recusado: o cluster já tem banco da aplicação")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("ensaio recusado: falha ao inspecionar os bancos do cluster")
	}
	return nil
}

func socketDir(adminURL string) (string, error) {
	parsed, err := url.Parse(adminURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", errors.New("ensaio recusado: URL do ensaio inválida")
	}
	if parsed.User != nil {
		if _, has := parsed.User.Password(); has {
			return "", errors.New("ensaio recusado: a URL do ensaio não pode carregar senha")
		}
	}
	if parsed.Hostname() != "" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return "", errors.New("ensaio recusado: o ensaio não aceita host remoto")
	}
	if parsed.Port() != "" {
		return "", errors.New("ensaio recusado: o ensaio não aceita conexão TCP")
	}
	host := parsed.Query().Get("host")
	if host == "" || !filepath.IsAbs(host) {
		return "", errors.New("ensaio recusado: o ensaio só aceita socket Unix de cluster descartável")
	}
	if parsed.Path != "/"+maintenanceDB {
		return "", errors.New("ensaio recusado: a conexão inicial precisa ser o banco de manutenção")
	}
	return filepath.Clean(host), nil
}

func socketListed(shown, want string) bool {
	wantResolved := resolvePath(want)
	for _, item := range strings.Split(shown, ",") {
		if resolvePath(item) == wantResolved {
			return true
		}
	}
	return false
}

func resolvePath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return cleaned
	}
	return resolved
}

// resolveDataDestination returns the canonical data directory. A pg-<n> name is
// never accepted on its own: a symlink contributes its target, including when
// that target is missing, so the lexical shape cannot hide another directory.
// A missing path that is not a symlink stays lexical; the drill's literal
// checks use paths that have not been created.
func resolveDataDestination(path string) (string, error) {
	return followDataDestination(filepath.Clean(path), 0)
}

func followDataDestination(path string, depth int) (string, error) {
	if depth > 16 {
		return "", errors.New("ensaio recusado: o cluster não é descartável")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved), nil
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := os.Readlink(path)
	if err != nil || target == "" {
		return "", errors.New("ensaio recusado: o cluster não é descartável")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return followDataDestination(filepath.Clean(target), depth+1)
}

func disposableDataDir(dir string) error {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "" || cleaned == "." || !filepath.IsAbs(cleaned) {
		return errors.New("ensaio recusado: o cluster não é descartável")
	}
	// The exception is the canonical destination only. The embedded test server
	// stores the cluster in <checkout>/.tmp/testdb/pg-<n>, including a checkout
	// named chat-isp, and may expose it through a short symlink in the temp dir.
	// That symlink is accepted only when it resolves to the ephemeral cluster.
	// A pg-<n> path that points at any other directory stays refused.
	resolved, err := resolveDataDestination(cleaned)
	if err != nil {
		return err
	}
	if isEmbeddedTestDataDir(resolved) {
		return nil
	}
	if pathInRealCheckout(cleaned) || pathInRealCheckout(resolved) {
		return errors.New("ensaio recusado: diretório de dados do checkout real")
	}
	underTmp, err := underProcessTemp(resolved)
	if err != nil {
		return err
	}
	if !underTmp {
		return errors.New("ensaio recusado: o cluster não é descartável")
	}
	return nil
}

func isEmbeddedTestDataDir(path string) bool {
	base := filepath.Base(path)
	rest, ok := strings.CutPrefix(base, "pg-")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	parent := filepath.Dir(path)
	if filepath.Base(parent) != "testdb" {
		return false
	}
	return filepath.Base(filepath.Dir(parent)) == ".tmp"
}

func pathInRealCheckout(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned == realCheckout || strings.HasPrefix(cleaned, realCheckout+string(os.PathSeparator))
}

func underProcessTemp(resolved string) (bool, error) {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false, errors.New("ensaio recusado: diretório temporário indisponível")
	}
	tmp = resolvePath(tmp)
	if resolved == tmp || strings.HasPrefix(resolved, tmp+string(os.PathSeparator)) {
		return true, nil
	}
	return false, nil
}

func guardDumpDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("ensaio recusado: diretório do dump ausente")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errors.New("ensaio recusado: diretório do dump indisponível")
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return errors.New("ensaio recusado: diretório temporário indisponível")
	}
	resolved := resolvePath(abs)
	tmpResolved := resolvePath(tmp)
	if pathInRealCheckout(abs) || pathInRealCheckout(resolved) {
		return errors.New("ensaio recusado: o dump não pode ficar no checkout real")
	}
	if resolved != tmpResolved && !strings.HasPrefix(resolved, tmpResolved+string(os.PathSeparator)) {
		return errors.New("ensaio recusado: o dump só pode ficar em diretório temporário")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return errors.New("ensaio recusado: diretório do dump indisponível")
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return errors.New("ensaio recusado: diretório do dump indisponível")
	}
	if len(entries) != 0 {
		return errors.New("ensaio recusado: o diretório do dump precisa estar vazio")
	}
	return nil
}

func withDatabase(raw, name string) (string, error) {
	if !dbNameRE.MatchString(name) {
		return "", errors.New("ensaio recusado: nome de banco fora do ensaio")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("ensaio recusado: URL do ensaio inválida")
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}

func createDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if !dbNameRE.MatchString(name) {
		return errors.New("ensaio recusado: nome de banco fora do ensaio")
	}
	var stmt string
	if err := conn.QueryRow(ctx, `SELECT format('CREATE DATABASE %I', $1::text)`, name).Scan(&stmt); err != nil {
		return errors.New("falha ao criar o banco descartável")
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return errors.New("falha ao criar o banco descartável")
	}
	return nil
}

func dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if !dbNameRE.MatchString(name) {
		return errors.New("ensaio recusado: nome de banco fora do ensaio")
	}
	var stmt string
	if err := conn.QueryRow(ctx, `SELECT format('DROP DATABASE IF EXISTS %I WITH (FORCE)', $1::text)`, name).Scan(&stmt); err != nil {
		return errors.New("falha ao apagar o banco descartável")
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return errors.New("falha ao apagar o banco descartável")
	}
	return nil
}

func pgMessage(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := pgErr.Message
		if len(msg) > 180 {
			msg = msg[:180]
		}
		return msg
	}
	return "erro de banco"
}
