package platformusage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/testdb"
)

const (
	secretBody       = "corpo-mensagem-unico-nao-exibir"
	secretCPF        = "cpf-52998224725"
	secretProtocol   = "PROTOCOLO-UNICO-NAO-EXIBIR"
	secretSummary    = "resumo-conversa-unico-nao-exibir"
	secretContact    = "contato-unico-nao-exibir"
	secretInbox      = "caixa-unica-nao-exibir"
	secretAttachment = "anexo-unico-nao-exibir"
	panelOrigin      = "https://painel.chatisp.test"
	password         = "senha-correta-10"
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		os.Exit(1)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		os.Exit(1)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		os.Exit(1)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		os.Exit(1)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		os.Exit(1)
	}
	defer pools.Close()
	usageEnv = env
	usageStore = db.NewStore(pools.App, pools.Platform)
	os.Exit(m.Run())
}

var (
	usageEnv   *testdb.Env
	usageStore *db.Store
)

func TestUsageHidesMessageBodyAndRejectsProviderRoles(t *testing.T) {
	ctx := context.Background()
	superTenant := uuid.New()
	activeTenant := uuid.New()
	suspendedTenant := uuid.New()
	if err := usageStore.WithPlatform(ctx, func(ctx context.Context) error {
		for _, tenant := range []db.NewTenant{
			{ID: superTenant, Timezone: "America/Sao_Paulo", Status: "active"},
			{ID: activeTenant, Timezone: "America/Fortaleza", Status: "active"},
			{ID: suspendedTenant, Timezone: "America/Manaus", Status: "suspended"},
		} {
			if _, err := usageStore.CreateTenant(ctx, tenant); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	super := seedMember(t, superTenant, "admin", "active")
	markSuperadmin(t, super.user)
	admin := seedMember(t, activeTenant, "admin", "active")
	supervisor := seedMember(t, activeTenant, "supervisor", "active")
	agent := seedMember(t, activeTenant, "agent", "active")
	seedMember(t, activeTenant, "agent", "disabled")
	seedMember(t, activeTenant, "supervisor", "active")

	at := time.Date(2026, 11, 3, 15, 4, 5, 0, time.UTC)
	seedAttendance(t, activeTenant, at)

	if err := usageStore.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var body string
		scanErr := tx.QueryRow(ctx, `SELECT body FROM messages LIMIT 1`).Scan(&body)
		if scanErr == nil {
			return fmt.Errorf("plataforma leu o corpo")
		}
		denied := strings.ToLower(scanErr.Error())
		if !strings.Contains(denied, "permission denied") && !strings.Contains(denied, "negad") {
			return fmt.Errorf("erro inesperado ao negar corpo: %w", scanErr)
		}
		return scanErr
	}); err == nil || (!strings.Contains(strings.ToLower(err.Error()), "permission denied") && !strings.Contains(strings.ToLower(err.Error()), "negad")) {
		t.Fatalf("leitura do corpo: %v", err)
	}

	svc := newIdentity(t)
	api := httpapi.New(httpapi.Dependencies{
		Auth:           svc,
		Store:          usageStore,
		TrustedOrigins: []string{panelOrigin},
		Logger:         logging.New("error", io.Discard),
	})
	from := at.Add(-time.Minute).UTC().Format(time.RFC3339)
	to := at.Add(time.Hour).UTC().Format(time.RFC3339)
	path := "/api/platform/usage?from=" + from + "&to=" + to

	superCookie, _ := login(t, api.Handler, super.email)
	shown := call(api.Handler, http.MethodGet, path, superCookie)
	if shown.Code != http.StatusOK {
		t.Fatalf("superadmin: %d %s", shown.Code, shown.Body.String())
	}
	raw := shown.Body.String()
	for _, secret := range []string{secretBody, secretCPF, secretProtocol, secretSummary, secretContact, secretInbox, secretAttachment, "object_key", "phone"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("resposta contém %s: %s", secret, raw)
		}
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var snap struct {
		ProvidersActive    int64 `json:"providers_active"`
		ProvidersSuspended int64 `json:"providers_suspended"`
		Providers          []struct {
			TenantID          uuid.UUID `json:"tenant_id"`
			Timezone          string    `json:"timezone"`
			Status            string    `json:"status"`
			ActiveAgents      int64     `json:"active_agents"`
			Inboxes           int64     `json:"inboxes"`
			OpenConversations int64     `json:"open_conversations"`
			Messages          int64     `json:"messages"`
			AttachmentBytes   *int64    `json:"attachment_bytes"`
		} `json:"providers"`
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	}
	if err := dec.Decode(&snap); err != nil {
		t.Fatalf("json: %v corpo %s", err, raw)
	}
	if snap.ProvidersActive != 2 || snap.ProvidersSuspended != 1 {
		t.Fatalf("provedores ativos %d suspensos %d", snap.ProvidersActive, snap.ProvidersSuspended)
	}
	var found bool
	for _, item := range snap.Providers {
		if item.TenantID != activeTenant {
			continue
		}
		found = true
		if item.Status != "active" || item.Timezone != "America/Fortaleza" || item.ActiveAgents != 1 || item.Inboxes != 1 || item.OpenConversations != 1 || item.Messages != 1 || item.AttachmentBytes == nil || *item.AttachmentBytes != 4096 {
			t.Fatalf("contagens %+v", item)
		}
	}
	if !found {
		t.Fatal("provedor ativo ausente")
	}

	var audits []db.PlatformAuditLog
	if err := usageStore.WithPlatform(ctx, func(ctx context.Context) error {
		rows, err := usageStore.ListPlatformAuditLogs(ctx)
		audits = rows
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range audits {
		if row.Action != "platform.usage.read" || row.Actor != super.user.String() {
			continue
		}
		seen++
		if row.TargetType != "platform_usage" || row.Result != "success" || row.TargetID == nil {
			t.Fatalf("auditoria %+v", row)
		}
		if strings.Contains(*row.TargetID, secretBody) || strings.Contains(*row.TargetID, secretProtocol) {
			t.Fatalf("auditoria com conteúdo: %s", *row.TargetID)
		}
	}
	if seen != 1 {
		t.Fatalf("auditorias de uso: %d", seen)
	}

	for _, member := range []seeded{admin, supervisor, agent} {
		cookie, _ := login(t, api.Handler, member.email)
		denied := call(api.Handler, http.MethodGet, path, cookie)
		if denied.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", member.role, denied.Code, denied.Body.String())
		}
		if strings.Contains(denied.Body.String(), secretBody) || strings.Contains(denied.Body.String(), secretCPF) || strings.Contains(denied.Body.String(), secretProtocol) {
			t.Fatalf("403 vazou conteúdo: %s", denied.Body.String())
		}
	}
	if err := usageStore.WithPlatform(ctx, func(ctx context.Context) error {
		rows, err := usageStore.ListPlatformAuditLogs(ctx)
		audits = rows
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seen = 0
	for _, row := range audits {
		if row.Action == "platform.usage.read" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("papel de provedor gravou auditoria de uso: %d", seen)
	}
}

type seeded struct {
	email string
	user  uuid.UUID
	role  string
}

func seedMember(t *testing.T, tenantID uuid.UUID, role, status string) seeded {
	t.Helper()
	item := seeded{user: uuid.New(), role: role}
	item.email = item.user.String() + "@example.com"
	ctx := context.Background()
	if err := usageStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := usageStore.CreateUser(ctx, db.NewUser{ID: item.user, Email: item.email, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := usageStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := usageStore.CreateMembership(ctx, db.NewMembership{
			ID: uuid.New(), UserID: item.user, Role: role, Status: status,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := usageStore.WithAuth(ctx, func(ctx context.Context) error {
		return usageStore.SetPasswordHash(ctx, item.user, hash)
	}); err != nil {
		t.Fatal(err)
	}
	return item
}

func markSuperadmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, usageEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
}

func seedAttendance(t *testing.T, tenantID uuid.UUID, at time.Time) {
	t.Helper()
	connector := uuid.New()
	inbox := uuid.New()
	sector := uuid.New()
	contact := uuid.New()
	identity := uuid.New()
	closedContact := uuid.New()
	closedIdentity := uuid.New()
	openConv := uuid.New()
	closedConv := uuid.New()
	ctx := context.Background()
	err := usageStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'disconnected', false)
		`, connector, tenantID, "origem-"+connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name)
			VALUES ($1, $2, $3, $4)
		`, inbox, tenantID, connector, secretInbox); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sectors (tenant_id, id, name, active, legacy_default)
			VALUES ($1, $2, 'Padrão', true, true)
		`, tenantID, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sector_inboxes (tenant_id, sector_id, inbox_id) VALUES ($1, $2, $3)
		`, tenantID, sector, inbox); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_sector_defaults (tenant_id, inbox_id, sector_id) VALUES ($1, $2, $3)
		`, tenantID, inbox, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, $3), ($4, $2, $3)
		`, contact, tenantID, secretContact, closedContact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5), ($6, $2, $7, $4, $8)
		`, identity, tenantID, contact, connector, "wa-"+identity.String(), closedIdentity, closedContact, "wa-"+closedIdentity.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (
				id, tenant_id, summary, inbox_id, contact_id, contact_identity_id,
				status, protocol, protocol_period, cycle_number
			) VALUES
				($1, $2, $3, $4, $5, $6, 'open', $7, '202611', 1),
				($8, $2, $3, $4, $9, $10, 'closed', $11, '202611', 1)
		`, openConv, tenantID, secretSummary, inbox, contact, identity, secretProtocol, closedConv, closedContact, closedIdentity, secretProtocol+"-F"); err != nil {
			return err
		}
		body := secretBody + " " + secretCPF
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (
				id, tenant_id, conversation_id, connector_id, direction, origin, visibility, body, ingested_at
			) VALUES
				($1, $2, $3, $4, 'inbound', 'customer', 'public', $5, $6),
				($7, $2, $3, $4, 'inbound', 'customer', 'public', $5, $8)
		`, uuid.New(), tenantID, openConv, connector, body, at, uuid.New(), at.Add(-2*time.Hour)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO attachments (
				tenant_id, id, conversation_id, request_id, direction, name, expected_bytes, object_key, expires_at
			) VALUES ($1, $2, $3, $4, 'incoming', $5, 4096, $6, $7)
		`, tenantID, uuid.New(), openConv, uuid.New(), secretAttachment, "obj-"+uuid.NewString(), at.Add(24*time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newIdentity(t *testing.T) *auth.Service {
	t.Helper()
	svc, err := auth.New(auth.Options{Store: usageStore, Mail: &mail.Fake{}, Logger: logging.New("error", io.Discard)})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func login(t *testing.T, handler http.Handler, email string) (string, string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/panel/session", bytes.NewReader(raw))
	req.Host = "painel.chatisp.test"
	req.RemoteAddr = "198.51.100.20:4242"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", panelOrigin)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body.String())
	}
	var session, csrf string
	for _, line := range rec.Result().Cookies() {
		switch line.Name {
		case auth.SessionCookieName:
			session = line.Value
		case auth.CSRFCookieName:
			csrf = line.Value
		}
	}
	if session == "" || csrf == "" {
		t.Fatal("cookies ausentes")
	}
	return session, csrf
}

func call(handler http.Handler, method, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = "painel.chatisp.test"
	req.RemoteAddr = "198.51.100.21:4242"
	req.Header.Set("Origin", panelOrigin)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
