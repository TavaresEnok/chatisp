package cannedreplies

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestClosedVariables(t *testing.T) {
	for _, s := range []string{"{{usuario}}", "{{nome_cliente.x}}", "{{nome_cliente | escape}}", "{{ nome_cliente }}", "{{nome_cliente}", "<b>oi</b>", "{x}", "{{{{nome_cliente}}}}"} {
		if _, err := expand(s, nil, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %q: %v", s, err)
		}
	}
	got, err := expand("{{nome_cliente}} {{nome_cliente}} {{protocolo}} {{nome_atendente}}", map[string]string{"nome_atendente": "{{protocolo}}<x>"}, false)
	if err != nil || got.Text != "{{nome_cliente}} {{nome_cliente}} {{protocolo}} {{protocolo}}<x>" || strings.Join(got.Missing, ",") != "nome_cliente,protocolo" {
		t.Fatalf("single pass/missing: %+v %v", got, err)
	}
	if _, err = expand("{{nome_cliente}}", nil, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("signature accepts customer")
	}
	for _, e := range []Edit{{"UPPER", "T", "x"}, {"x", " ", "x"}, {"x", "T", strings.Repeat("é", 10001)}, {"x", "T", "{{bad}}"}} {
		if validEdit(e) {
			t.Fatalf("valid: %+v", e)
		}
	}
}

func TestPersistentHTTPAndPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	svc := New(store)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	seed := func(role string, tenant uuid.UUID) auth.Principal {
		p := auth.Principal{TenantID: tenant, UserID: uuid.New(), MembershipID: uuid.New(), Role: role}
		p.Email = p.UserID.String() + "@example.com"
		must(store.WithPlatform(ctx, func(c context.Context) error {
			if tenant == uuid.Nil {
				p.TenantID = uuid.New()
				if _, e := store.CreateTenant(c, db.NewTenant{ID: p.TenantID, Timezone: "UTC", Status: "active"}); e != nil {
					return e
				}
			}
			_, e := store.CreateUser(c, db.NewUser{ID: p.UserID, Email: p.Email, Status: "active"})
			return e
		}))
		must(store.WithTenant(ctx, p.TenantID, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: p.MembershipID, UserID: p.UserID, Role: role, Status: "active"})
			return e
		}))
		return p
	}
	supervisor := seed("supervisor", uuid.Nil)
	agent := seed("agent", supervisor.TenantID)
	other := seed("admin", uuid.Nil)
	edit := Edit{Shortcode: "reiniciar", Title: "Reiniciar", Content: "Olá {{nome_cliente}} / {{nome_atendente}} / {{protocolo}}"}
	reply, err := svc.Create(ctx, supervisor, edit)
	must(err)
	if _, err = svc.Create(ctx, supervisor, edit); !errors.Is(err, ErrConflict) {
		t.Fatalf("unique: %v", err)
	}
	if _, err = svc.Create(ctx, agent, edit); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent write: %v", err)
	}
	_, err = svc.Create(ctx, other, edit)
	must(err) // shortcode unique only within tenant
	page, err := New(db.NewStore(pools.App, pools.Platform)).List(ctx, agent, "reinic", 1, 0)
	must(err)
	if len(page.Replies) != 1 || page.Replies[0].ID != reply.ID {
		t.Fatalf("persistent list: %+v", page)
	}
	_, err = svc.Create(ctx, supervisor, Edit{"segundo", "Outra", "Texto"})
	must(err)
	page, err = svc.List(ctx, agent, "", 1, 0)
	must(err)
	if !page.HasMore || len(page.Replies) != 1 {
		t.Fatalf("pagination: %+v", page)
	}
	page, err = svc.List(ctx, agent, "%", 25, 0)
	must(err)
	if len(page.Replies) != 0 {
		t.Fatal("search wildcard")
	}
	if _, err = svc.Update(ctx, other, reply.ID, 1, edit); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant update: %v", err)
	}
	updated, err := svc.Update(ctx, supervisor, reply.ID, 1, edit)
	must(err)
	if updated.Version != 2 {
		t.Fatal("version")
	}
	if err = svc.Delete(ctx, supervisor, reply.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if _, err = svc.Update(ctx, supervisor, reply.ID, 1, edit); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}

	// Build a real tenant conversation and its M3A sector bindings.
	inbox, conversation, contact, connector, identity, sector := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	must(store.WithTenantTx(ctx, supervisor.TenantID, func(c context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, supervisor.TenantID, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, []any{inbox, supervisor.TenantID, connector}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Ana')`, []any{contact, supervisor.TenantID}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, supervisor.TenantID, contact, connector, identity.String()}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, []any{supervisor.TenantID, sector}},
			{`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, []any{supervisor.TenantID, sector, inbox}},
			{`INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3)`, []any{supervisor.TenantID, sector, agent.MembershipID}},
			{`INSERT INTO conversations(id,tenant_id,summary,inbox_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number,sector_id) VALUES($1,$2,'',$3,$4,$5,'P-123','202610',1,$6)`, []any{conversation, supervisor.TenantID, inbox, contact, identity, sector}},
		} {
			if _, e := tx.Exec(c, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	}))
	draft, err := svc.Render(ctx, agent, reply.ID, inbox, conversation)
	must(err)
	if draft.Text != "Olá Ana / "+agent.Email+" / P-123" || len(draft.Missing) != 0 {
		t.Fatalf("draft: %+v", draft)
	}
	if _, err = svc.Render(ctx, other, reply.ID, inbox, conversation); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross render: %v", err)
	}
	// Revocation does not revoke the tenant catalog, but must revoke conversation render.
	must(store.WithTenantTx(ctx, agent.TenantID, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, agent.TenantID, agent.MembershipID)
		return e
	}))
	if _, err = svc.Render(ctx, agent, reply.ID, inbox, conversation); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked render: %v", err)
	}
	_, err = svc.List(ctx, agent, "", 25, 0)
	must(err)

	config, err := svc.Signature(ctx, agent)
	must(err)
	if config.Enabled || config.Version != 0 {
		t.Fatalf("default: %+v", config)
	}
	config.Enabled = true
	config, err = svc.SetSignature(ctx, supervisor, config)
	must(err)
	if _, err = svc.SetSignature(ctx, agent, config); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent config: %v", err)
	}
	if _, err = svc.SetSignature(ctx, supervisor, Signature{Prefix: "{{nome_atendente}}: ", Version: 0}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale config: %v", err)
	}
	acceptance := uuid.New()
	accept := func(id uuid.UUID, text string) (string, error) {
		var v string
		e := store.WithTenantTx(ctx, supervisor.TenantID, func(c context.Context, tx pgx.Tx) error {
			var e error
			v, e = AcceptPublicHumanTx(c, tx, supervisor, id, text)
			return e
		})
		return v, e
	}
	got, err := accept(acceptance, "Oi")
	must(err)
	if got != supervisor.Email+": Oi" {
		t.Fatalf("signature: %q", got)
	}
	config.Enabled = false
	_, err = svc.SetSignature(ctx, supervisor, config)
	must(err)
	got, err = accept(acceptance, "Oi")
	must(err)
	if got != supervisor.Email+": Oi" {
		t.Fatal("replay changed")
	}
	if _, err = accept(acceptance, "Mudou"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	got, err = accept(uuid.New(), "Oi")
	must(err)
	if got != "Oi" {
		t.Fatal("disabled signature")
	}
	// Rollback leaves no receipt, so a later acceptance may use a different original.
	rolled := uuid.New()
	sentinel := errors.New("rollback")
	err = store.WithTenantTx(ctx, supervisor.TenantID, func(c context.Context, tx pgx.Tx) error {
		if _, e := AcceptPublicHumanTx(c, tx, supervisor, rolled, "Original"); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	_, err = accept(rolled, "Novo")
	must(err)
	// Concurrent same-key acceptance must produce exactly one stable receipt.
	concurrent := uuid.New()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			v, e := accept(concurrent, "Único")
			if e == nil && v != "Único" {
				e = errors.New("unstable acceptance")
			}
			results <- e
		}()
	}
	must(<-results)
	must(<-results)

	// Actual HTTP registration, sessions, CSRF and Origin; no fake auth adapter.
	authService, err := auth.New(auth.Options{Store: store})
	must(err)
	session := func(p auth.Principal) (string, string) {
		token, csrf := uuid.NewString(), uuid.NewString()
		now := time.Now().UTC()
		must(store.WithAuth(ctx, func(c context.Context) error {
			return store.InsertSession(c, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(token), CSRFHash: auth.HashSecret(csrf), UserID: p.UserID, TenantID: p.TenantID, MembershipID: p.MembershipID, Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)})
		}))
		return token, csrf
	}
	st, sc := session(supervisor)
	at, ac := session(agent)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("canned-test", "1"))
	Register(api, Dependencies{Auth: authService, CannedReplies: svc, TrustedOrigins: []string{"https://panel.example"}})
	request := func(method, path, body, token, csrf, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: token})
		}
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d wanted %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	base := "/api/panel/canned-replies"
	request("GET", base, "", "", "", "", 401)
	request("GET", base, "", at, "", "", 200)
	payload := `{"shortcode":"http","title":"HTTP","content":"Oi {{nome_cliente}}"}`
	request("POST", base, payload, st, sc, "https://evil.example", 403)
	request("POST", base, payload, st, "bad", "https://panel.example", 403)
	request("POST", base, payload, at, ac, "https://panel.example", 403)
	w := request("POST", base, payload, st, sc, "https://panel.example", 201)
	var created Reply
	must(json.Unmarshal(w.Body.Bytes(), &created))
	if created.ID == uuid.Nil {
		t.Fatal("HTTP id")
	}
	request("PUT", base+"/"+created.ID.String(), `{"shortcode":"http","title":"Changed","content":"Texto","version":1}`, st, sc, "https://panel.example", 200)
	request("DELETE", base+"/"+created.ID.String()+"?version=1", "", st, sc, "https://panel.example", 409)
	request("DELETE", base+"/"+created.ID.String()+"?version=2", "", st, sc, "https://panel.example", 200)
	request("POST", base, `{"shortcode":"bad","title":"Bad","content":"{{arbitrary}}"}`, st, sc, "https://panel.example", 422)
	request("GET", "/api/panel/attendant-signature", "", at, "", "", 200)
	request("PUT", "/api/panel/attendant-signature", `{"enabled":true,"prefix":"{{nome_atendente}}: ","version":2}`, st, sc, "https://panel.example", 200)
	renderPayload := `{"inbox_id":"` + inbox.String() + `","conversation_id":"` + conversation.String() + `"}`
	request("POST", base+"/"+reply.ID.String()+"/render", renderPayload, st, sc, "https://panel.example", 200)
	request("POST", base+"/"+reply.ID.String()+"/render", renderPayload, at, ac, "https://panel.example", 403)

	// FORCE RLS holds even without application filtering and disallows cross writes.
	must(store.WithTenantTx(ctx, other.TenantID, func(c context.Context, tx pgx.Tx) error {
		var n int
		must(tx.QueryRow(c, `SELECT count(*) FROM canned_replies WHERE id=$1`, reply.ID).Scan(&n))
		if n != 0 {
			t.Fatal("RLS leak")
		}
		return nil
	}))
	err = store.WithTenantTx(ctx, other.TenantID, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `INSERT INTO canned_replies(tenant_id,id,shortcode,title,content) VALUES($1,$2,'cross','cross','cross')`, supervisor.TenantID, uuid.New())
		return e
	})
	if err == nil {
		t.Fatal("RLS write accepted")
	}
	must(store.WithTenantTx(ctx, supervisor.TenantID, func(c context.Context, tx pgx.Tx) error {
		var n int
		must(tx.QueryRow(c, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action LIKE 'canned.%'`, supervisor.TenantID).Scan(&n))
		if n != 6 {
			t.Fatalf("CRUD audits %d", n)
		}
		must(tx.QueryRow(c, `SELECT count(*) FROM messages WHERE tenant_id=$1`, supervisor.TenantID).Scan(&n))
		if n != 0 {
			t.Fatal("render sent message")
		}
		must(tx.QueryRow(c, `SELECT count(*) FROM pg_class WHERE relname IN ('canned_replies','attendant_signature_settings','attendant_signature_receipts') AND relrowsecurity AND relforcerowsecurity`).Scan(&n))
		if n != 3 {
			t.Fatal("FORCE RLS missing")
		}
		return nil
	}))
	forged := agent
	forged.Role = "admin"
	if _, err = svc.Create(ctx, forged, edit); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forged role: %v", err)
	}
	must(store.WithTenantTx(ctx, agent.TenantID, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET status='disabled' WHERE tenant_id=$1 AND id=$2`, agent.TenantID, agent.MembershipID)
		return e
	}))
	if _, err = svc.List(ctx, agent, "", 25, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled member: %v", err)
	}
	request("GET", base, "", at, "", "", 403)
	must(store.WithPlatform(ctx, func(c context.Context) error {
		_, e := store.SetTenantStatus(c, supervisor.TenantID, "suspended")
		return e
	}))
	if _, err = svc.Create(ctx, supervisor, edit); !errors.Is(err, ErrForbidden) {
		t.Fatalf("suspended tenant: %v", err)
	}
	request("GET", base, "", st, "", "", 403)
}
