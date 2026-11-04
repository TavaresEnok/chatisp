package restoredrill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/testdb"
)

var testEnv *testdb.Env

func TestMain(m *testing.M) {
	_ = os.Unsetenv("CHATISP_DATABASE_URL")
	_ = os.Unsetenv(testdb.EnvDatabaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	env, err := testdb.Start(ctx)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	testEnv = env
	code := m.Run()
	env.Close()
	os.Exit(code)
}

func TestDrillRestoresDumpAndReapplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	dumpDir := t.TempDir()
	report, err := Run(ctx, Options{AdminURL: testEnv.AdminURL, DumpDir: dumpDir})
	if err != nil {
		t.Fatalf("ensaio: %s", err)
	}
	if !report.AttachmentReferenceOK || !report.KeyReferenceOK {
		t.Fatal("referência de anexo ou de chave não conferiu")
	}
	if !report.ContentReadable || !report.KeyRecovered || !report.SecretDecrypted {
		t.Fatal("conteúdo ou chave recuperada não conferiu")
	}
	if report.DurationNote != durationNote || strings.Contains(strings.ToLower(report.DurationNote), "rpo") || strings.Contains(strings.ToLower(report.DurationNote), "rto") {
		t.Fatalf("nota de duração: %s", report.DurationNote)
	}
	assertBackupArtifacts(t, dumpDir)
	if !report.DeletionTablePresent || !report.DeletionReapplied || !report.RemovedAttachmentRestored {
		t.Fatal("deletion_records não foi reaplicada")
	}
	if report.ImmutableBackupErasedImmediately || !report.DumpRetained {
		t.Fatal("o backup imutável foi tratado como apagado")
	}
	if report.LoadAcceptedAsCapacity {
		t.Fatal("carga foi aceita como capacidade")
	}
	if !dbNameRE.MatchString(report.Database) {
		t.Fatalf("banco do relatório: %s", report.Database)
	}
	if report.Duration <= 0 {
		t.Fatal("duração do ensaio não foi medida")
	}
	assertReportText(t, report.Text, true)
	if !dumpStillHoldsRemovedAttachment(t, dumpDir) {
		t.Fatal("o arquivo de dump perdeu o anexo excluído depois do backup")
	}
	assertNoDrillDatabaseLeft(t)
}

func TestReapplySkipsMissingTable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name, err := newDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if err := createDatabase(ctx, admin, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cleaner, err := connect(cleanCtx, testEnv.AdminURL)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleaner.Close(cleanCtx)
		if err := dropDatabase(cleanCtx, cleaner, name); err != nil {
			t.Error(err)
		}
	})
	dsn, err := withDatabase(testEnv.AdminURL, name)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	present, err := reapply(ctx, conn, []deletion{{
		TenantID:  tenantID,
		Subject:   "attachment",
		SubjectID: removedAttachmentID,
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("tabela ausente foi tratada como existente")
	}
	var reg *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.deletion_records')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg != nil {
		t.Fatal("o ensaio criou deletion_records fora da migração")
	}
}

func TestRunRefusesTCPURL(t *testing.T) {
	_, err := Run(context.Background(), Options{
		AdminURL: "postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable",
		DumpDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("URL TCP foi aceita")
	}
	if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "postgresql://") {
		t.Fatalf("erro expôs URL: %s", err)
	}
}

func TestDisposableGuardRejectsRealCheckout(t *testing.T) {
	if err := disposableDataDir("/home/enok/srv_projetos/workspace/chat-isp/.tmp/testdb/pg"); err == nil {
		t.Fatal("checkout real foi aceito")
	}
	if err := disposableDataDir("/var/lib/postgresql/18/main"); err == nil {
		t.Fatal("diretório de serviço foi aceito")
	}
	if err := guardDumpDir("/home/enok/srv_projetos/workspace/chat-isp"); err == nil {
		t.Fatal("dump no checkout real foi aceito")
	}
}

func TestDisposableGuardAllowsEmbeddedClusterAnywhere(t *testing.T) {
	const checkout = "/home/enok/srv_projetos/workspace/chat-isp/.tmp/testdb/pg-482913"
	if err := disposableDataDir(checkout); err != nil {
		t.Fatalf("cluster embutido no checkout chat-isp: %s", err)
	}
	const worktree = "/home/enok/srv_projetos/worktrees/outro-nome/.tmp/testdb/pg-7"
	if err := disposableDataDir(worktree); err != nil {
		t.Fatalf("cluster embutido em worktree: %s", err)
	}
	if err := disposableDataDir("/home/enok/srv_projetos/workspace/chat-isp/.tmp/testdb/pg-abc"); err == nil {
		t.Fatal("diretório estável disfarçado de cluster foi aceito")
	}
	if err := disposableDataDir("/home/enok/srv_projetos/workspace/chat-isp/var/lib/postgresql"); err == nil {
		t.Fatal("dados do checkout foram aceitos")
	}
	if err := guardDumpDir("/home/enok/srv_projetos/workspace/chat-isp/.tmp/testdb/pg-482913"); err == nil {
		t.Fatal("dump no checkout real foi aceito")
	}
}

func TestDisposableGuardRejectsEmbeddedSymlinkEscape(t *testing.T) {
	foreign := embeddedNamedSymlink(t, "/var/lib/postgresql/18/main")
	if err := disposableDataDir(foreign); err == nil {
		t.Fatal("symlink pg-N para diretório não descartável foi aceito")
	}
	forbidden := embeddedNamedSymlink(t, "/home/enok/srv_projetos/workspace/chat-isp/var/lib/postgresql")
	err := disposableDataDir(forbidden)
	if err == nil {
		t.Fatal("symlink pg-N para diretório do checkout foi aceito")
	}
	if !strings.Contains(err.Error(), "checkout real") {
		t.Fatalf("recusa inesperada: %s", err)
	}

	cluster := filepath.Join(t.TempDir(), ".tmp", "testdb", "pg-482913")
	if err := os.MkdirAll(cluster, 0o755); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(t.TempDir(), "cispshort")
	if err := os.Symlink(cluster, short); err != nil {
		t.Fatal(err)
	}
	if err := disposableDataDir(short); err != nil {
		t.Fatalf("symlink curto para o cluster efêmero: %s", err)
	}
	if err := disposableDataDir(cluster); err != nil {
		t.Fatalf("cluster efêmero resolvido: %s", err)
	}
}

func embeddedNamedSymlink(t *testing.T, target string) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), ".tmp", "testdb")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "pg-123")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestReportTextsRefuseCapacity(t *testing.T) {
	assertReportText(t, reportText(true), true)
	assertReportText(t, reportText(false), false)
}

func assertReportText(t *testing.T, text string, tablePresent bool) {
	t.Helper()
	if !strings.Contains(text, "A carga não foi aceita como capacidade.") {
		t.Fatalf("relatório sem a frase de carga: %s", text)
	}
	if !strings.Contains(text, "conteúdo mantido") || !strings.Contains(text, "artefato recuperado") {
		t.Fatalf("relatório não distingue conteúdo e chave: %s", text)
	}
	if !strings.Contains(text, "O backup imutável não é prometido como apagado na hora.") {
		t.Fatalf("relatório sem a frase de backup: %s", text)
	}
	if tablePresent && !strings.Contains(text, "deletion_records foram reaplicadas") {
		t.Fatalf("relatório não confirma reaplicação: %s", text)
	}
	lower := strings.ToLower(text)
	for _, forbidden := range []string{"k6", "p95", "rpo", "rto", "rps"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("relatório contém %s: %s", forbidden, text)
		}
	}
	for _, r := range text {
		if r >= '0' && r <= '9' {
			t.Fatalf("relatório contém número: %s", text)
		}
	}
}

func dumpStillHoldsRemovedAttachment(t *testing.T, dir string) bool {
	t.Helper()
	found, err := dumpContains(dir, []byte(removedObjectKey))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := deletionID.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	leaked, err := dumpContains(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	writable, err := dumpWritable(dir)
	if err != nil {
		t.Fatal(err)
	}
	return found && !leaked && !writable
}

func assertBackupArtifacts(t *testing.T, dir string) {
	t.Helper()
	if err := backupObjectIntact(dir, keptObjectKey, keptObjectVersion); err != nil {
		t.Fatal(err)
	}
	if err := backupObjectIntact(dir, removedObjectKey, removedObjectVersion); err != nil {
		t.Fatal(err)
	}
	recovery := filepath.Join(dir, "recovery")
	info, err := os.Lstat(recovery)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("diretório da chave: %v", err)
	}
	materialPath := filepath.Join(recovery, "material")
	materialInfo, err := os.Lstat(materialPath)
	if err != nil || materialInfo.Mode().Perm() != 0o600 || materialInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("material da chave: modo %v err %v", materialInfo, err)
	}
	material, err := os.ReadFile(materialPath)
	if err != nil || len(material) == 0 {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.txt", "objects.manifest", "secrets.manifest"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, material) {
			t.Fatalf("material vazou em %s", name)
		}
	}
}

func assertNoDrillDatabaseLeft(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var name string
	err = conn.QueryRow(ctx, `
		SELECT datname FROM pg_database WHERE datname LIKE 'chatisp_restore_drill_%' LIMIT 1
	`).Scan(&name)
	if err == nil {
		t.Fatalf("banco descartável permaneceu: %s", name)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
}
