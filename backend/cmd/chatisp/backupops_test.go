package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeMirrorObj struct {
	body    []byte
	version string
}

type fakeMirrorBucket struct {
	objs map[string]fakeMirrorObj
}

func newFakeMirrorBucket() *fakeMirrorBucket {
	return &fakeMirrorBucket{objs: map[string]fakeMirrorObj{}}
}

func (f *fakeMirrorBucket) put(key string, body []byte) string {
	sum := md5.Sum(body)
	ver := "etag:" + hex.EncodeToString(sum[:])
	f.objs[key] = fakeMirrorObj{body: append([]byte(nil), body...), version: ver}
	return ver
}

func (f *fakeMirrorBucket) Create(_ context.Context, key string, body io.ReadSeeker, size int64) (string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if int64(len(raw)) != size {
		return "", errors.New("size mismatch")
	}
	return f.put(key, raw), nil
}

func (f *fakeMirrorBucket) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	obj, ok := f.objs[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	if version != "" && obj.version != version {
		return nil, errors.New("version mismatch")
	}
	return io.NopCloser(bytes.NewReader(obj.body)), nil
}

func (f *fakeMirrorBucket) Sign(_ context.Context, _, _, _ string, _ time.Duration) (string, error) {
	return "", nil
}

func (f *fakeMirrorBucket) Delete(_ context.Context, key, _ string) error {
	delete(f.objs, key)
	return nil
}

func (f *fakeMirrorBucket) Walk(_ context.Context, visit func(key, version string, size int64) error) error {
	keys := make([]string, 0, len(f.objs))
	for k := range f.objs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		obj := f.objs[k]
		if err := visit(k, obj.version, int64(len(obj.body))); err != nil {
			return err
		}
	}
	return nil
}

func TestMirrorManifestRoundTripAndRefusals(t *testing.T) {
	dir := t.TempDir()
	key := uuid.NewString() + "/" + uuid.NewString()
	sum := sha256.Sum256([]byte("x"))
	in := map[string]mirrorEntry{key: {Key: key, Version: "etag:abc", SHA: hex.EncodeToString(sum[:]), Bytes: 1}}
	if err := writeMirrorManifest(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := readMirrorManifest(dir)
	if err != nil || len(out) != 1 || out[key] != in[key] {
		t.Fatalf("manifesto: %+v %v", out, err)
	}
	for name, body := range map[string]string{
		"cabeçalho desconhecido": "# outro\n",
		"chave fora do padrão":   manifestHeader + "\n../../etc/passwd\tetag:a\t" + strings.Repeat("0", 64) + "\t1\n",
		"hash curto":             manifestHeader + "\n" + key + "\tetag:a\tabc\t1\n",
		"tamanho inválido":       manifestHeader + "\n" + key + "\tetag:a\t" + strings.Repeat("0", 64) + "\t-1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readMirrorManifest(dir); err == nil {
			t.Fatalf("%s: manifesto aceito", name)
		}
	}
	if _, err := readKeySet(filepath.Join(dir, "inexistente")); err == nil {
		t.Fatal("arquivo de exclusões ausente aceito")
	}
	skip := filepath.Join(dir, "skip")
	_ = os.WriteFile(skip, []byte("../fora\n"), 0o600)
	if _, err := readKeySet(skip); err == nil {
		t.Fatal("chave fora do padrão aceita no arquivo de exclusões")
	}
}

func TestMirrorPreservationRefusalsAndConcurrency(t *testing.T) {
	ctx := context.Background()
	bucket := newFakeMirrorBucket()
	ns := uuid.NewString()
	key1 := ns + "/" + uuid.NewString()
	origBytes := []byte("conteudo-original-imutavel")
	bucket.put(key1, origBytes)

	mirror := t.TempDir()
	// Leftover partial files from an interrupted execution must be cleaned up under lock.
	if err := os.MkdirAll(filepath.Join(mirror, objectsDir, ns), 0o700); err != nil {
		t.Fatal(err)
	}
	partialObj := filepath.Join(mirror, objectsDir, ns, ".incoming-interrompido")
	partialManifest := filepath.Join(mirror, ".manifest-interrompido")
	_ = os.WriteFile(partialObj, []byte("lixo"), 0o600)
	_ = os.WriteFile(partialManifest, []byte("lixo"), 0o600)

	var out bytes.Buffer
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err != nil {
		t.Fatalf("backup inicial: %v", err)
	}
	if _, err := os.Stat(partialObj); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("arquivo parcial .incoming-* não foi removido")
	}
	if _, err := os.Stat(partialManifest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("manifesto parcial .manifest-* não foi removido")
	}

	// 1. Alteração inesperada de versão na mesma chave NUNCA pode sobrescrever o backup existente.
	bucket.put(key1, []byte("conteudo-alterado-indevidamente"))
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "sobrescrita recusada") {
		t.Fatalf("alteração inesperada deveria ser recusada: %v", err)
	}
	mirroredPath := filepath.Join(mirror, objectsDir, filepath.FromSlash(key1))
	gotBytes, err := os.ReadFile(mirroredPath)
	if err != nil || !bytes.Equal(gotBytes, origBytes) {
		t.Fatalf("espelho foi sobrescrito ou corrompido: %q vs %q", gotBytes, origBytes)
	}
	// Restaura versão original no bucket para os próximos testes.
	bucket.put(key1, origBytes)

	// 2. Arquivo existente no disco fora do manifesto não pode ser sobrescrito silenciosamente.
	keyUnlisted := ns + "/" + uuid.NewString()
	unlistedPath := filepath.Join(mirror, objectsDir, filepath.FromSlash(keyUnlisted))
	if err := os.WriteFile(unlistedPath, []byte("preexistente"), 0o400); err != nil {
		t.Fatal(err)
	}
	bucket.put(keyUnlisted, []byte("novo-no-bucket"))
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "fora do manifesto") {
		t.Fatalf("arquivo fora do manifesto deveria recusar sobrescrita: %v", err)
	}
	_ = os.Chmod(unlistedPath, 0o600)
	_ = os.Remove(unlistedPath)
	delete(bucket.objs, keyUnlisted)

	// 3. Corrupção de objeto já espelhado deve ser detectada (mesmo com mesmo tamanho!).
	if err := os.Chmod(mirroredPath, 0o600); err != nil {
		t.Fatal(err)
	}
	corruptedSameSize := bytes.Repeat([]byte("X"), len(origBytes))
	if err := os.WriteFile(mirroredPath, corruptedSameSize, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "corrompido") {
		t.Fatalf("corrupção com mesmo tamanho deveria ser detectada no backup: %v", err)
	}
	// Restaura bytes originais no espelho.
	_ = os.Chmod(mirroredPath, 0o600)
	if err := os.WriteFile(mirroredPath, origBytes, 0o400); err != nil {
		t.Fatal(err)
	}

	// 4. Execução concorrente deve ser recusada por lock exclusivo.
	unlock, err := lockMirrorDir(mirror)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "outra operação já está em execução") {
		unlock()
		t.Fatalf("execução concorrente no backup não foi recusada: %v", err)
	}
	if err := attachmentsRestoreWith(ctx, mirror, "", newFakeMirrorBucket(), &out); err == nil || !strings.Contains(err.Error(), "outra operação já está em execução") {
		unlock()
		t.Fatalf("execução concorrente no restore não foi recusada: %v", err)
	}
	unlock()

	// 5. Caminhos com symlinks devem ser recusados no backup e no restore.
	symlinkObj := filepath.Join(mirror, objectsDir, ns, uuid.NewString())
	if err := os.Symlink("/etc/hosts", symlinkObj); err != nil {
		t.Fatal(err)
	}
	if err := attachmentsBackupWith(ctx, mirror, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink dentro de objects/ aceito no backup: %v", err)
	}
	if err := attachmentsRestoreWith(ctx, mirror, "", newFakeMirrorBucket(), &out); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink dentro de objects/ aceito no restore: %v", err)
	}
	_ = os.Remove(symlinkObj)

	symlinkRoot := filepath.Join(t.TempDir(), "mirror-link")
	if err := os.Symlink(mirror, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	if err := attachmentsBackupWith(ctx, symlinkRoot, bucket, bucket, &out); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("raiz symlink aceita no backup: %v", err)
	}
	if err := attachmentsRestoreWith(ctx, symlinkRoot, "", newFakeMirrorBucket(), &out); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("raiz symlink aceita no restore: %v", err)
	}
}

func TestReapplyDeletionsSQLDeduplicationAndIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("falha ao iniciar PostgreSQL de teste: %v", err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	sqlBytes, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "backup", "reapply-deletions.sql"))
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	nsA := uuid.NewString()
	nsB := uuid.NewString()
	attA1 := uuid.NewString()
	attA2 := uuid.NewString()
	attA3 := uuid.NewString()
	attB1 := uuid.NewString()

	seedTenant := func(tenant, ns string, reserved int64) string {
		connector := uuid.NewString()
		inbox := uuid.NewString()
		sector := uuid.NewString()
		contact := uuid.NewString()
		ci := uuid.NewString()
		conv := uuid.NewString()
		sql := `
			INSERT INTO tenants (id, timezone, status) VALUES ('` + tenant + `', 'UTC', 'active');
			INSERT INTO attachment_budgets (tenant_id, namespace, reserved_bytes) VALUES ('` + tenant + `', '` + ns + `', ` + strconv.FormatInt(reserved, 10) + `);
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin) VALUES ('` + connector + `', '` + tenant + `', 'meta_cloud', '` + connector + `');
			INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ('` + inbox + `', '` + tenant + `', '` + connector + `', 'Caixa');
			INSERT INTO sectors (tenant_id, id, name) VALUES ('` + tenant + `', '` + sector + `', 'Setor');
			INSERT INTO sector_inboxes VALUES ('` + tenant + `', '` + sector + `', '` + inbox + `');
			INSERT INTO contacts (id, tenant_id) VALUES ('` + contact + `', '` + tenant + `');
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ('` + ci + `', '` + tenant + `', '` + contact + `', '` + connector + `', '` + ci + `');
			INSERT INTO conversations (id, tenant_id, inbox_id, sector_id, contact_id, contact_identity_id, protocol, protocol_period, cycle_number) VALUES ('` + conv + `', '` + tenant + `', '` + inbox + `', '` + sector + `', '` + contact + `', '` + ci + `', 'P-` + conv[:8] + `', '202611', 1);
		`
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("seed tenant %s: %v", tenant, err)
		}
		return conv
	}

	convA := seedTenant(tenantA, nsA, 3000)
	convB := seedTenant(tenantB, nsB, 5000)
	var connectorA string
	if err := conn.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1`, tenantA).Scan(&connectorA); err != nil {
		t.Fatal(err)
	}

	setupSQL := `
		INSERT INTO attachments (tenant_id, id, conversation_id, request_id, direction, name, expected_bytes, object_key, object_version, state, media_type, sha256, expires_at) VALUES
			('` + tenantA + `', '` + attA1 + `', '` + convA + `', '` + uuid.NewString() + `', 'incoming', 'a1.txt', 1000, '` + nsA + `/` + uuid.NewString() + `', 'etag:1', 'validated', 'text/plain', '` + strings.Repeat("a", 64) + `', now() + interval '1 day'),
			('` + tenantA + `', '` + attA2 + `', '` + convA + `', '` + uuid.NewString() + `', 'incoming', 'a2.txt', 500, '` + nsA + `/` + uuid.NewString() + `', 'etag:2', 'validated', 'text/plain', '` + strings.Repeat("b", 64) + `', now() + interval '1 day'),
			('` + tenantA + `', '` + attA3 + `', '` + convA + `', '` + uuid.NewString() + `', 'incoming', 'a3.txt', 1500, '` + nsA + `/` + uuid.NewString() + `', 'etag:3', 'validated', 'text/plain', '` + strings.Repeat("c", 64) + `', now() + interval '1 day'),
			('` + tenantB + `', '` + attB1 + `', '` + convB + `', '` + uuid.NewString() + `', 'incoming', 'b1.txt', 2000, '` + nsB + `/` + uuid.NewString() + `', 'etag:4', 'validated', 'text/plain', '` + strings.Repeat("d", 64) + `', now() + interval '1 day');
		INSERT INTO deletion_records (id, tenant_id, subject, subject_id) VALUES
			('` + uuid.NewString() + `', '` + tenantA + `', 'attachment', '` + attA1 + `');
	`
	if _, err := conn.Exec(ctx, setupSQL); err != nil {
		t.Fatalf("setup SQL: %v", err)
	}

	// attA1 está TANTO em deletion_records (banco) QUANTO duplicado em ledger_external;
	// attA2 está apenas em ledger_external; e há uma entrada forjada tentando excluir attB1 sob tenantA.
	reapplyScript := `
		DROP TABLE IF EXISTS ledger_external;
		DROP TABLE IF EXISTS guards_external;
		CREATE TEMP TABLE ledger_external (tenant_id uuid, subject text, subject_id uuid);
		CREATE TEMP TABLE guards_external (tenant_id uuid, connector_id uuid, key_version text, identity_mac bytea, cutoff timestamptz);
		INSERT INTO guards_external VALUES
			('` + tenantA + `', '` + connectorA + `', 'v1', decode('` + strings.Repeat("ab", 32) + `','hex'), '2026-11-05 12:00:00+00'),
			('` + tenantA + `', '` + connectorA + `', 'v1', decode('` + strings.Repeat("ab", 32) + `','hex'), '2026-11-06 12:00:00+00');
		INSERT INTO ledger_external VALUES
			('` + tenantA + `', 'attachment', '` + attA1 + `'),
			('` + tenantA + `', 'attachment', '` + attA1 + `'),
			('` + tenantA + `', 'attachment', '` + attA2 + `'),
			('` + tenantA + `', 'attachment', '` + attB1 + `');
	` + string(sqlBytes)

	if _, err := conn.Exec(ctx, reapplyScript); err != nil {
		t.Fatalf("primeira execução de reapply-deletions.sql: %v", err)
	}
	var cutoff time.Time
	var guardCount int
	if err := conn.QueryRow(ctx, `SELECT count(*),max(cutoff) FROM privacy_ingress_guards WHERE tenant_id=$1 AND connector_id=$2`, tenantA, connectorA).Scan(&guardCount, &cutoff); err != nil {
		t.Fatal(err)
	}
	if guardCount != 1 || !cutoff.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("marcador restaurado: quantidade=%d corte=%v", guardCount, cutoff)
	}

	var resA, resB int64
	if err := conn.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id = $1`, tenantA).Scan(&resA); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id = $1`, tenantB).Scan(&resB); err != nil {
		t.Fatal(err)
	}
	// Sem deduplicação, attA1 (1000) seria descontado múltiplas vezes (3000 - 2000 - 500 = 500).
	// Com deduplicação correta, 3000 - (1000 + 500) = 1500.
	if resA != 1500 {
		t.Fatalf("reserved_bytes de tenantA incorreto após deduplicação: obtido %d, esperado 1500", resA)
	}
	// Isolamento entre tenants: a entrada (tenantA, attachment, attB1) jamais pode excluir ou descontar attB1 de tenantB.
	if resB != 5000 {
		t.Fatalf("reserved_bytes de tenantB alterado indevidamente: obtido %d, esperado 5000", resB)
	}

	// Segunda execução idempotente com o mesmo livro externo: nada mais pode ser descontado.
	if _, err := conn.Exec(ctx, reapplyScript); err != nil {
		t.Fatalf("segunda execução idempotente de reapply-deletions.sql: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id = $1`, tenantA).Scan(&resA); err != nil {
		t.Fatal(err)
	}
	if resA != 1500 {
		t.Fatalf("reaplicação não foi idempotente: reserved_bytes de tenantA mudou para %d", resA)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*),max(cutoff) FROM privacy_ingress_guards WHERE tenant_id=$1 AND connector_id=$2`, tenantA, connectorA).Scan(&guardCount, &cutoff); err != nil {
		t.Fatal(err)
	}
	if guardCount != 1 || !cutoff.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("reaplicação alterou corte: quantidade=%d corte=%v", guardCount, cutoff)
	}
	var connectorB string
	if err := conn.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1`, tenantB).Scan(&connectorB); err != nil {
		t.Fatal(err)
	}
	for _, invalidConnector := range []string{uuid.NewString(), connectorB} {
		if _, err := conn.Exec(ctx, `TRUNCATE guards_external`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO guards_external VALUES ($1,$2,'v1',$3,'2026-11-06 12:00:00+00')`, tenantA, invalidConnector, bytes.Repeat([]byte{0xcd}, 32)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err == nil {
			t.Fatalf("restauração aceitou conector ausente ou de outro tenant: %s", invalidConnector)
		}
		if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupDeployFilesAndSchedulingCoherence(t *testing.T) {
	root := repoRoot(t)
	backupDir := filepath.Join(root, "deploy", "backup")

	// 3.1 Isolamento do volume: nenhum script pode montar "-v backup-mirror:/mirror" global.
	for _, name := range []string{"chatisp-backup.sh", "chatisp-restore.sh"} {
		raw, err := os.ReadFile(filepath.Join(backupDir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "-v backup-mirror:") {
			t.Fatalf("%s ainda usa volume global literal -v backup-mirror", name)
		}
	}
	restoreText, err := os.ReadFile(filepath.Join(backupDir, "chatisp-restore.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(restoreText), "\n") {
		if strings.Contains(line, "|| true") && !strings.Contains(line, "rm -sf chatisp-api") && !strings.Contains(line, "grep -v '^CHATISP_DATA_KEY") {
			t.Fatalf("chatisp-restore.sh contém '|| true' proibido: %s", line)
		}
	}

	// 3.2 Agendamento systemd: timers @full, @diff, @incr e @ledger precisam existir e ser aceitos pelo script.
	for _, unit := range []string{
		"chatisp-backup@.service",
		"chatisp-backup-full.timer",
		"chatisp-backup-diff.timer",
		"chatisp-backup-incr.timer",
		"chatisp-backup-ledger.timer",
	} {
		if _, err := os.Stat(filepath.Join(backupDir, "systemd", unit)); err != nil {
			t.Fatalf("unidade systemd ausente: %s (%v)", unit, err)
		}
	}

	// 3.7 Regras de alerta renderizadas cobrem postgres, attachments, keys e ledger com rótulo installation.
	cmd := exec.Command("bash", filepath.Join(backupDir, "render-alerts.sh"))
	cmd.Env = append(os.Environ(), "BACKUP_MAX_AGE_SECONDS=86400", "BACKUP_INSTALLATION=isp-lab")
	rendered, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render-alerts.sh falhou: %v (%s)", err, string(rendered))
	}
	rText := string(rendered)
	for _, want := range []string{
		`installation="isp-lab"`,
		`kind=~"postgres|attachments|keys"`,
		`kind="ledger"`,
		`ChatISPBackupSemSinalPostgres`,
		`ChatISPBackupSemSinalAttachments`,
		`ChatISPBackupSemSinalKeys`,
		`ChatISPBackupSemSinalLedger`,
	} {
		if !strings.Contains(rText, want) {
			t.Fatalf("regras renderizadas sem %q", want)
		}
	}
}

func TestMirrorRefusesUnconfiguredAndVersionedStorage(t *testing.T) {
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if err := attachmentsBackup(ctx, []string{"-dest", t.TempDir()}, nil, &out, &errOut); err == nil || !strings.Contains(err.Error(), "não configurado") {
		t.Fatalf("sem storage: %v", err)
	}
	versioned := []string{
		"CHATISP_ATTACHMENTS_S3_ENDPOINT=https://storage.example", "CHATISP_ATTACHMENTS_S3_REGION=r", "CHATISP_ATTACHMENTS_S3_BUCKET=b",
		"CHATISP_ATTACHMENTS_S3_ACCESS_KEY=a", "CHATISP_ATTACHMENTS_S3_SECRET_KEY=s",
	}
	if err := attachmentsRestore(ctx, []string{"-src", t.TempDir()}, versioned, &out, &errOut); err == nil || !strings.Contains(err.Error(), "sem versionamento") {
		t.Fatalf("bucket versionado: %v", err)
	}
	if err := attachmentsBackup(ctx, nil, nil, &out, &errOut); err == nil {
		t.Fatal("sem -dest")
	}
}

// Real S3-compatible service (Garage, HTTPS) with synthetic bytes. Skipped
// unless CHATISP_MIRROR_TEST_* is set; see scripts/backup-drill.sh.
func TestMirrorBackupAndRestoreAgainstRealService(t *testing.T) {
	endpoint := os.Getenv("CHATISP_MIRROR_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("CHATISP_MIRROR_TEST_ENDPOINT ausente: espelho contra S3 real não executado")
	}
	env := func(bucket string) []string {
		return []string{
			"CHATISP_ATTACHMENTS_S3_ENDPOINT=" + endpoint, "CHATISP_ATTACHMENTS_S3_REGION=" + os.Getenv("CHATISP_MIRROR_TEST_REGION"),
			"CHATISP_ATTACHMENTS_S3_BUCKET=" + bucket, "CHATISP_ATTACHMENTS_S3_ACCESS_KEY=" + os.Getenv("CHATISP_MIRROR_TEST_ACCESS_KEY"),
			"CHATISP_ATTACHMENTS_S3_SECRET_KEY=" + os.Getenv("CHATISP_MIRROR_TEST_SECRET_KEY"), "CHATISP_ATTACHMENTS_S3_PATH_STYLE=true",
			"CHATISP_ATTACHMENTS_S3_UNVERSIONED=true", "CHATISP_ATTACHMENTS_S3_CA_FILE=" + os.Getenv("CHATISP_MIRROR_TEST_CA_FILE"),
		}
	}
	source, target := os.Getenv("CHATISP_MIRROR_TEST_SOURCE_BUCKET"), os.Getenv("CHATISP_MIRROR_TEST_TARGET_BUCKET")
	ctx := context.Background()
	storage, _, err := mirrorStorage(env(source))
	if err != nil {
		t.Fatal(err)
	}
	keep, drop := uuid.NewString()+"/"+uuid.NewString(), uuid.NewString()+"/"+uuid.NewString()
	payloads := map[string][]byte{keep: []byte("bytes mantidos " + uuid.NewString()), drop: []byte("bytes que serão excluídos " + uuid.NewString())}
	versions := map[string]string{}
	for key, body := range payloads {
		v, err := storage.Create(ctx, key, bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		versions[key] = v
		t.Cleanup(func() { _ = storage.Delete(context.Background(), key, v) })
	}
	mirror := t.TempDir()
	var out, errOut bytes.Buffer
	if err := attachmentsBackup(ctx, []string{"-dest", mirror}, env(source), &out, &errOut); err != nil {
		t.Fatalf("espelho: %v %s", err, errOut.String())
	}
	t.Log(strings.TrimSpace(out.String()))
	// Exclusão no bucket vivo depois do backup: o espelho não pode acompanhar.
	if err := storage.Delete(ctx, drop, versions[drop]); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := attachmentsBackup(ctx, []string{"-dest", mirror}, env(source), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mirror, objectsDir, filepath.FromSlash(drop))); err != nil {
		t.Fatalf("o espelho propagou a exclusão: %v", err)
	}
	manifest, _ := readMirrorManifest(mirror)
	if manifest[drop].Version != versions[drop] || manifest[keep].Version != versions[keep] {
		t.Fatalf("identidade de versão divergente: %+v", manifest)
	}

	skip := filepath.Join(mirror, "excluidos.txt")
	if err := os.WriteFile(skip, []byte(drop+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := mirrorStorage(env(target))
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := attachmentsRestore(ctx, []string{"-src", mirror, "-skip", skip}, env(target), &out, &errOut); err != nil {
		t.Fatalf("restauração: %v %s", err, errOut.String())
	}
	t.Log(strings.TrimSpace(out.String()))
	t.Cleanup(func() {
		_ = fresh.Delete(context.Background(), keep, versions[keep])
		_ = fresh.Delete(context.Background(), drop, versions[drop])
	})
	reader, err := fresh.Read(ctx, keep, versions[keep])
	if err != nil {
		t.Fatalf("objeto mantido não voltou com a mesma identidade: %v", err)
	}
	got := new(bytes.Buffer)
	_, _ = got.ReadFrom(reader)
	reader.Close()
	if !bytes.Equal(got.Bytes(), payloads[keep]) {
		t.Fatal("bytes restaurados diferem")
	}
	if _, err := fresh.Read(ctx, drop, versions[drop]); err == nil {
		t.Fatal("objeto excluído pela política voltou ao bucket")
	}
	// Segunda restauração: nada a fazer.
	out.Reset()
	if err := attachmentsRestore(ctx, []string{"-src", mirror, "-skip", skip}, env(target), &out, &errOut); err != nil || !strings.Contains(out.String(), "0 objeto(s) restaurado(s), 1 já presente(s)") {
		t.Fatalf("restauração repetida: %v %s", err, out.String())
	}
	// Adulterar o espelho é detectado antes de gravar.
	if err := os.Chmod(filepath.Join(mirror, objectsDir, filepath.FromSlash(keep)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, objectsDir, filepath.FromSlash(keep)), []byte("adulterado"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := attachmentsRestore(ctx, []string{"-src", mirror, "-skip", skip}, env(target), &out, &errOut); err == nil || !strings.Contains(err.Error(), "não confere com o manifesto") {
		t.Fatalf("espelho adulterado aceito: %v", err)
	}
}

func TestMirrorRejectsAncestorSymlinkBeforeCreatingAnything(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	bucket := newFakeMirrorBucket()
	if err := attachmentsBackupWith(context.Background(), filepath.Join(link, "new", "mirror"), bucket, bucket, io.Discard); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ancestral symlink aceito: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup criou diretório através do symlink antes de recusá-lo")
	}
	if err := attachmentsRestoreWith(context.Background(), filepath.Join(link, "mirror"), "", bucket, io.Discard); err == nil {
		t.Fatal("restore aceitou ancestral symlink")
	}
}

func TestMirrorManifestDetectsCompleteLineTruncationAndDuplicates(t *testing.T) {
	key := uuid.NewString() + "/" + uuid.NewString()
	sum := sha256.Sum256([]byte("x"))
	entry := mirrorEntry{Key: key, Version: "etag:a", SHA: hex.EncodeToString(sum[:]), Bytes: 1}
	dir := t.TempDir()
	if err := writeMirrorManifest(dir, map[string]mirrorEntry{key: entry}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, manifestName)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(full), "\n")
	for name, body := range map[string]string{
		"sem rodapé":            strings.Join(lines[:2], "\n") + "\n",
		"linha inteira perdida": lines[0] + "\n" + lines[2] + "\n",
		"duplicado":             lines[0] + "\n" + lines[1] + "\n" + lines[1] + "\n" + lines[2] + "\n",
		"vazio":                 "",
		"extra após rodapé":     string(full) + lines[1] + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readMirrorManifest(dir); err == nil {
				t.Fatal("manifesto parcial/corrompido aceito")
			}
		})
	}
}

func TestMirrorRestoreValidatesAllObjectsBeforeWriting(t *testing.T) {
	ctx := context.Background()
	bucket := newFakeMirrorBucket()
	ns := uuid.NewString()
	first, last := ns+"/00000000-0000-0000-0000-000000000001", ns+"/ffffffff-ffff-ffff-ffff-ffffffffffff"
	bucket.put(first, []byte("first"))
	bucket.put(last, []byte("last"))
	dir := t.TempDir()
	if err := attachmentsBackupWith(ctx, dir, bucket, bucket, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, objectsDir, filepath.FromSlash(last))
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("FAIL"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := newFakeMirrorBucket()
	if err := attachmentsRestoreWith(ctx, dir, "", target, io.Discard); err == nil {
		t.Fatal("espelho corrompido aceito")
	}
	if len(target.objs) != 0 {
		t.Fatal("restore escreveu objetos antes de validar todo o espelho")
	}
}

func TestMirrorEmptyBackupAndInterruptedOrphan(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	bucket := newFakeMirrorBucket()
	if err := attachmentsBackupWith(ctx, dir, bucket, bucket, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := attachmentsRestoreWith(ctx, dir, "", newFakeMirrorBucket(), io.Discard); err != nil {
		t.Fatalf("backup válido sem anexos deveria restaurar: %v", err)
	}
	// A crash after linking an object but before committing its manifest is
	// explicit incompleteness. Neither backup nor restore may silently bless it.
	key := uuid.NewString() + "/" + uuid.NewString()
	path := filepath.Join(dir, objectsDir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("preserve-me"), 0o400); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func() error{
		"backup":  func() error { return attachmentsBackupWith(ctx, dir, bucket, bucket, io.Discard) },
		"restore": func() error { return attachmentsRestoreWith(ctx, dir, "", newFakeMirrorBucket(), io.Discard) },
	} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "incompleto") {
			t.Fatalf("%s aceitou objeto fora do manifesto: %v", name, err)
		}
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "preserve-me" {
		t.Fatalf("objeto órfão foi apagado/sobrescrito: %q %v", body, err)
	}
}
