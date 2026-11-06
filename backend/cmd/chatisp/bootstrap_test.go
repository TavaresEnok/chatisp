package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/testdb"
)

func TestBootstrapFirstAdminOnEmptyInstallation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("testdb start: %v", err)
	}
	t.Cleanup(env.Close)
	urls, _, err := prepareDevDatabase(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	migrationsDir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=" + urls.Owner,
		"CHATISP_APP_DATABASE_URL=" + urls.App,
		"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
		"CHATISP_MIGRATIONS_DIR=" + migrationsDir,
	}
	const email = "Dono@Provedor.example"
	const password = "senha-do-dono-10"
	runBoot := func(stdin string, args ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runWithInput(ctx, append([]string{"bootstrap"}, args...), environ, strings.NewReader(stdin), &stdout, &stderr)
		return stdout.String() + stderr.String(), err
	}

	// Refusals that happen before any write.
	if _, err := runBoot(password+"\n", "-superadmin"); err == nil {
		t.Fatal("bootstrap sem -email foi aceito")
	}
	if _, err := runBoot("curta\n", "-email", email); !errors.Is(err, auth.ErrInvalidPassword) {
		t.Fatalf("senha fraca: %v", err)
	}
	if _, err := runBoot("", "-email", email); err == nil {
		t.Fatal("bootstrap sem senha foi aceito")
	}
	if _, err := runBoot(password+"\n", "-email", email, "-timezone", "Lua/Base"); !errors.Is(err, auth.ErrInvalidTimezone) {
		t.Fatalf("fuso inválido: %v", err)
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("recusa gravou %d usuário(s)", n)
	}

	out, err := runBoot(password+"\n", "-email", email, "-superadmin")
	if err != nil {
		t.Fatalf("bootstrap: %v\n%s", err, out)
	}
	if strings.Contains(out, password) {
		t.Fatal("bootstrap ecoou a senha")
	}

	store, closeStore := openStore(t, ctx, urls)
	defer closeStore()
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Options{Store: store, Mail: &mail.Fake{}, Policy: auth.DefaultPolicy, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := svc.Login(ctx, strings.ToLower(email), password, "", "203.0.113.80")
	if err != nil {
		t.Fatalf("login do primeiro admin: %v", err)
	}
	principal, err := svc.Authenticate(ctx, issued.Token, issued.CSRFToken, false)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Role != "admin" || !principal.IsSuperadmin || principal.TenantID == uuid.Nil {
		t.Fatalf("principal: %+v", principal)
	}
	if principal.EnrollmentOnly || principal.MFAEnabled {
		t.Fatal("primeiro acesso deve ter sessão completa sem fator confirmado")
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM platform_audit_logs WHERE action = 'platform.bootstrap' AND result = 'success'`); n != 1 {
		t.Fatalf("auditoria do bootstrap: %d", n)
	}

	// Same e-mail again: no change, password untouched.
	out, err = runBoot("outra-senha-forte-10\n", "-email", email)
	if err != nil || !strings.Contains(out, "nada foi alterado") {
		t.Fatalf("repetição: %v %s", err, out)
	}
	if _, err := svc.Login(ctx, strings.ToLower(email), password, "", "203.0.113.81"); err != nil {
		t.Fatalf("repetição trocou a senha: %v", err)
	}
	// Another e-mail on a populated installation is refused.
	if _, err := runBoot(password+"\n", "-email", "intruso@example.com", "-superadmin"); !errors.Is(err, errAlreadyBootstrapped) {
		t.Fatalf("segundo bootstrap: %v", err)
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM tenants`); n != 1 {
		t.Fatalf("provedores: %d", n)
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM users WHERE is_superadmin`); n != 1 {
		t.Fatalf("superadmins: %d", n)
	}
}

// A run interrupted after the first transaction leaves an invited membership, which cannot
// sign in; the next run with the same e-mail finishes it.
func TestBootstrapResumesInterruptedRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("testdb start: %v", err)
	}
	t.Cleanup(env.Close)
	urls, _, err := prepareDevDatabase(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	migrationsDir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=" + urls.Owner,
		"CHATISP_APP_DATABASE_URL=" + urls.App,
		"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
		"CHATISP_MIGRATIONS_DIR=" + migrationsDir,
	}
	store, closeStore := openStore(t, ctx, urls)
	defer closeStore()
	userID, tenantID := uuid.New(), uuid.New()
	if err := store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := store.CreateTenant(ctx, db.NewTenant{ID: tenantID, Timezone: "America/Sao_Paulo", Status: "active"}); err != nil {
			return err
		}
		if _, err := store.CreateUser(ctx, db.NewUser{ID: userID, Email: "meio@example.com", Status: "active"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (id, tenant_id, user_id, role, status) VALUES ($1, $2, $3, 'admin', 'invited')`, uuid.New(), tenantID, userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Options{Store: store, Mail: &mail.Fake{}, Policy: auth.DefaultPolicy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "meio@example.com", "senha-retomada-10", "", "203.0.113.90"); err == nil {
		t.Fatal("estado parcial permitiu login")
	}
	var stdout, stderr bytes.Buffer
	if err := runWithInput(ctx, []string{"bootstrap", "-email", "meio@example.com"}, environ, strings.NewReader("senha-retomada-10\n"), &stdout, &stderr); err != nil {
		t.Fatalf("retomada: %v %s", err, stderr.String())
	}
	if _, err := svc.Login(ctx, "meio@example.com", "senha-retomada-10", "", "203.0.113.91"); err != nil {
		t.Fatalf("login após retomada: %v", err)
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM tenants`); n != 1 {
		t.Fatalf("retomada criou outro provedor: %d", n)
	}

	// On a populated installation a pending invitation is not activated by bootstrap.
	invitedUser := uuid.New()
	if err := store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := store.CreateUser(ctx, db.NewUser{ID: invitedUser, Email: "convidado@example.com", Status: "active"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (id, tenant_id, user_id, role, status) VALUES ($1, $2, $3, 'admin', 'invited')`, uuid.New(), tenantID, invitedUser)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := runWithInput(ctx, []string{"bootstrap", "-email", "convidado@example.com"}, environ, strings.NewReader("senha-sem-token-10\n"), &stdout, &stderr); !errors.Is(err, errAlreadyBootstrapped) {
		t.Fatalf("bootstrap ativou convite pendente: %v", err)
	}
	if _, err := svc.Login(ctx, "convidado@example.com", "senha-sem-token-10", "", "203.0.113.92"); err == nil {
		t.Fatal("convite pendente entrou sem token")
	}
	if n := countRows(t, env.AdminURL, `SELECT count(*) FROM users WHERE is_superadmin`); n != 0 {
		t.Fatalf("superadmin sem pedido: %d", n)
	}
}

func openStore(t *testing.T, ctx context.Context, urls db.RoleURLs) (*db.Store, func()) {
	t.Helper()
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform})
	if err != nil {
		t.Fatal(err)
	}
	return db.NewStore(pools.App, pools.Platform), pools.Close
}

func countRows(t *testing.T, adminURL, query string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
