package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
)

func TestServerIsPostgreSQL18(t *testing.T) {
	if testEnv.ServerVersionNum/10000 != 18 {
		t.Fatalf("server_version %s (%d)", testEnv.ServerVersion, testEnv.ServerVersionNum)
	}
	if testEnv.Embedded && (testEnv.BundleVersion != "18.6.0" || testEnv.ServerVersionNum != 180006) {
		t.Fatalf("embutido %s server_version %s (%d)", testEnv.BundleVersion, testEnv.ServerVersion, testEnv.ServerVersionNum)
	}
}

func TestAppRoleHasNoBypassAndDoesNotOwnTables(t *testing.T) {
	ctx := context.Background()
	admin := adminConn(t)
	defer admin.Close(ctx)

	type roleRow struct {
		name     string
		super    bool
		bypass   bool
		canLogin bool
	}
	rows, err := admin.Query(ctx, `
		SELECT rolname, rolsuper, rolbypassrls, rolcanlogin
		FROM pg_roles
		WHERE rolname IN ('chatisp_owner', 'chatisp_app', 'chatisp_platform', 'chatisp_ingress')
		ORDER BY rolname
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []roleRow
	for rows.Next() {
		var row roleRow
		if err := rows.Scan(&row.name, &row.super, &row.bypass, &row.canLogin); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("papéis: %d", len(got))
	}
	for _, row := range got {
		if row.super || row.bypass || !row.canLogin {
			t.Fatalf("papel %s super=%v bypass=%v login=%v", row.name, row.super, row.bypass, row.canLogin)
		}
	}

	type tableRow struct {
		name  string
		owner string
		rls   bool
		force bool
	}
	tables, err := admin.Query(ctx, `
		SELECT c.relname, pg_get_userbyid(c.relowner), c.relrowsecurity, c.relforcerowsecurity
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND c.relname IN ('tenants', 'users', 'memberships', 'audit_logs', 'platform_audit_logs')
		ORDER BY c.relname
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer tables.Close()
	seen := map[string]tableRow{}
	for tables.Next() {
		var row tableRow
		if err := tables.Scan(&row.name, &row.owner, &row.rls, &row.force); err != nil {
			t.Fatal(err)
		}
		seen[row.name] = row
		if row.owner != db.RoleOwner {
			t.Fatalf("dono de %s: %s", row.name, row.owner)
		}
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tenants", "memberships", "audit_logs"} {
		row, ok := seen[name]
		if !ok || !row.rls || !row.force {
			t.Fatalf("RLS forçada ausente em %s: %+v", name, row)
		}
	}
	for _, name := range []string{"users", "platform_audit_logs"} {
		row, ok := seen[name]
		if !ok || row.rls || row.force {
			t.Fatalf("%s deveria permanecer global, sem RLS: %+v", name, row)
		}
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
	if _, err := testPools.App.Exec(ctx, `ALTER TABLE memberships DISABLE ROW LEVEL SECURITY`); err == nil {
		t.Fatal("chatisp_app alterou a política da tabela")
	}

	var (
		appUsers       bool
		appPlatformLog bool
		appMembership  bool
		appAudit       bool
		ingressAudit   bool
		platformUsers  bool
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			has_table_privilege('chatisp_app', 'public.users', 'SELECT'),
			has_table_privilege('chatisp_app', 'public.platform_audit_logs', 'SELECT'),
			has_table_privilege('chatisp_app', 'public.memberships', 'INSERT'),
			has_table_privilege('chatisp_app', 'public.audit_logs', 'SELECT'),
			has_table_privilege('chatisp_ingress', 'public.audit_logs', 'SELECT'),
			has_column_privilege('chatisp_platform', 'public.users', 'email', 'SELECT')
	`).Scan(&appUsers, &appPlatformLog, &appMembership, &appAudit, &ingressAudit, &platformUsers); err != nil {
		t.Fatal(err)
	}
	if appUsers || appPlatformLog || ingressAudit || !appMembership || !appAudit || !platformUsers {
		t.Fatalf("privilégios app_users=%v app_platform_log=%v app_membership=%v app_audit=%v ingress_audit=%v platform_users=%v",
			appUsers, appPlatformLog, appMembership, appAudit, ingressAudit, platformUsers)
	}
}

func TestTenantIsolationAndCompositeForeignKey(t *testing.T) {
	ctx := context.Background()
	left := seedTenant(t, "agent")
	right := seedTenant(t, "supervisor")

	var auditA db.AuditLog
	err := testStore.WithTenant(ctx, left.tenant, func(ctx context.Context) error {
		row, err := testStore.CreateAuditLog(ctx, db.NewAuditLog{
			MembershipID: &left.membership,
			ActorUserID:  &left.user,
			Action:       "member.add",
			TargetType:   "membership",
			TargetID:     &left.membership,
			Result:       "success",
		})
		auditA = row
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	err = testStore.WithTenant(ctx, right.tenant, func(ctx context.Context) error {
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		if len(logs) != 0 {
			t.Fatalf("tenant B leu %d auditorias", len(logs))
		}
		members, err := testStore.ListMemberships(ctx)
		if err != nil {
			return err
		}
		if len(members) != 1 || members[0].ID != right.membership || members[0].TenantID != right.tenant {
			t.Fatalf("memberships visíveis ao tenant B: %+v", members)
		}
		n, err := testStore.UpdateMembershipStatus(ctx, left.membership, "disabled")
		if err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("tenant B alterou %d linhas do tenant A", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The rejected insert aborts the transaction, so it stays in its own scope.
	err = testStore.WithTenant(ctx, right.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateAuditLog(ctx, db.NewAuditLog{
			MembershipID: &left.membership,
			Action:       "member.link",
			TargetType:   "membership",
			Result:       "denied",
		})
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "audit_logs_membership_fkey" {
		t.Fatalf("FK composta deveria rejeitar o vínculo cruzado: %v", err)
	}

	err = testStore.WithTenant(ctx, left.tenant, func(ctx context.Context) error {
		members, err := testStore.ListMemberships(ctx)
		if err != nil {
			return err
		}
		if len(members) != 1 || members[0].Status != "active" || members[0].ID != left.membership {
			t.Fatalf("membership A depois da tentativa de B: %+v", members)
		}
		logs, err := testStore.ListAuditLogs(ctx)
		if err != nil {
			return err
		}
		if len(logs) != 1 || logs[0].ID != auditA.ID || logs[0].TenantID != left.tenant {
			t.Fatalf("auditoria A: %+v", logs)
		}
		tenant, err := testStore.GetTenant(ctx)
		if err != nil {
			return err
		}
		if tenant.ID != left.tenant || tenant.Timezone != "America/Sao_Paulo" || tenant.Status != "active" {
			t.Fatalf("tenant A: %+v", tenant)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, right.tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.access_scope', 'platform', true)`); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 1 {
		t.Fatalf("chatisp_app com escopo de plataforma viu %d memberships", visible)
	}
	tag, err := tx.Exec(ctx, `UPDATE memberships SET status = 'disabled' WHERE id = $1`, left.membership)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("update cru do tenant B afetou %d linhas", tag.RowsAffected())
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (id, tenant_id, action, target_type, result)
		VALUES ($1, $2, 'peek', 'tenant', 'denied')
	`, uuid.New(), left.tenant)
	if !isRLS(err) {
		t.Fatalf("gravação cruzada deveria violar RLS: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var untouched string
	err = testStore.WithTenant(ctx, left.tenant, func(ctx context.Context) error {
		members, err := testStore.ListMemberships(ctx)
		if err != nil {
			return err
		}
		if len(members) != 1 {
			return errors.New("membership A sumiu")
		}
		untouched = members[0].Status
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if untouched != "active" {
		t.Fatalf("status final de A: %s", untouched)
	}
}

func TestPoliciesFailClosedWithoutTenantSetting(t *testing.T) {
	ctx := context.Background()
	seed := seedTenant(t, "admin")
	err := testStore.WithTenant(ctx, seed.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateAuditLog(ctx, db.NewAuditLog{
			Action:     "login",
			TargetType: "session",
			Result:     "success",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var audits int
	if err := testPools.App.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatalf("leitura sem contexto retornou %d auditorias", audits)
	}
	var tenants int
	if err := testPools.App.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&tenants); err != nil {
		t.Fatal(err)
	}
	if tenants != 0 {
		t.Fatalf("leitura sem contexto retornou %d tenants", tenants)
	}
	_, err = testPools.App.Exec(ctx, `
		INSERT INTO audit_logs (id, tenant_id, action, target_type, result)
		VALUES ($1, $2, 'login', 'session', 'failure')
	`, uuid.New(), seed.tenant)
	if !isRLS(err) {
		t.Fatalf("escrita sem contexto deveria ser negada pela política: %v", err)
	}
	_, err = testPools.App.Exec(ctx, `
		INSERT INTO memberships (id, tenant_id, user_id, role, status)
		VALUES ($1, $2, $3, 'agent', 'active')
	`, uuid.New(), seed.tenant, seed.user)
	if !isRLS(err) {
		t.Fatalf("membership sem contexto deveria ser negada: %v", err)
	}

	var platformSeen int
	if err := testPools.Platform.QueryRow(ctx, `SELECT count(*) FROM memberships`).Scan(&platformSeen); err != nil {
		t.Fatal(err)
	}
	if platformSeen != 0 {
		t.Fatalf("plataforma sem escopo viu %d memberships", platformSeen)
	}
	_, err = testPools.Platform.Exec(ctx, `
		INSERT INTO memberships (id, tenant_id, user_id, role, status)
		VALUES ($1, $2, $3, 'agent', 'active')
	`, uuid.New(), seed.tenant, seed.user)
	if !isRLS(err) {
		t.Fatalf("plataforma sem escopo não deveria gravar: %v", err)
	}
}

func TestGlobalTablesStayRestricted(t *testing.T) {
	ctx := context.Background()
	seed := seedTenant(t, "admin")
	_ = seed

	_, err := testPools.App.Exec(ctx, `SELECT email FROM users`)
	if !isPermission(err) {
		t.Fatalf("chatisp_app não deveria ler users: %v", err)
	}
	_, err = testPools.App.Exec(ctx, `SELECT actor FROM platform_audit_logs`)
	if !isPermission(err) {
		t.Fatalf("chatisp_app não deveria ler auditoria de plataforma: %v", err)
	}
	_, err = testPools.Ingress.Exec(ctx, `SELECT action FROM audit_logs`)
	if !isPermission(err) {
		t.Fatalf("chatisp_ingress não deveria ler auditoria de tenant: %v", err)
	}

	err = testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
			Actor:      "bootstrap",
			Action:     "tenant.create",
			TargetType: "tenant",
			Result:     "success",
		})
		if err != nil {
			return err
		}
		rows, err := testStore.ListPlatformAuditLogs(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errors.New("auditoria de plataforma não foi lida pelo papel de plataforma")
		}
		if _, err := testStore.ListMemberships(ctx); !errors.Is(err, db.ErrTenantContextRequired) {
			return errors.New("contexto de plataforma não pode substituir o tenant")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = testStore.WithTenant(ctx, seed.tenant, func(ctx context.Context) error {
		_, err := testStore.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
			Actor:      "app",
			Action:     "tenant.create",
			TargetType: "tenant",
			Result:     "denied",
		})
		if !errors.Is(err, db.ErrPlatformContextRequired) {
			return errors.New("contexto de tenant não pode gravar auditoria de plataforma")
		}
		if _, err := testStore.CreateMembership(ctx, db.NewMembership{
			UserID: seed.user,
			Role:   "owner",
			Status: "active",
		}); !errors.Is(err, db.ErrInvalidMembershipRole) {
			return errors.New("papel inválido deveria ser rejeitado pela aplicação")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPasswordHashStaysOnAuthPath(t *testing.T) {
	ctx := context.Background()
	admin := adminConn(t)
	defer admin.Close(ctx)

	var (
		platformSelectHash bool
		platformUpdateHash bool
		platformInsertHash bool
		appSelectHash      bool
		appExecRead        bool
		platformExecRead   bool
		ingressExecRead    bool
		platformSelectMail bool
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			has_column_privilege('chatisp_platform', 'public.users', 'password_hash', 'SELECT'),
			has_column_privilege('chatisp_platform', 'public.users', 'password_hash', 'UPDATE'),
			has_column_privilege('chatisp_platform', 'public.users', 'password_hash', 'INSERT'),
			has_column_privilege('chatisp_app', 'public.users', 'password_hash', 'SELECT'),
			has_function_privilege('chatisp_app', 'public.auth_password_hash_by_email(text)', 'EXECUTE'),
			has_function_privilege('chatisp_platform', 'public.auth_password_hash_by_email(text)', 'EXECUTE'),
			has_function_privilege('chatisp_ingress', 'public.auth_set_password_hash(uuid, text)', 'EXECUTE'),
			has_column_privilege('chatisp_platform', 'public.users', 'email', 'SELECT')
	`).Scan(
		&platformSelectHash, &platformUpdateHash, &platformInsertHash, &appSelectHash,
		&appExecRead, &platformExecRead, &ingressExecRead, &platformSelectMail,
	); err != nil {
		t.Fatal(err)
	}
	if platformSelectHash || platformUpdateHash || platformInsertHash || appSelectHash || platformExecRead || ingressExecRead || !appExecRead || !platformSelectMail {
		t.Fatalf("privilégios de credencial platform_select=%v platform_update=%v platform_insert=%v app_select=%v app_exec=%v platform_exec=%v ingress_exec=%v platform_email=%v",
			platformSelectHash, platformUpdateHash, platformInsertHash, appSelectHash, appExecRead, platformExecRead, ingressExecRead, platformSelectMail)
	}

	var owner string
	var secdef bool
	var config []string
	if err := admin.QueryRow(ctx, `
		SELECT r.rolname, p.prosecdef, p.proconfig
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_roles r ON r.oid = p.proowner
		WHERE n.nspname = 'public' AND p.proname = 'auth_set_password_hash'
	`).Scan(&owner, &secdef, &config); err != nil {
		t.Fatal(err)
	}
	if owner != db.RoleOwner || !secdef || !contains(joinConfig(config), "search_path=pg_catalog") {
		t.Fatalf("função de autenticação owner=%s secdef=%v config=%v", owner, secdef, config)
	}

	userID := uuid.New()
	email := userID.String() + "@example.com"
	const secret = "hash-for-tests"
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: userID, Email: email, Status: "active"})
		if err != nil {
			return err
		}
		if err := testStore.SetPasswordHash(ctx, userID, secret); !errors.Is(err, db.ErrAuthContextRequired) {
			return errors.New("plataforma não pode gravar hash")
		}
		if _, err := testStore.PasswordHashByEmail(ctx, email); !errors.Is(err, db.ErrAuthContextRequired) {
			return errors.New("plataforma não pode ler hash")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var visibleEmail string
	if err := testPools.Platform.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&visibleEmail); err != nil {
		t.Fatalf("plataforma deveria ler o e-mail: %v", err)
	}
	if visibleEmail != email {
		t.Fatalf("e-mail visível: %s", visibleEmail)
	}
	expectPlatformDenied(t, `SELECT password_hash FROM users WHERE id = $1`, userID)
	expectPlatformDenied(t, `UPDATE users SET password_hash = 'substituido' WHERE id = $1`, userID)
	expectPlatformDenied(t, `UPDATE users SET email = password_hash WHERE id = $1`, userID)
	expectPlatformDenied(t, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, $2, 'outro', 'invited')
	`, uuid.New(), "novo-"+email)
	expectPlatformDenied(t, `SELECT auth_password_hash_by_email($1)`, email)

	appTx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appTx.Exec(ctx, `SELECT auth_password_hash_by_email($1)`, email); !isAuthDenied(err) {
		_ = appTx.Rollback(ctx)
		t.Fatalf("função sem escopo de autenticação: %v", err)
	}
	if err := appTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := testPools.App.Exec(ctx, `SELECT password_hash FROM users`); !isPermission(err) {
		t.Fatalf("chatisp_app leu password_hash direto: %v", err)
	}

	err = testStore.WithAuth(ctx, func(ctx context.Context) error {
		if _, err := testStore.PasswordHashByEmail(ctx, email); !errors.Is(err, db.ErrNotFound) {
			return errors.New("usuário sem hash deveria ser indistinguível de ausente")
		}
		if err := testStore.SetPasswordHash(ctx, userID, secret); err != nil {
			return err
		}
		got, err := testStore.PasswordHashByEmail(ctx, email)
		if err != nil {
			return err
		}
		if got != secret {
			return errors.New("hash lido pelo caminho de autenticação não confere")
		}
		if _, err := testStore.CreateUser(ctx, db.NewUser{Email: "outra@example.com", Status: "active"}); !errors.Is(err, db.ErrPlatformContextRequired) {
			return errors.New("autenticação não substitui o contexto de plataforma")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapAndMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	urls, err := db.Bootstrap(ctx, testEnv.AdminURL, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bootstrap(ctx, testEnv.AdminURL, testSecrets); err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner, App: urls.App})
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	var owner string
	if err := pools.Owner.QueryRow(ctx, `SELECT current_user`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != db.RoleOwner {
		t.Fatalf("current_user do owner: %s", owner)
	}
	if testPools.Owner != nil {
		t.Fatal("o pool duradouro do teste de domínio não deve carregar chatisp_owner")
	}
}

type seeded struct {
	tenant     uuid.UUID
	user       uuid.UUID
	membership uuid.UUID
}

func seedTenant(t *testing.T, role string) seeded {
	t.Helper()
	ctx := context.Background()
	item := seeded{tenant: uuid.New(), user: uuid.New(), membership: uuid.New()}
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       item.tenant,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
			Limits:   json.RawMessage(`{"max_agents":2}`),
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{
			ID:     item.user,
			Email:  item.user.String() + "@example.com",
			Status: "active",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenant(ctx, item.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     item.membership,
			UserID: item.user,
			Role:   role,
			Status: "active",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testEnv.AdminURL)
	if err != nil {
		t.Fatal("falha ao conectar como administrador do teste")
	}
	return conn
}

func isRLS(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501" && contains(pgErr.Message, "row-level security")
}

func isPermission(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501" && contains(pgErr.Message, "permission denied")
}

func expectPlatformDenied(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPools.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.access_scope', 'auth', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, sql, args...); !isPermission(err) {
		t.Fatalf("plataforma não foi barrada (%s): %v", sql, err)
	}
}

func isAuthDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func joinConfig(values []string) string {
	out := ""
	for _, value := range values {
		if out != "" {
			out += ","
		}
		out += value
	}
	return out
}

func contains(msg, part string) bool {
	return len(msg) >= len(part) && (msg == part || len(part) == 0 || (len(msg) > 0 && (indexOf(msg, part) >= 0)))
}

func indexOf(msg, part string) int {
	for i := 0; i+len(part) <= len(msg); i++ {
		if msg[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
