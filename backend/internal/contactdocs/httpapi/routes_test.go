package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/contactdocs"
	"chatisp/internal/contactdocs/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/testdb"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	cpfDigits      = "52998224725"
	cnpjDigits     = "11222333000181"
	otherCPFDigits = "39053344705"
)

func TestContactDocuments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
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
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	logs := &bytes.Buffer{}
	logger := logging.New("debug", logs)
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	svc := contactdocs.New(store, ring)
	tenant, tenantB := uuid.New(), uuid.New()
	exec(`INSERT INTO tenants (id, timezone, status) VALUES ($1,'America/Sao_Paulo','active'), ($2,'America/Sao_Paulo','active')`, tenant, tenantB)
	adminA := newActor(t, ctx, exec, store, tenant, "admin")
	supervisor := newActor(t, ctx, exec, store, tenant, "supervisor")
	agentA := newActor(t, ctx, exec, store, tenant, "agent")
	agentB := newActor(t, ctx, exec, store, tenant, "agent")
	adminB := newActor(t, ctx, exec, store, tenantB, "admin")

	connectorA, connectorB := uuid.New(), uuid.New()
	inboxA, inboxB := uuid.New(), uuid.New()
	sectorA, sectorB := uuid.New(), uuid.New()
	exec(`INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin) VALUES ($1,$2,'meta_cloud',$3), ($4,$2,'meta_cloud',$5)`, connectorA, tenant, connectorA.String(), connectorB, connectorB.String())
	exec(`INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,'Caixa A'), ($4,$2,$5,'Caixa B')`, inboxA, tenant, connectorA, inboxB, connectorB)
	exec(`INSERT INTO sectors (tenant_id, id, name) VALUES ($1,$2,'Setor A'), ($1,$3,'Setor B')`, tenant, sectorA, sectorB)
	exec(`INSERT INTO sector_memberships (tenant_id, sector_id, membership_id) VALUES ($1,$2,$3), ($1,$4,$5)`, tenant, sectorA, agentA.p.MembershipID, sectorB, agentB.p.MembershipID)
	exec(`INSERT INTO sector_inboxes (tenant_id, sector_id, inbox_id) VALUES ($1,$2,$3), ($1,$4,$5)`, tenant, sectorA, inboxA, sectorB, inboxB)
	exec(`INSERT INTO inbox_sector_defaults (tenant_id, inbox_id, sector_id) VALUES ($1,$2,$3), ($1,$4,$5)`, tenant, inboxA, sectorA, inboxB, sectorB)

	sectorContact, otherContact, manualContact := uuid.New(), uuid.New(), uuid.New()
	foreignContact := uuid.New()
	exec(`INSERT INTO contacts (id, tenant_id, name, created_by_membership_id) VALUES ($1,$2,'Titular B',$3), ($4,$2,'Outro B',$3), ($5,$2,'Manual',$6), ($7,$8,'Outro tenant',$9)`, sectorContact, tenant, adminA.p.MembershipID, otherContact, manualContact, agentA.p.MembershipID, foreignContact, tenantB, adminB.p.MembershipID)
	identityB, identityOther := uuid.New(), uuid.New()
	exec(`INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,'ext-b'), ($5,$2,$6,$4,'ext-other')`, identityB, tenant, sectorContact, connectorB, identityOther, otherContact)
	exec(`INSERT INTO conversations (id, tenant_id, inbox_id, contact_id, contact_identity_id, status, protocol, protocol_period, cycle_number) VALUES ($1,$2,$3,$4,$5,'open','p-b','202611',1), ($6,$2,$3,$7,$8,'open','p-other','202611',1)`, uuid.New(), tenant, inboxB, sectorContact, identityB, uuid.New(), otherContact, identityOther)

	if view, err := svc.Masked(ctx, adminA.p, sectorContact); err != nil || view.Present || view.Version != 0 || view.Mask != "" {
		t.Fatalf("ausente %+v %v", view, err)
	}
	if _, err := svc.Masked(ctx, agentA.p, sectorContact); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("outro setor: %v", err)
	}
	if view, err := svc.Masked(ctx, agentB.p, sectorContact); err != nil || view.Present {
		t.Fatalf("agente do setor %+v %v", view, err)
	}
	if _, err := svc.Reveal(ctx, supervisor.p, sectorContact); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("revelar ausência: %v", err)
	}

	cmd := contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 0, Document: "529.982.247-25"}
	view, err := svc.Set(ctx, supervisor.p, sectorContact, cmd)
	if err != nil || !view.Present || view.Kind != "cpf" || view.Mask != "***.***.***-**" || view.Version != 1 || strings.Contains(view.Mask, cpfDigits) {
		t.Fatalf("set %+v %v", view, err)
	}
	replay, err := svc.Set(ctx, supervisor.p, sectorContact, cmd)
	if err != nil || replay != view {
		t.Fatalf("replay %+v %v", replay, err)
	}
	stale := cmd
	stale.RequestID = uuid.New()
	if _, err = svc.Set(ctx, supervisor.p, sectorContact, stale); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("versão: %v", err)
	}
	changed := cmd
	changed.Document = cnpjDigits
	if _, err = svc.Set(ctx, supervisor.p, sectorContact, changed); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("idempotência divergente: %v", err)
	}
	if _, err = svc.Set(ctx, supervisor.p, otherContact, contactdocs.Command{RequestID: uuid.New(), Document: cpfDigits}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("hmac duplicado: %v", err)
	}
	if _, err = svc.Masked(ctx, agentA.p, sectorContact); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("busca de setor negada: %v", err)
	}
	if _, err = svc.FindByDocumentHMAC(ctx, agentA.p, cpfDigits); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("hmac de outro setor: %v", err)
	}
	opened, err := svc.Reveal(ctx, agentB.p, sectorContact)
	if !errors.Is(err, contactdocs.ErrForbidden) || opened.Document != "" {
		t.Fatalf("revelação do agente %+v %v", opened, err)
	}
	opened, err = svc.Reveal(ctx, supervisor.p, sectorContact)
	if err != nil || opened.Document != cpfDigits || opened.Kind != "cpf" || opened.Version != 1 {
		t.Fatalf("revelação %+v %v", opened.Kind, err)
	}
	match, err := svc.FindByDocumentHMAC(ctx, agentB.p, "529.982.247-25")
	if err != nil || match.ContactID != sectorContact || match.Mask != view.Mask || strings.Contains(match.Mask, "529") {
		t.Fatalf("busca %+v %v", match, err)
	}
	if _, err = svc.FindByDocumentHMAC(ctx, adminB.p, cpfDigits); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("hmac entre tenants: %v", err)
	}

	manual, err := svc.Set(ctx, adminA.p, manualContact, contactdocs.Command{RequestID: uuid.New(), Document: "11.222.333/0001-81"})
	if err != nil || manual.Kind != "cnpj" || manual.Mask != "**.***.***/****-**" {
		t.Fatalf("manual %+v %v", manual, err)
	}
	if _, err = svc.Reveal(ctx, agentA.p, manualContact); !errors.Is(err, contactdocs.ErrForbidden) {
		t.Fatalf("autor não revela: %v", err)
	}
	if _, err = svc.Set(ctx, agentA.p, manualContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 1, Document: cnpjDigits}); !errors.Is(err, contactdocs.ErrForbidden) {
		t.Fatalf("autor não grava: %v", err)
	}
	if found, err := svc.FindByDocumentHMAC(ctx, agentA.p, cnpjDigits); err != nil || found.ContactID != manualContact {
		t.Fatalf("autor busca %+v %v", found, err)
	}
	if _, err = svc.FindByDocumentHMAC(ctx, supervisor.p, cnpjDigits); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("supervisor no manual alheio: %v", err)
	}
	foreign, err := svc.Set(ctx, adminB.p, foreignContact, contactdocs.Command{RequestID: uuid.New(), Document: cpfDigits})
	if err != nil || foreign.Version != 1 {
		t.Fatalf("outro tenant %+v %v", foreign, err)
	}
	again, err := svc.FindByDocumentHMAC(ctx, supervisor.p, cpfDigits)
	if err != nil || again.ContactID != sectorContact {
		t.Fatalf("isolamento da busca %+v %v", again, err)
	}

	var same bool
	if err = admin.QueryRow(ctx, `SELECT (SELECT document_hmac FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2) = (SELECT document_hmac FROM contact_documents WHERE tenant_id=$3 AND contact_id=$4)`, tenant, sectorContact, tenantB, foreignContact).Scan(&same); err != nil || same {
		t.Fatalf("hmac igual entre tenants: %v %v", same, err)
	}
	var version string
	var forced bool
	if err = admin.QueryRow(ctx, `SELECT key_version FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2`, tenant, sectorContact).Scan(&version); err != nil || version != "v1" {
		t.Fatalf("versão da chave %s %v", version, err)
	}
	if err = admin.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='contact_documents'`).Scan(&forced); err != nil || !forced {
		t.Fatalf("rls force %v %v", forced, err)
	}
	for _, table := range []string{"contact_documents", "contact_document_peppers", "contact_document_commands"} {
		var n int
		if err = pools.Owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("owner %s %d %v", table, n, err)
		}
		if err = pools.App.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("app sem tenant %s %d %v", table, n, err)
		}
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM contact_documents WHERE tenant_id=$1`, tenantB).Scan(&n)
		if err != nil || n != 0 {
			t.Fatalf("vazamento %d %v", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cleared, err := svc.Clear(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 1})
	if err != nil || cleared.Present || cleared.Version != 2 {
		t.Fatalf("clear %+v %v", cleared, err)
	}
	var clearedVersion int64
	var clearedCipher []byte
	if err = admin.QueryRow(ctx, `SELECT version, ciphertext FROM contact_documents WHERE tenant_id=$1 AND contact_id=$2`, tenant, sectorContact).Scan(&clearedVersion, &clearedCipher); err != nil || clearedVersion != 2 || clearedCipher != nil {
		t.Fatalf("ausência monotônica version=%d cipher=%v err=%v", clearedVersion, clearedCipher, err)
	}
	if masked, err := svc.Masked(ctx, supervisor.p, sectorContact); err != nil || masked.Present || masked.Version != 2 {
		t.Fatalf("leitura da ausência %+v %v", masked, err)
	}
	if _, err = svc.FindByDocumentHMAC(ctx, supervisor.p, cpfDigits); !errors.Is(err, contactdocs.ErrNotFound) {
		t.Fatalf("busca após limpar: %v", err)
	}
	if _, err = svc.Set(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 0, Document: otherCPFDigits}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("set com versão zerada após clear: %v", err)
	}
	if _, err = svc.Set(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 1, Document: otherCPFDigits}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("escrita obsoleta após clear: %v", err)
	}
	recreated, err := svc.Set(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 2, Document: otherCPFDigits})
	if err != nil || !recreated.Present || recreated.Kind != "cpf" || recreated.Version != 3 {
		t.Fatalf("recriação %+v %v", recreated, err)
	}
	if _, err = svc.Set(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 1, Document: cnpjDigits}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("ciclo ABA set: %v", err)
	}
	if _, err = svc.Clear(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 1}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("ciclo ABA clear: %v", err)
	}
	if _, err = svc.Clear(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 2}); !errors.Is(err, contactdocs.ErrConflict) {
		t.Fatalf("versão intermediária: %v", err)
	}
	if opened, err = svc.Reveal(ctx, supervisor.p, sectorContact); err != nil || opened.Document != otherCPFDigits || opened.Version != 3 {
		t.Fatalf("documento recriado %+v %v", opened.Version, err)
	}
	cleared, err = svc.Clear(ctx, supervisor.p, sectorContact, contactdocs.Command{RequestID: uuid.New(), ExpectedVersion: 3})
	if err != nil || cleared.Present || cleared.Version != 4 {
		t.Fatalf("segundo clear %+v %v", cleared, err)
	}
	var audits int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='contact_document.set' AND target_id=$2 AND result='success'`, tenant, sectorContact).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("auditoria de replay %d %v", audits, err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='contact_document.reveal' AND result='denied' AND target_id=$2`, tenant, sectorContact).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("auditoria negada %d %v", audits, err)
	}

	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	httpapi.Register(humachi.New(router, cfg), httpapi.Dependencies{Auth: identity, Documents: svc, TrustedOrigins: []string{"https://panel.example"}})
	path := "/api/panel/contacts/" + sectorContact.String() + "/document"
	var safeBodies []string
	request := func(method, path, session, origin, csrf string, body any, want int) (int, []byte, string) {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: session})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			msg := strings.ReplaceAll(rec.Body.String(), cpfDigits, "[cpf]")
			msg = strings.ReplaceAll(msg, cnpjDigits, "[cnpj]")
			msg = strings.ReplaceAll(msg, otherCPFDigits, "[cpf2]")
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, msg)
		}
		return rec.Code, rec.Body.Bytes(), rec.Header().Get("Cache-Control")
	}
	if _, _, cache := request(http.MethodGet, path, "", "", "", nil, http.StatusUnauthorized); cache != "" && cache != "no-store" {
		t.Fatal(cache)
	}
	_, body, cache := request(http.MethodGet, path, supervisor.token, "", "", nil, http.StatusOK)
	safeBodies = append(safeBodies, string(body))
	if cache != "no-store" || strings.Contains(string(body), cpfDigits) {
		t.Fatal("máscara http")
	}
	put := map[string]any{"request_id": uuid.New(), "expected_version": cleared.Version, "document": "529.982.247-25"}
	request(http.MethodPut, path, supervisor.token, "https://evil.example", supervisor.csrf, put, http.StatusForbidden)
	request(http.MethodPut, path, supervisor.token, "https://panel.example", "errado", put, http.StatusForbidden)
	_, body, cache = request(http.MethodPut, path, supervisor.token, "https://panel.example", supervisor.csrf, put, http.StatusOK)
	safeBodies = append(safeBodies, string(body))
	if cache != "no-store" || strings.Contains(string(body), cpfDigits) {
		t.Fatal("put vazou")
	}
	_, body, _ = request(http.MethodPut, path, supervisor.token, "https://panel.example", supervisor.csrf, put, http.StatusOK)
	safeBodies = append(safeBodies, string(body))
	put["request_id"] = uuid.New()
	_, body, _ = request(http.MethodPut, path, supervisor.token, "https://panel.example", supervisor.csrf, put, http.StatusConflict)
	safeBodies = append(safeBodies, string(body))
	_, bad, _ := request(http.MethodPut, path, supervisor.token, "https://panel.example", supervisor.csrf, map[string]any{"request_id": uuid.New(), "expected_version": 1, "document": cpfDigits + "xx"}, http.StatusUnprocessableEntity)
	safeBodies = append(safeBodies, string(bad))
	manualPath := "/api/panel/contacts/" + manualContact.String() + "/document"
	_, denied, _ := request(http.MethodPost, manualPath+"/reveal", agentA.token, "https://panel.example", agentA.csrf, nil, http.StatusForbidden)
	safeBodies = append(safeBodies, string(denied))
	_, revealed, cache := request(http.MethodPost, path+"/reveal", supervisor.token, "https://panel.example", supervisor.csrf, nil, http.StatusOK)
	if cache != "no-store" || !strings.Contains(string(revealed), cpfDigits) {
		t.Fatal("revelação autorizada")
	}
	_, searched, cache := request(http.MethodPost, "/api/panel/contacts/document-search", agentB.token, "https://panel.example", agentB.csrf, map[string]string{"document": cpfDigits}, http.StatusOK)
	safeBodies = append(safeBodies, string(searched))
	if cache != "no-store" || strings.Contains(string(searched), cpfDigits) {
		t.Fatal("busca http")
	}
	_, missing, _ := request(http.MethodPost, "/api/panel/contacts/document-search", agentA.token, "https://panel.example", agentA.csrf, map[string]string{"document": cpfDigits}, http.StatusNotFound)
	safeBodies = append(safeBodies, string(missing))
	corpus := strings.Join(append(safeBodies, logs.String(), errText(contactdocs.ErrInvalid), errText(contactdocs.ErrConflict), errText(contactdocs.ErrForbidden), errText(contactdocs.ErrUnavailable)), "\n")
	var leaked int
	for _, secret := range []string{cpfDigits, cnpjDigits, otherCPFDigits} {
		if strings.Contains(corpus, secret) {
			t.Fatal("documento em claro no log ou erro")
		}
		if err = admin.QueryRow(ctx, `
			SELECT count(*) FROM (
				SELECT response::text AS blob FROM contact_document_commands
				UNION ALL SELECT action || result || coalesce(correlation_id, '') FROM audit_logs
				UNION ALL SELECT coalesce(payload::text, '') || event_type FROM realtime_events
				UNION ALL SELECT encode(ciphertext, 'escape') FROM contact_documents
				UNION ALL SELECT encode(document_hmac, 'escape') FROM contact_documents
				UNION ALL SELECT encode(ciphertext, 'escape') FROM contact_document_peppers
				UNION ALL SELECT encode(request_hmac, 'escape') FROM contact_document_commands
			) s WHERE position($1 in blob) > 0`, secret).Scan(&leaked); err != nil || leaked != 0 {
			t.Fatalf("vazamento persistido %d %v", leaked, err)
		}
	}
	exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, tenant)
	_, suspended, _ := request(http.MethodGet, path, supervisor.token, "", "", nil, http.StatusForbidden)
	if strings.Contains(string(suspended), cpfDigits) {
		t.Fatal("suspensão ecoou o documento")
	}
	if _, err = svc.Reveal(ctx, supervisor.p, sectorContact); !errors.Is(err, contactdocs.ErrForbidden) {
		t.Fatalf("suspenso: %v", err)
	}
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)
	exec(`UPDATE contact_documents SET ciphertext = decode(repeat('00', 40), 'hex') WHERE tenant_id=$1 AND contact_id=$2`, tenant, sectorContact)
	if _, err = svc.Reveal(ctx, supervisor.p, sectorContact); !errors.Is(err, contactdocs.ErrUnavailable) || strings.Contains(err.Error(), cpfDigits) {
		t.Fatalf("cifra inválida: %v", err)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newActor(t *testing.T, ctx context.Context, exec func(string, ...any), store *db.Store, tenant uuid.UUID, role string) struct {
	p           auth.Principal
	token, csrf string
} {
	t.Helper()
	user, member := uuid.New(), uuid.New()
	exec(`INSERT INTO users (id, email, status, totp_ciphertext, totp_key_version, totp_aad_tenant, totp_confirmed_at) VALUES ($1,$2,'active',$3,'v1',$4,now())`, user, user.String()+"@example.test", bytes.Repeat([]byte{7}, 32), tenant)
	exec(`INSERT INTO memberships (id, tenant_id, user_id, role, status) VALUES ($1,$2,$3,$4,'active')`, member, tenant, user, role)
	token, csrf := uuid.NewString(), uuid.NewString()
	now := time.Now()
	if err := store.WithAuth(ctx, func(ctx context.Context) error {
		return store.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	return struct {
		p           auth.Principal
		token, csrf string
	}{p: auth.Principal{UserID: user, TenantID: tenant, MembershipID: member, Role: role}, token: token, csrf: csrf}
}
