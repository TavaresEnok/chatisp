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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
)

const (
	keptObjectKey        = "drill/anexo-mantido"
	removedObjectKey     = "drill/anexo-removido"
	keptObjectVersion    = "versao-mantida"
	removedObjectVersion = "versao-removida"
	keyVersion           = "versao-chave-ensaio"
	secretAAD            = "ensaio-aad"
)

var (
	tenantID            = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	conversationID      = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	keptAttachmentID    = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	removedAttachmentID = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	keptRequestID       = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	removedRequestID    = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	namespaceID         = uuid.MustParse("77777777-7777-4777-8777-777777777777")
	secretID            = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	connectorID         = uuid.MustParse("99999999-9999-4999-8999-999999999999")
	deletionID          = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	secretCipher        = []byte("ensaio-cipher")
	fixtureExpiry       = time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
)

// Options selects the disposable cluster and the directory that keeps the dump.
type Options struct {
	AdminURL string
	DumpDir  string
}

// Report is the drill result. It carries no capacity figure.
// Database is the disposable database that received the restore.
// Duration is the measured recovery time of this drill.
type Report struct {
	Database                         string
	Duration                         time.Duration
	AttachmentReferenceOK            bool
	KeyReferenceOK                   bool
	DeletionTablePresent             bool
	DeletionReapplied                bool
	RemovedAttachmentRestored        bool
	ImmutableBackupErasedImmediately bool
	LoadAcceptedAsCapacity           bool
	DumpRetained                     bool
	Text                             string
}

type deletion struct {
	TenantID  uuid.UUID
	Subject   string
	SubjectID uuid.UUID
}

// Run restores a test dump on databases it creates, checks the attachment and
// key references, and reapplies deletion_records when that table exists.
// The dump file stays on disk. Load is not accepted as capacity.
func Run(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if err := guardDestination(opts.AdminURL, opts.DumpDir); err != nil {
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

	if err := seed(ctx, sourceConn); err != nil {
		return Report{}, err
	}
	if err := writeDump(ctx, sourceConn, opts.DumpDir); err != nil {
		return Report{}, err
	}
	if err := recordDeletion(ctx, sourceConn); err != nil {
		return Report{}, err
	}
	ledger, err := readLedger(ctx, sourceConn)
	if err != nil {
		return Report{}, err
	}
	if len(ledger) == 0 {
		return Report{}, errors.New("falha ao registrar a exclusão posterior ao backup")
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

	if err := restoreDump(ctx, destConn, opts.DumpDir); err != nil {
		return Report{}, err
	}
	if err := expectRestored(ctx, destConn); err != nil {
		return Report{}, err
	}
	present, err := reapply(ctx, destConn, ledger)
	if err != nil {
		return Report{}, err
	}
	if present {
		if err := expectReapplied(ctx, destConn); err != nil {
			return Report{}, err
		}
	}
	if err := expectDumpRetained(opts.DumpDir); err != nil {
		return Report{}, err
	}

	report := Report{
		Database:                         dest,
		Duration:                         time.Since(started),
		AttachmentReferenceOK:            true,
		KeyReferenceOK:                   true,
		DeletionTablePresent:             present,
		DeletionReapplied:                present,
		RemovedAttachmentRestored:        true,
		ImmutableBackupErasedImmediately: false,
		LoadAcceptedAsCapacity:           false,
		DumpRetained:                     true,
		Text:                             reportText(present),
	}
	return report, nil
}

func reportText(tablePresent bool) string {
	const head = "O ensaio restaurou um dump de PostgreSQL de teste num banco descartável. A referência do anexo e a referência da chave conferem com o backup. "
	const tail = "O backup imutável não é prometido como apagado na hora. A carga não foi aceita como capacidade."
	if tablePresent {
		return head + "As exclusões registradas em deletion_records foram reaplicadas. " + tail
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

func seed(ctx context.Context, conn *pgx.Conn) error {
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO attachments (
			tenant_id, id, conversation_id, request_id, direction, name,
			expected_bytes, object_key, object_version, expires_at
		) VALUES
			($1, $2, $3, $4, 'incoming', 'mantido.txt', 128, $5, $6, $7),
			($1, $8, $3, $9, 'incoming', 'removido.txt', 64, $10, $11, $7)
	`, tenantID, keptAttachmentID, conversationID, keptRequestID, keptObjectKey, keptObjectVersion, fixtureExpiry,
		removedAttachmentID, removedRequestID, removedObjectKey, removedObjectVersion); err != nil {
		return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO credential_secrets (
			tenant_id, id, entity, entity_id, field, key_version, ciphertext, aad
		) VALUES ($1, $2, 'channel_connector', $3, 'access_token', $4, $5, $6)
	`, tenantID, secretID, connectorID, keyVersion, secretCipher, secretAAD); err != nil {
		return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO deletion_records (id, tenant_id, subject, subject_id)
		VALUES ($1, $2, 'attachment', $3)
	`, deletionID, tenantID, removedAttachmentID); err != nil {
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

func reapply(ctx context.Context, conn *pgx.Conn, ledger []deletion) (bool, error) {
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
			tag, err = tx.Exec(ctx, `DELETE FROM attachments WHERE tenant_id = $1 AND id = $2`, rec.TenantID, rec.SubjectID)
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
	return true, nil
}

func expectRestored(ctx context.Context, conn *pgx.Conn) error {
	if err := expectKeptReferences(ctx, conn); err != nil {
		return err
	}
	var objectKey, objectVersion string
	var conversation uuid.UUID
	err := conn.QueryRow(ctx, `
		SELECT object_key, object_version, conversation_id
		FROM attachments
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, removedAttachmentID).Scan(&objectKey, &objectVersion, &conversation)
	if err != nil || objectKey != removedObjectKey || objectVersion != removedObjectVersion || conversation != conversationID {
		return errors.New("o restore não devolveu o anexo excluído depois do backup")
	}
	return nil
}

func expectReapplied(ctx context.Context, conn *pgx.Conn) error {
	if err := expectKeptReferences(ctx, conn); err != nil {
		return err
	}
	var stillThere bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM attachments WHERE tenant_id = $1 AND id = $2
		)
	`, tenantID, removedAttachmentID).Scan(&stillThere); err != nil {
		return errors.New("falha ao conferir a exclusão reaplicada")
	}
	if stillThere {
		return errors.New("anexo excluído permaneceu após reaplicar deletion_records")
	}
	var recorded bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM deletion_records
			WHERE tenant_id = $1 AND subject = 'attachment' AND subject_id = $2
		)
	`, tenantID, removedAttachmentID).Scan(&recorded); err != nil || !recorded {
		return errors.New("deletion_records não guardou a exclusão reaplicada")
	}
	return nil
}

func expectKeptReferences(ctx context.Context, conn *pgx.Conn) error {
	var objectKey, objectVersion string
	var conversation uuid.UUID
	err := conn.QueryRow(ctx, `
		SELECT object_key, object_version, conversation_id
		FROM attachments
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, keptAttachmentID).Scan(&objectKey, &objectVersion, &conversation)
	if err != nil || objectKey != keptObjectKey || objectVersion != keptObjectVersion || conversation != conversationID {
		return errors.New("referência de anexo divergente depois do restore")
	}
	var conversationExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM conversations WHERE tenant_id = $1 AND id = $2)
	`, tenantID, conversationID).Scan(&conversationExists); err != nil || !conversationExists {
		return errors.New("referência de anexo divergente depois do restore")
	}
	var version string
	var cipher []byte
	err = conn.QueryRow(ctx, `
		SELECT key_version, ciphertext
		FROM credential_secrets
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, secretID).Scan(&version, &cipher)
	if err != nil || version != keyVersion || string(cipher) != string(secretCipher) {
		return errors.New("referência de chave divergente depois do restore")
	}
	return nil
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
	writable, err := dumpWritable(dir)
	if err != nil || writable {
		return errors.New("o dump não permaneceu somente leitura")
	}
	return nil
}
