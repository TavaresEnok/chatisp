// Package restoredrill runs the M8 restore drill on a disposable PostgreSQL
// cluster. It never opens the application database and never follows
// CHATISP_DATABASE_URL.
package restoredrill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
)

const (
	keptObjectKey        = "drill/anexo-mantido"
	removedObjectKey     = "drill/anexo-removido"
	keptObjectVersion    = "versao-mantida"
	removedObjectVersion = "versao-removida"
	keyVersion           = "ensaio-v1"
	durationNote         = "duração medida deste exercício descartável; não é meta de piloto"
)

var (
	tenantID                = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	conversationID          = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	keptAttachmentID        = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	removedAttachmentID     = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	keptRequestID           = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	removedRequestID        = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	namespaceID             = uuid.MustParse("77777777-7777-4777-8777-777777777777")
	secretID                = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	connectorID             = uuid.MustParse("99999999-9999-4999-8999-999999999999")
	deletionID              = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	removedSecretID         = uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	removedSecretDeletionID = uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	removedConnectorID      = uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	fixtureExpiry           = time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
)

// Options selects the disposable cluster and the directory that keeps the dump.
type Options struct {
	AdminURL string
	DumpDir  string
}

// Report is the drill result. It carries no capacity figure.
// Database is the disposable database that received the restore.
// Duration is the measured time of this disposable exercise.
// It is not a pilot recovery objective.
// ContentReadable, KeyRecovered and SecretDecrypted record bytes and a new
// ring loaded from the recovered artifact. They are distinct from the
// reference flags.
type Report struct {
	Database                         string
	Duration                         time.Duration
	DurationNote                     string
	AttachmentReferenceOK            bool
	KeyReferenceOK                   bool
	ContentReadable                  bool
	KeyRecovered                     bool
	SecretDecrypted                  bool
	DeletionTablePresent             bool
	DeletionReapplied                bool
	RemovedAttachmentRestored        bool
	ImmutableBackupErasedImmediately bool
	LoadAcceptedAsCapacity           bool
	DumpRetained                     bool
	Text                             string
}

type outcome struct {
	attachmentRef   bool
	keyRef          bool
	content         bool
	keyRecovered    bool
	secret          bool
	removedRestored bool
	deletions       bool
	tablePresent    bool
	dumpRetained    bool
}

type deletion struct {
	TenantID  uuid.UUID
	Subject   string
	SubjectID uuid.UUID
}

// Run restores a test dump on databases it creates, checks restored attachment
// bytes and a secret opened by a new ring, and reapplies deletion_records when
// that table exists. The dump stays on disk. Load is not accepted as capacity.
// A failure returns no successful report, including when only part of the
// content or the key recovery succeeded.
func Run(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if err := guardDestination(opts.AdminURL, opts.DumpDir); err != nil {
		return Report{}, err
	}
	dump, err := pinTempDir(opts.DumpDir)
	if err != nil {
		return Report{}, err
	}
	admin, err := connect(ctx, opts.AdminURL)
	if err != nil {
		return Report{}, err
	}
	defer admin.Close(context.Background())

	if err := guard(ctx, admin, opts.AdminURL, opts.DumpDir); err != nil {
		return Report{}, err
	}

	src, err := newScratch()
	if err != nil {
		return Report{}, err
	}
	defer src.release()
	var restoredObjects string
	var restoredKeys string
	defer func() {
		if restoredObjects != "" {
			_ = disableTree(restoredObjects)
		}
		if restoredKeys != "" {
			_ = disableTree(restoredKeys)
		}
	}()

	var created []string
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, name := range created {
			_ = dropDatabase(cleanCtx, admin, name)
		}
	}()

	source, err := newDatabaseName()
	if err != nil {
		return Report{}, err
	}
	if err := createDatabase(ctx, admin, source); err != nil {
		return Report{}, err
	}
	created = append(created, source)
	if err := prepareDatabase(ctx, opts.AdminURL, source); err != nil {
		return Report{}, err
	}

	sourceURL, err := withDatabase(opts.AdminURL, source)
	if err != nil {
		return Report{}, err
	}
	sourceConn, err := connect(ctx, sourceURL)
	if err != nil {
		return Report{}, err
	}
	defer sourceConn.Close(context.Background())

	if err := seed(ctx, sourceConn, src); err != nil {
		return Report{}, err
	}
	if err := writeDump(ctx, sourceConn, dump); err != nil {
		return Report{}, err
	}
	if err := snapshotBackup(src, dump); err != nil {
		return Report{}, err
	}
	sourceObjects := src.objects
	sourceKeys := src.keys
	if err := disableTree(sourceObjects); err != nil {
		return Report{}, err
	}
	if err := disableTree(sourceKeys); err != nil {
		return Report{}, err
	}
	src.objects = ""
	src.keys = ""
	if err := absent(sourceObjects); err != nil {
		return Report{}, err
	}
	if err := absent(sourceKeys); err != nil {
		return Report{}, err
	}

	if err := recordDeletion(ctx, sourceConn); err != nil {
		return Report{}, err
	}
	ledger, err := readLedger(ctx, sourceConn)
	if err != nil {
		return Report{}, err
	}
	if err := ledgerComplete(ledger); err != nil {
		return Report{}, err
	}

	dest, err := newDatabaseName()
	if err != nil {
		return Report{}, err
	}
	if err := createDatabase(ctx, admin, dest); err != nil {
		return Report{}, err
	}
	created = append(created, dest)
	if err := prepareDatabase(ctx, opts.AdminURL, dest); err != nil {
		return Report{}, err
	}
	destURL, err := withDatabase(opts.AdminURL, dest)
	if err != nil {
		return Report{}, err
	}
	destConn, err := connect(ctx, destURL)
	if err != nil {
		return Report{}, err
	}
	defer destConn.Close(context.Background())

	if err := restoreDump(ctx, destConn, dump); err != nil {
		return Report{}, err
	}
	restoredObjects, err = privateTempDir("restoredrill-restored-objects-")
	if err != nil {
		return Report{}, err
	}
	restoredKeys, err = privateTempDir("restoredrill-restored-keys-")
	if err != nil {
		return Report{}, err
	}
	if err := restoreTrees(dump, restoredObjects, restoredKeys); err != nil {
		return Report{}, err
	}
	metas, err := readObjectManifest(dump)
	if err != nil {
		return Report{}, err
	}
	secrets, err := readSecretManifest(dump)
	if err != nil {
		return Report{}, err
	}
	ring, version, err := loadRecoveredRing(restoredKeys)
	if err != nil {
		return Report{}, err
	}
	if err := expectRestored(ctx, destConn, restoredObjects, ring, version, metas, secrets); err != nil {
		return Report{}, err
	}
	ring = nil

	present, err := reapply(ctx, destConn, ledger, restoredObjects)
	if err != nil {
		return Report{}, err
	}
	if present {
		if err := expectReapplied(ctx, destConn, dump, restoredObjects, restoredKeys, secrets); err != nil {
			return Report{}, err
		}
	}
	if err := expectDumpRetained(dump); err != nil {
		return Report{}, err
	}
	report, err := succeed(dest, started, outcome{
		attachmentRef:   true,
		keyRef:          true,
		content:         true,
		keyRecovered:    true,
		secret:          true,
		removedRestored: true,
		deletions:       present,
		tablePresent:    present,
		dumpRetained:    true,
	})
	if err != nil {
		return Report{}, err
	}
	if err := writeRestoreMetric(dump, report.Duration); err != nil {
		return Report{}, err
	}
	return report, nil
}

func succeed(dest string, started time.Time, got outcome) (Report, error) {
	complete := got.attachmentRef && got.keyRef && got.content && got.keyRecovered && got.secret && got.removedRestored && got.dumpRetained
	if !complete || got.tablePresent != got.deletions {
		return Report{}, errors.New("ensaio incompleto")
	}
	return Report{
		Database:                         dest,
		Duration:                         time.Since(started),
		DurationNote:                     durationNote,
		AttachmentReferenceOK:            true,
		KeyReferenceOK:                   true,
		ContentReadable:                  true,
		KeyRecovered:                     true,
		SecretDecrypted:                  true,
		DeletionTablePresent:             got.tablePresent,
		DeletionReapplied:                got.deletions,
		RemovedAttachmentRestored:        true,
		ImmutableBackupErasedImmediately: false,
		LoadAcceptedAsCapacity:           false,
		DumpRetained:                     true,
		Text:                             reportText(got.tablePresent),
	}, nil
}

func reportText(tablePresent bool) string {
	const head = "O ensaio restaurou um dump de PostgreSQL de teste num banco descartável e os bytes dos anexos num diretório novo. A referência do anexo conferiu e o conteúdo mantido foi lido pela referência restaurada, com o hash dos bytes. A referência da chave conferiu e uma chave nova, lida só do artefato recuperado, decifrou o segredo mantido com a versão e o AAD corretos. "
	const tail = "O backup imutável não é prometido como apagado na hora. A carga não foi aceita como capacidade."
	if tablePresent {
		return head + "As exclusões registradas em deletion_records foram reaplicadas, inclusive o objeto elegível e o segredo removido. " + tail
	}
	return head + "A tabela deletion_records não existe, então nenhuma exclusão foi reaplicada. " + tail
}

func connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, errors.New("falha ao conectar ao PostgreSQL descartável do ensaio")
	}
	return conn, nil
}

func newDatabaseName() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("falha ao nomear o banco descartável")
	}
	name := "chatisp_restore_drill_" + hex.EncodeToString(buf)
	if !dbNameRE.MatchString(name) {
		return "", errors.New("ensaio recusado: nome de banco fora do ensaio")
	}
	return name, nil
}

func prepareDatabase(ctx context.Context, adminURL, name string) error {
	dsn, err := withDatabase(adminURL, name)
	if err != nil {
		return err
	}
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		return errors.New("falha ao preparar o banco descartável")
	}
	urls, err := db.Bootstrap(ctx, dsn, secrets)
	if err != nil {
		return err
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		return errors.New("falha ao migrar o banco descartável")
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		return errors.New("falha ao migrar o banco descartável")
	}
	return nil
}

func seed(ctx context.Context, conn *pgx.Conn, src *scratch) error {
	if src == nil {
		return errors.New("falha ao gravar a referência de ensaio")
	}
	if err := writeSyntheticObjects(src.objects); err != nil {
		return err
	}
	if err := createKeyMaterial(src.keys); err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("falha ao gravar a referência de ensaio")
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO tenants (id, timezone, status) VALUES ($1, 'America/Sao_Paulo', 'active')
	`, tenantID); err != nil {
		return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO conversations (id, tenant_id, summary) VALUES ($1, $2, 'ensaio')
	`, conversationID, tenantID); err != nil {
		return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO attachment_budgets (tenant_id, namespace) VALUES ($1, $2)
	`, tenantID, namespaceID); err != nil {
		return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
	}
	if err := insertAttachments(ctx, tx, src.objects); err != nil {
		return err
	}
	if err := encryptFixtureSecrets(ctx, tx, src.keys); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("falha ao gravar a referência de ensaio")
	}
	return nil
}

func recordDeletion(ctx context.Context, conn *pgx.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("falha ao registrar a exclusão posterior ao backup")
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		DELETE FROM attachments WHERE tenant_id = $1 AND id = $2
	`, tenantID, removedAttachmentID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("falha ao registrar a exclusão posterior ao backup")
	}
	secretTag, err := tx.Exec(ctx, `
		DELETE FROM credential_secrets WHERE tenant_id = $1 AND id = $2
	`, tenantID, removedSecretID)
	if err != nil || secretTag.RowsAffected() != 1 {
		return errors.New("falha ao registrar a exclusão posterior ao backup")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deletion_records (id, tenant_id, subject, subject_id)
		VALUES ($1, $2, 'attachment', $3), ($4, $2, 'credential_secret', $5)
	`, deletionID, tenantID, removedAttachmentID, removedSecretDeletionID, removedSecretID); err != nil {
		return fmt.Errorf("falha ao registrar a exclusão posterior ao backup: %s", pgMessage(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("falha ao registrar a exclusão posterior ao backup")
	}
	return nil
}

func readLedger(ctx context.Context, conn *pgx.Conn) ([]deletion, error) {
	rows, err := conn.Query(ctx, `
		SELECT tenant_id, subject, subject_id
		FROM deletion_records
		ORDER BY subject, subject_id
	`)
	if err != nil {
		return nil, errors.New("falha ao ler deletion_records")
	}
	defer rows.Close()
	var ledger []deletion
	for rows.Next() {
		var row deletion
		if err := rows.Scan(&row.TenantID, &row.Subject, &row.SubjectID); err != nil {
			return nil, errors.New("falha ao ler deletion_records")
		}
		ledger = append(ledger, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("falha ao ler deletion_records")
	}
	return ledger, nil
}

func ledgerComplete(ledger []deletion) error {
	var sawAttachment bool
	var sawSecret bool
	for _, rec := range ledger {
		if rec.TenantID == tenantID && rec.Subject == "attachment" && rec.SubjectID == removedAttachmentID {
			sawAttachment = true
		}
		if rec.TenantID == tenantID && rec.Subject == "credential_secret" && rec.SubjectID == removedSecretID {
			sawSecret = true
		}
	}
	if !sawAttachment || !sawSecret {
		return errors.New("falha ao registrar a exclusão posterior ao backup")
	}
	return nil
}

func reapply(ctx context.Context, conn *pgx.Conn, ledger []deletion, objectsRoot string) (bool, error) {
	var present bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.deletion_records') IS NOT NULL`).Scan(&present); err != nil {
		return false, errors.New("falha ao consultar deletion_records")
	}
	if !present {
		return false, nil
	}
	if len(ledger) == 0 {
		return true, nil
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return true, errors.New("falha ao reaplicar exclusões")
	}
	defer tx.Rollback(ctx)
	var doomed []string
	for _, rec := range ledger {
		if rec.Subject != "attachment" && rec.Subject != "credential_secret" {
			return true, errors.New("registro de exclusão com alvo desconhecido")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_records (id, tenant_id, subject, subject_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING
		`, uuid.New(), rec.TenantID, rec.Subject, rec.SubjectID); err != nil {
			return true, fmt.Errorf("falha ao reaplicar exclusões: %s", pgMessage(err))
		}
		var tag pgconn.CommandTag
		switch rec.Subject {
		case "attachment":
			if strings.TrimSpace(objectsRoot) == "" {
				return true, errors.New("objeto elegível ausente")
			}
			var objectKey string
			err = tx.QueryRow(ctx, `
				SELECT object_key FROM attachments WHERE tenant_id = $1 AND id = $2
			`, rec.TenantID, rec.SubjectID).Scan(&objectKey)
			if err != nil || strings.TrimSpace(objectKey) == "" {
				return true, errors.New("exclusão não voltou com o restore")
			}
			tag, err = tx.Exec(ctx, `DELETE FROM attachments WHERE tenant_id = $1 AND id = $2`, rec.TenantID, rec.SubjectID)
			doomed = append(doomed, objectKey)
		case "credential_secret":
			tag, err = tx.Exec(ctx, `DELETE FROM credential_secrets WHERE tenant_id = $1 AND id = $2`, rec.TenantID, rec.SubjectID)
		}
		if err != nil {
			return true, fmt.Errorf("falha ao reaplicar exclusões: %s", pgMessage(err))
		}
		if tag.RowsAffected() != 1 {
			return true, errors.New("exclusão não voltou com o restore")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return true, errors.New("falha ao reaplicar exclusões")
	}
	for _, objectKey := range doomed {
		if err := removeEligibleObject(objectsRoot, objectKey); err != nil {
			return true, err
		}
	}
	return true, nil
}

func expectRestored(ctx context.Context, conn *pgx.Conn, objects string, ring *crypto.Ring, version string, metas map[string]objectMeta, secrets map[uuid.UUID]secretMeta) error {
	var conversationExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM conversations WHERE tenant_id = $1 AND id = $2)
	`, tenantID, conversationID).Scan(&conversationExists); err != nil || !conversationExists {
		return errors.New("referência de anexo divergente depois do restore")
	}
	if err := verifyAttachmentBytes(ctx, conn, objects, metas, keptAttachmentID); err != nil {
		return err
	}
	if err := verifyAttachmentBytes(ctx, conn, objects, metas, removedAttachmentID); err != nil {
		return errors.New("o restore não devolveu o anexo excluído depois do backup")
	}
	for _, id := range []uuid.UUID{secretID, removedSecretID} {
		row, err := loadSecretRow(ctx, conn, id)
		if err != nil {
			return err
		}
		meta, ok := secrets[id]
		if !ok {
			return errors.New("referência de chave divergente depois do restore")
		}
		if err := verifySecret(ring, version, row, meta); err != nil {
			return err
		}
	}
	return nil
}

func expectReapplied(ctx context.Context, conn *pgx.Conn, dump, objects, keyDir string, secrets map[uuid.UUID]secretMeta) error {
	if err := verifyAttachmentBytes(ctx, conn, objects, mustMetas(dump), keptAttachmentID); err != nil {
		return err
	}
	var attachmentLeft bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM attachments WHERE tenant_id = $1 AND id = $2
		)
	`, tenantID, removedAttachmentID).Scan(&attachmentLeft); err != nil {
		return errors.New("falha ao conferir a exclusão reaplicada")
	}
	if attachmentLeft {
		return errors.New("anexo excluído permaneceu após reaplicar deletion_records")
	}
	var secretLeft bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM credential_secrets WHERE tenant_id = $1 AND id = $2
		)
	`, tenantID, removedSecretID).Scan(&secretLeft); err != nil {
		return errors.New("falha ao conferir a exclusão reaplicada")
	}
	if secretLeft {
		return errors.New("segredo excluído permaneceu após reaplicar deletion_records")
	}
	if err := objectAbsent(objects, removedObjectKey); err != nil {
		return err
	}
	for _, subject := range []struct {
		name string
		id   uuid.UUID
	}{
		{"attachment", removedAttachmentID},
		{"credential_secret", removedSecretID},
	} {
		var recorded bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM deletion_records
				WHERE tenant_id = $1 AND subject = $2 AND subject_id = $3
			)
		`, tenantID, subject.name, subject.id).Scan(&recorded); err != nil || !recorded {
			return errors.New("deletion_records não guardou a exclusão reaplicada")
		}
	}
	ring, version, err := loadRecoveredRing(keyDir)
	if err != nil {
		return err
	}
	row, err := loadSecretRow(ctx, conn, secretID)
	if err != nil {
		return err
	}
	meta, ok := secrets[secretID]
	if !ok {
		return errors.New("referência de chave divergente depois do restore")
	}
	if err := verifySecret(ring, version, row, meta); err != nil {
		return err
	}
	if err := backupObjectIntact(dump, removedObjectKey, removedObjectVersion); err != nil {
		return err
	}
	if err := backupObjectIntact(dump, keptObjectKey, keptObjectVersion); err != nil {
		return err
	}
	return nil
}

func mustMetas(dump string) map[string]objectMeta {
	metas, err := readObjectManifest(dump)
	if err != nil {
		return nil
	}
	return metas
}

func expectDumpRetained(dir string) error {
	foundKey, err := dumpContains(dir, []byte(removedObjectKey))
	if err != nil || !foundKey {
		return errors.New("o dump não conservou a referência do anexo excluído depois do backup")
	}
	foundVersion, err := dumpContains(dir, []byte(keyVersion))
	if err != nil || !foundVersion {
		return errors.New("o dump não conservou a referência da chave")
	}
	raw, err := deletionID.MarshalBinary()
	if err != nil {
		return errors.New("falha ao ler o dump")
	}
	foundDeletion, err := dumpContains(dir, raw)
	if err != nil || foundDeletion {
		return errors.New("o dump foi alterado depois da exclusão")
	}
	secretRaw, err := removedSecretDeletionID.MarshalBinary()
	if err != nil {
		return errors.New("falha ao ler o dump")
	}
	foundSecretDeletion, err := dumpContains(dir, secretRaw)
	if err != nil || foundSecretDeletion {
		return errors.New("o dump foi alterado depois da exclusão")
	}
	if err := backupObjectIntact(dir, removedObjectKey, removedObjectVersion); err != nil {
		return err
	}
	if err := requireKeyArtifact(mustChild(dir, "recovery")); err != nil {
		return errors.New("o backup não conservou o material da chave")
	}
	if err := ensureMaterialNotLeaked(dir); err != nil {
		return err
	}
	writable, err := dumpWritable(dir)
	if err != nil || writable {
		return errors.New("o dump não permaneceu somente leitura")
	}
	return nil
}
