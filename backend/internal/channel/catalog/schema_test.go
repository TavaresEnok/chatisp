package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
)

func TestCatalogRoleBoundary(t *testing.T) {
	ctx := context.Background()
	admin := adminConn(t)
	defer admin.Close(ctx)

	if testEnv.ServerVersionNum/10000 != 18 {
		t.Fatalf("server_version %s (%d)", testEnv.ServerVersion, testEnv.ServerVersionNum)
	}

	var appBypass bool
	if err := testPools.App.QueryRow(ctx, `
		SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user
	`).Scan(&appBypass); err != nil {
		t.Fatal(err)
	}
	if appBypass {
		t.Fatal("chatisp_app está com BYPASSRLS")
	}

	tables := []string{
		"channel_connectors",
		"inboxes",
		"contacts",
		"contact_channel_identities",
	}
	rows, err := admin.Query(ctx, `
		SELECT c.relname, pg_get_userbyid(c.relowner), c.relrowsecurity, c.relforcerowsecurity
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND c.relname = ANY($1)
		ORDER BY c.relname
	`, tables)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var name, owner string
		var rls, force bool
		if err := rows.Scan(&name, &owner, &rls, &force); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		seen[name] = true
		if owner != db.RoleOwner || !rls || !force {
			rows.Close()
			t.Fatalf("%s owner=%s rls=%v force=%v", name, owner, rls, force)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(seen) != len(tables) {
		t.Fatalf("tabelas do catálogo: %d", len(seen))
	}

	if _, err := testPools.App.Exec(ctx, `ALTER TABLE contacts DISABLE ROW LEVEL SECURITY`); err == nil {
		t.Fatal("chatisp_app alterou a política da tabela")
	}

	var (
		platformContacts bool
		ingressContacts  bool
		platformInbox    bool
		ingressInbox     bool
		appInsert        bool
		platformConvo    bool
		ingressConvo     bool
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			has_table_privilege('chatisp_platform', 'public.contacts', 'SELECT'),
			has_table_privilege('chatisp_ingress', 'public.contacts', 'SELECT'),
			has_table_privilege('chatisp_platform', 'public.inboxes', 'INSERT'),
			has_table_privilege('chatisp_ingress', 'public.inboxes', 'INSERT'),
			has_table_privilege('chatisp_app', 'public.channel_connectors', 'INSERT'),
			has_table_privilege('chatisp_platform', 'public.conversations', 'SELECT'),
			has_table_privilege('chatisp_ingress', 'public.conversations', 'SELECT')
	`).Scan(&platformContacts, &ingressContacts, &platformInbox, &ingressInbox, &appInsert, &platformConvo, &ingressConvo); err != nil {
		t.Fatal(err)
	}
	if platformContacts || ingressContacts || platformInbox || ingressInbox || platformConvo || ingressConvo || !appInsert {
		t.Fatalf("privilégios platform_contacts=%v ingress_contacts=%v platform_inbox=%v ingress_inbox=%v app_insert=%v platform_convo=%v ingress_convo=%v",
			platformContacts, ingressContacts, platformInbox, ingressInbox, appInsert, platformConvo, ingressConvo)
	}

	for _, rolePool := range []struct {
		name string
		exec func(context.Context, string, ...any) (pgconn.CommandTag, error)
	}{
		{"platform", testPools.Platform.Exec},
		{"ingress", testPools.Ingress.Exec},
	} {
		_, err := rolePool.exec(ctx, `SELECT count(*) FROM contacts`)
		if !isPermission(err) {
			t.Fatalf("%s leu contatos: %v", rolePool.name, err)
		}
		_, err = rolePool.exec(ctx, `SELECT summary FROM conversations`)
		if !isPermission(err) {
			t.Fatalf("%s leu conversations: %v", rolePool.name, err)
		}
	}

	var convoCols string
	if err := admin.QueryRow(ctx, `
		SELECT string_agg(column_name, ',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'conversations'
	`).Scan(&convoCols); err != nil {
		t.Fatal(err)
	}
	if convoCols != "id,tenant_id,summary,created_at" {
		t.Fatalf("conversations mudou: %s", convoCols)
	}
}

func TestCatalogIsolationCompositeKeysAndOrigin(t *testing.T) {
	left := seedTenant(t)
	right := seedTenant(t)

	origin := "meta-phone-" + uuid.NewString()
	leftConnector := uuid.New()
	leftInboxA := uuid.New()
	leftInboxB := uuid.New()
	leftConnectorB := uuid.New()
	leftContact := uuid.New()
	leftIdentity := uuid.New()

	withTenant(t, left, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled, credential_ref)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true, 'cred_ref_left')
		`, leftConnector, left, origin); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'pending', true)
		`, leftConnectorB, left, origin+"-b"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name, presented_number)
			VALUES ($1, $2, $3, 'Suporte', '+5511999990001')
		`, leftInboxA, left, leftConnector); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name)
			VALUES ($1, $2, $3, 'Comercial')
		`, leftInboxB, left, leftConnectorB); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name)
			VALUES ($1, $2, 'Cliente sem telefone')
		`, leftContact, left); err != nil {
			t.Fatal(err)
		}
		var phone, email *string
		if err := tx.QueryRow(ctx, `SELECT phone, email FROM contacts WHERE id = $1`, leftContact).Scan(&phone, &email); err != nil {
			t.Fatal(err)
		}
		if phone != nil || email != nil {
			t.Fatalf("contato nasceu com phone=%v email=%v", phone, email)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id, evidence)
			VALUES ($1, $2, $3, $4, 'wa-id-1', 'webhook')
		`, leftIdentity, left, leftContact, leftConnector); err != nil {
			t.Fatal(err)
		}
		var inboxes int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM inboxes`).Scan(&inboxes); err != nil {
			t.Fatal(err)
		}
		if inboxes != 2 {
			t.Fatalf("caixas do tenant A: %d", inboxes)
		}
	})

	withTenant(t, right, func(ctx context.Context, tx pgx.Tx) {
		var connectors, inboxes, contacts, identities int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_connectors`).Scan(&connectors); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM inboxes`).Scan(&inboxes); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contacts`).Scan(&contacts); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contact_channel_identities`).Scan(&identities); err != nil {
			t.Fatal(err)
		}
		if connectors != 0 || inboxes != 0 || contacts != 0 || identities != 0 {
			t.Fatalf("tenant B viu connectors=%d inboxes=%d contacts=%d identities=%d", connectors, inboxes, contacts, identities)
		}
		tag, err := tx.Exec(ctx, `UPDATE channel_connectors SET enabled = false WHERE id = $1`, leftConnector)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() != 0 {
			t.Fatalf("tenant B alterou %d conectores", tag.RowsAffected())
		}

		_, err = expectExec(t, ctx, tx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name)
			VALUES ($1, $2, $3, 'Caixa cruzada')
		`, uuid.New(), right, leftConnector)
		if !isForeignKey(err) {
			t.Fatalf("caixa apontando para conector de outro tenant: %v", err)
		}
		rightConnector := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'pending', true)
		`, rightConnector, right, origin+"-right"); err != nil {
			t.Fatal(err)
		}
		_, err = expectExec(t, ctx, tx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, 'wa-id-cross')
		`, uuid.New(), right, leftContact, rightConnector)
		if !isForeignKey(err) {
			t.Fatalf("identidade apontando para contato de outro tenant: %v", err)
		}
		rightContact := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Outro')
		`, rightContact, right); err != nil {
			t.Fatal(err)
		}
		_, err = expectExec(t, ctx, tx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, 'wa-id-cross-connector')
		`, uuid.New(), right, rightContact, leftConnector)
		if !isForeignKey(err) {
			t.Fatalf("identidade apontando para conector de outro tenant: %v", err)
		}
		_, err = expectExec(t, ctx, tx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, uuid.New(), right, origin)
		if !isUnique(err) {
			t.Fatalf("remote_origin ativo duplicado: %v", err)
		}
	})

	withTenant(t, left, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			UPDATE channel_connectors SET status = 'disconnected' WHERE id = $1
		`, leftConnector); err != nil {
			t.Fatal(err)
		}
	})
	withTenant(t, right, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, uuid.New(), right, origin); err != nil {
			t.Fatalf("origem liberada deveria ser reutilizável: %v", err)
		}
	})
}

func TestCatalogFailsClosedWithoutTenant(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t)
	contactID := uuid.New()
	withTenant(t, tenantID, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Visível só no tenant')
		`, contactID, tenantID); err != nil {
			t.Fatal(err)
		}
	})

	var n int
	if err := testPools.App.QueryRow(ctx, `SELECT count(*) FROM contacts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("leitura sem contexto retornou %d contatos", n)
	}
	_, err := testPools.App.Exec(ctx, `
		INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Sem contexto')
	`, uuid.New(), tenantID)
	if !isRLS(err) {
		t.Fatalf("escrita sem contexto deveria ser negada: %v", err)
	}
	_, err = testPools.App.Exec(ctx, `
		INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1, $2, $3, 'Sem contexto')
	`, uuid.New(), tenantID, uuid.New())
	if !isRLS(err) {
		t.Fatalf("caixa sem contexto deveria ser negada: %v", err)
	}
}

func TestConnectorTypeAndEvolutionDefaults(t *testing.T) {
	tenantID := seedTenant(t)
	connectorID := uuid.New()
	origin := "evo-origin-" + uuid.NewString()

	withTenant(t, tenantID, func(ctx context.Context, tx pgx.Tx) {
		var enabled bool
		if err := tx.QueryRow(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status)
			VALUES ($1, $2, 'evolution', $3, 'disconnected')
			RETURNING enabled
		`, uuid.New(), tenantID, origin).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled {
			t.Fatal("evolution nasceu habilitada")
		}
		_, err := expectExec(t, ctx, tx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'evolution', $3, 'disconnected', true)
		`, uuid.New(), tenantID, origin+"-on")
		if !isCheck(err) {
			t.Fatalf("evolution habilitada deveria ser rejeitada: %v", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', false)
		`, connectorID, tenantID, origin+"-meta"); err != nil {
			t.Fatal(err)
		}
		_, err = expectExec(t, ctx, tx, `
			UPDATE channel_connectors SET connector_type = 'evolution' WHERE id = $1
		`, connectorID)
		if !isCheck(err) {
			t.Fatalf("troca de tipo com conexão ativa: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_connectors SET status = 'disconnected' WHERE id = $1
		`, connectorID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_connectors SET connector_type = 'evolution' WHERE id = $1
		`, connectorID); err != nil {
			t.Fatalf("troca de tipo após desconectar: %v", err)
		}
	})
}

func TestCatalogStoresNoPlainCredential(t *testing.T) {
	ctx := context.Background()
	admin := adminConn(t)
	defer admin.Close(ctx)

	rows, err := admin.Query(ctx, `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name IN (
		    'channel_connectors', 'inboxes', 'contacts', 'contact_channel_identities'
		  )
		ORDER BY table_name, column_name
	`)
	if err != nil {
		t.Fatal(err)
	}
	hasCredentialRef := false
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if column == "credential_ref" && table == "channel_connectors" {
			hasCredentialRef = true
			continue
		}
		lower := strings.ToLower(column)
		for _, banned := range []string{"secret", "token", "password", "cpf", "cnpj", "document", "ciphertext"} {
			if strings.Contains(lower, banned) {
				rows.Close()
				t.Fatalf("coluna %s.%s parece material sensível em claro", table, column)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if !hasCredentialRef {
		t.Fatal("falta a referência opaca de credencial")
	}

	var refType, nullable string
	if err := admin.QueryRow(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'channel_connectors'
		  AND column_name = 'credential_ref'
	`).Scan(&refType, &nullable); err != nil {
		t.Fatal(err)
	}
	if refType != "text" || nullable != "YES" {
		t.Fatalf("credential_ref type=%s nullable=%s", refType, nullable)
	}
}

func seedTenant(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tenantID := uuid.New()
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       tenantID,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
			Limits:   json.RawMessage(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenantID
}

func expectExec(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) (pgconn.CommandTag, error) {
	t.Helper()
	if _, err := tx.Exec(ctx, `SAVEPOINT catalog_expect`); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if _, rbErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT catalog_expect`); rbErr != nil {
		t.Fatal(rbErr)
	}
	return tag, err
}

func withTenant(t *testing.T, tenantID uuid.UUID, fn func(context.Context, pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		t.Fatal(err)
	}
	fn(ctx, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func isRLS(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501" && strings.Contains(pgErr.Message, "row-level security")
}

func isPermission(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501" && strings.Contains(pgErr.Message, "permission denied")
}

func isForeignKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isCheck(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
