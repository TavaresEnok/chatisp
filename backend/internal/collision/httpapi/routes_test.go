package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/collision"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCollisionHTTPAndSessions(t *testing.T) {
	ctx := context.Background()
	env, e := testdb.Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer env.Close()
	secrets, e := db.NewRoleSecrets()
	if e != nil {
		t.Fatal(e)
	}
	urls, e := db.Bootstrap(ctx, env.AdminURL, secrets)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := db.MigrationsDir()
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ApplyMigrations(ctx, urls.Owner, dir); e != nil {
		t.Fatal(e)
	}
	pools, e := db.OpenPools(ctx, urls)
	if e != nil {
		t.Fatal(e)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: store, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	tenant, user, member, inbox, connector, sector, conversation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	contact, contactIdentity := uuid.New(), uuid.New()
	if e = store.WithPlatform(ctx, func(c context.Context) error {
		if _, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
			return e
		}
		_, e := store.CreateUser(c, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := store.CreateMembership(c, db.NewMembership{ID: member, UserID: user, Role: "agent", Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		queries := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Teste')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, member}},
			{`INSERT INTO contacts(id,tenant_id) VALUES($1,$2)`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{contactIdentity, tenant, contact, connector, contactIdentity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'P-1','202610',1)`, []any{conversation, tenant, inbox, sector, contact, contactIdentity}},
		}
		for _, q := range queries {
			if _, e := tx.Exec(c, q.q, q.a...); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	type actor struct {
		token, csrf string
		session     uuid.UUID
	}
	newSession := func() actor {
		a := actor{uuid.NewString(), uuid.NewString(), uuid.New()}
		if e := store.WithAuth(ctx, func(c context.Context) error {
			return store.InsertSession(c, db.NewSession{ID: a.session, TokenHash: auth.HashSecret(a.token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)})
		}); e != nil {
			t.Fatal(e)
		}
		return a
	}
	a, b := newSession(), newSession()
	svc := &collision.Service{DB: store, Now: func() time.Time { return now }}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{identity, svc, []string{"https://panel.example"}})
	path := fmt.Sprintf("/api/panel/inboxes/%s/conversations/%s/collision", inbox, conversation)
	request := func(method, path, body string, a actor, origin, csrf string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: a.token})
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %d want %d: %s", method, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	tab1, tab2 := uuid.New(), uuid.New()
	body := func(tab uuid.UUID, typing bool) string {
		return fmt.Sprintf(`{"tab_id":"%s","typing":%t}`, tab, typing)
	}
	request("POST", path, body(tab1, true), a, "https://bad", a.csrf, 403)
	request("POST", path, body(tab1, true), a, "https://panel.example", "bad", 403)
	request("POST", path, body(tab1, true), a, "https://panel.example", a.csrf, 204)
	request("POST", path, body(tab2, false), b, "https://panel.example", b.csrf, 204)
	got := request("GET", path, "", b, "", "", 200)
	if !strings.Contains(got, `"typing":true`) || strings.Contains(got, a.session.String()) {
		t.Fatal(got)
	}
	request("DELETE", path+"/"+tab1.String(), "", b, "https://panel.example", b.csrf, 204)
	got = request("GET", path, "", b, "", "", 200)
	if !strings.Contains(got, `"typing":true`) {
		t.Fatal(got)
	}
	admin, e := pgx.Connect(ctx, env.AdminURL)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	// A different operator lists while a password/MFA reset holds the target
	// user's lock, then revokes its sessions in the same transaction.
	otherUser, otherMember, otherSession := uuid.New(), uuid.New(), uuid.New()
	if e = store.WithPlatform(ctx, func(c context.Context) error {
		_, err := store.CreateUser(c, db.NewUser{ID: otherUser, Email: otherUser.String() + "@example.com", Status: "active"})
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, err := store.CreateMembership(c, db.NewMembership{ID: otherMember, UserID: otherUser, Role: "agent", Status: "active"})
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(c, `INSERT INTO sector_memberships VALUES($1,$2,$3)`, tenant, sector, otherMember)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithAuth(ctx, func(c context.Context) error {
		return store.InsertSession(c, db.NewSession{ID: otherSession, TokenHash: auth.HashSecret(uuid.NewString()), UserID: otherUser, TenantID: tenant, MembershipID: otherMember, CSRFHash: auth.HashSecret(uuid.NewString()), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)})
	}); e != nil {
		t.Fatal(e)
	}
	reset, e := admin.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer reset.Rollback(ctx)
	if _, e = reset.Exec(ctx, `UPDATE users SET status=status WHERE id=$1`, user); e != nil {
		t.Fatal(e)
	}
	var resetPID int32
	if e = reset.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&resetPID); e != nil {
		t.Fatal(e)
	}
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type listResult struct {
		indicators []collision.Indicator
		err        error
	}
	result := make(chan listResult, 1)
	go func() {
		indicators, err := svc.List(testCtx, auth.Principal{SessionID: otherSession, UserID: otherUser, TenantID: tenant, MembershipID: otherMember, Role: "agent"}, inbox, conversation)
		result <- listResult{indicators, err}
	}()
	for {
		var blocked bool
		if e = reset.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, resetPID).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		select {
		case r := <-result:
			t.Fatalf("list finished before reset released user lock: %v", r.err)
		case <-testCtx.Done():
			t.Fatal("list did not wait for reset user lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, e = reset.Exec(testCtx, `UPDATE sessions SET revoked_at=$1 WHERE user_id=$2`, now, user); e != nil {
		t.Fatalf("reset session revocation: %v", e)
	}
	if e = reset.Commit(testCtx); e != nil {
		t.Fatal(e)
	}
	select {
	case r := <-result:
		if r.err != nil || len(r.indicators) != 0 {
			t.Fatalf("list after reset: %+v, %v", r.indicators, r.err)
		}
	case <-testCtx.Done():
		t.Fatal("list did not finish after reset")
	}
	// Restore the second session for the remaining independent-session checks.
	if _, e = admin.Exec(ctx, `UPDATE sessions SET revoked_at=NULL WHERE id=$1`, b.session); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec(ctx, `UPDATE sessions SET revoked_at=$1 WHERE id=$2`, now, a.session); e != nil {
		t.Fatal(e)
	}
	got = request("GET", path, "", b, "", "", 200)
	if !strings.Contains(got, `"typing":false`) {
		t.Fatal(got)
	}
	now = now.Add(collision.TTL)
	got = request("GET", path, "", b, "", "", 200)
	if !strings.Contains(got, `"indicators":[]`) {
		t.Fatal(got)
	}
	var count int
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT count(*) FROM collision_indicators`).Scan(&count)
	}); e != nil || count != 0 {
		t.Fatalf("cleanup %d %v", count, e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, tenant, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	request("GET", path, "", b, "", "", 403)
	nilRouter := chi.NewRouter()
	nilCfg := huma.DefaultConfig("test", "1")
	nilCfg.CreateHooks = nil
	nilAPI := humachi.New(nilRouter, nilCfg)
	Register(nilAPI, Dependencies{})
	rec := httptest.NewRecorder()
	nilRouter.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 503 || nilAPI.OpenAPI().Paths["/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision"].Post == nil {
		t.Fatal("nil dependencies or schema")
	}
}
