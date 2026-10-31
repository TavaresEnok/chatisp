package businesshours_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	bh "chatisp/internal/businesshours"
	hoursapi "chatisp/internal/businesshours/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresAndHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
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
	admin, e := pgx.Connect(ctx, env.AdminURL)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := admin.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	tenant, user, member, inbox, connector, sector, oncall := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	token, csrf := uuid.NewString(), uuid.NewString()
	// Restricted production APIs are used by the service; elevated fixture writes
	// are confined to this disposable embedded database.
	exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant)
	exec(`INSERT INTO users(id,email,status,totp_ciphertext,totp_key_version,totp_aad_tenant,totp_confirmed_at) VALUES($1,$2,'active',$3,'v1',$4,now())`, user, user.String()+"@example.test", bytes.Repeat([]byte{7}, 32), tenant)
	exec(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,'admin','active')`, member, tenant, user)
	exec(`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, connector, tenant, connector.String())
	exec(`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, inbox, tenant, connector)
	exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Padrão'),($1,$3,'Plantão')`, tenant, sector, oncall)
	exec(`INSERT INTO sector_inboxes VALUES($1,$2,$4),($1,$3,$4)`, tenant, sector, oncall, inbox)
	exec(`INSERT INTO inbox_sector_defaults VALUES($1,$2,$3)`, tenant, inbox, sector)
	now := time.Now()
	if e = store.WithAuth(ctx, func(ctx context.Context) error {
		return store.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); e != nil {
		t.Fatal(e)
	}
	p := bh.Principal{TenantID: tenant, UserID: user, MembershipID: member, Role: "admin"}
	svc := bh.New(store)
	c, e := svc.Get(ctx, p, inbox)
	if e != nil || c.Version != 0 || c.Configured || c.Timezone != "America/Sao_Paulo" {
		t.Fatalf("initial %+v %v", c, e)
	}
	cmd := bh.Command{RequestID: uuid.New(), Config: bh.Config{Intervals: []bh.Interval{{Day: 1, Start: 540, End: 1080}}, Welcome: "Olá {protocolo}", OutsideHours: "Fechado", NobodyAvailable: "Aguarde", OnCallSectorID: &oncall}}
	c, e = svc.Put(ctx, p, inbox, cmd)
	if e != nil || c.Version != 1 || !c.Configured {
		t.Fatalf("put %+v %v", c, e)
	}
	replay, e := svc.Put(ctx, p, inbox, cmd)
	if e != nil || replay.Version != 1 {
		t.Fatalf("replay %+v %v", replay, e)
	}
	conflict := cmd
	conflict.Config.Welcome = "Outro"
	if _, e = svc.Put(ctx, p, inbox, conflict); !errors.Is(e, bh.ErrConflict) {
		t.Fatalf("key reuse %v", e)
	}
	conflict = cmd
	conflict.RequestID = uuid.New()
	if _, e = svc.Put(ctx, p, inbox, conflict); !errors.Is(e, bh.ErrConflict) {
		t.Fatalf("stale version %v", e)
	}
	instant := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	d, e := svc.Evaluate(ctx, p, inbox, instant)
	if e != nil || d.Open || d.NextOpening == nil || d.NextOpening.Hour() != 12 || d.DestinationSectorID == nil || *d.DestinationSectorID != oncall || d.Config.Welcome != cmd.Config.Welcome {
		t.Fatalf("decision %+v %v", d, e)
	}
	// Decisions pin live authorization. A conflicting revocation cannot complete
	// while the transaction owns the binding; no scheduling sleeps are used.
	e = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := bh.EvaluateTx(ctx, tx, instant, tenant, inbox); e != nil {
			return e
		}
		revoker, e := admin.Begin(ctx)
		if e != nil {
			return e
		}
		defer revoker.Rollback(ctx)
		if _, e = revoker.Exec(ctx, `SET LOCAL lock_timeout='50ms'`); e != nil {
			return e
		}
		_, e = revoker.Exec(ctx, `DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3`, tenant, oncall, inbox)
		var pe *pgconn.PgError
		if !errors.As(e, &pe) || pe.Code != "55P03" {
			t.Fatalf("binding was not pinned: %v", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	exec(`DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3`, tenant, oncall, inbox)
	d, e = svc.Evaluate(ctx, p, inbox, instant)
	if e != nil || d.OnCallSectorID != nil || d.DestinationSectorID == nil || *d.DestinationSectorID != sector {
		t.Fatalf("revoked %+v %v", d, e)
	}
	invalid := cmd
	invalid.RequestID = uuid.New()
	invalid.ExpectedVersion = 1
	if _, e = svc.Put(ctx, p, inbox, invalid); !errors.Is(e, bh.ErrInvalid) {
		t.Fatalf("revoked config %v", e)
	}
	exec(`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, tenant, oncall, inbox)
	exec(`UPDATE sectors SET active=false WHERE tenant_id=$1 AND id=$2`, tenant, oncall)
	d, e = svc.Evaluate(ctx, p, inbox, instant)
	if e != nil || d.OnCallSectorID != nil {
		t.Fatalf("inactive %+v %v", d, e)
	}
	if _, e = svc.Put(ctx, p, inbox, invalid); !errors.Is(e, bh.ErrInvalid) {
		t.Fatalf("inactive config %v", e)
	}
	exec(`UPDATE sectors SET active=true WHERE tenant_id=$1 AND id=$2`, tenant, oncall)
	// Concurrent same-version commands: exactly one mutation and audit succeeds.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := cmd
			next.RequestID = uuid.New()
			next.ExpectedVersion = 1
			_, e := svc.Put(ctx, p, inbox, next)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, bh.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("race %d/%d", success, conflicts)
	}
	var count int
	if e = admin.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='business_hours.put'`, tenant).Scan(&count); e != nil || count != 2 {
		t.Fatalf("audit %d %v", count, e)
	}
	del := bh.Command{RequestID: uuid.New(), ExpectedVersion: 2}
	c, e = svc.Delete(ctx, p, inbox, del)
	if e != nil || c.Configured || c.Version != 3 {
		t.Fatalf("delete %+v %v", c, e)
	}
	c, e = svc.Delete(ctx, p, inbox, del)
	if e != nil || c.Version != 3 {
		t.Fatalf("delete replay %+v %v", c, e)
	}
	recreate := cmd
	recreate.ExpectedVersion = 3
	recreate.RequestID = uuid.New()
	c, e = svc.Put(ctx, p, inbox, recreate)
	if e != nil || c.Version != 4 {
		t.Fatalf("recreate %+v %v", c, e)
	}
	// RLS denies visibility without tenant scope and across tenant scopes.
	for _, table := range []string{"inbox_business_hours", "business_hours_commands"} {
		if e = pools.App.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); e != nil || count != 0 {
			t.Fatalf("unscoped %s %d %v", table, count, e)
		}
		e = store.WithTenantTx(ctx, uuid.New(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count)
		})
		if e != nil || count != 0 {
			t.Fatalf("cross tenant %s %d %v", table, count, e)
		}
	}
	e = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := bh.EvaluateTx(ctx, tx, instant, uuid.New(), inbox)
		if !errors.Is(e, sectors.ErrForbidden) {
			t.Fatalf("scope mismatch %v", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Register the real session-backed adapter without central router changes.
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: store, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	hoursapi.Register(humachi.New(router, cfg), hoursapi.Dependencies{Auth: identity, BusinessHours: svc, TrustedOrigins: []string{"https://panel.example"}})
	path := "/api/panel/inboxes/" + inbox.String() + "/business-hours"
	request := func(method, path, session, origin, csrfToken string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrfToken)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: session})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	request("GET", path, "", "", "", nil, 401)
	request("GET", path, token, "", "", nil, 200)
	request("GET", path+"/evaluation?instant=2026-11-02T11:00:00Z", token, "", "", nil, 200)
	request("GET", path+"/evaluation", token, "", "", nil, 422)
	next := cmd
	next.RequestID = uuid.New()
	next.ExpectedVersion = 4
	request("PUT", path, token, "https://evil.example", csrf, next, 403)
	request("PUT", path, token, "https://panel.example", "wrong", next, 403)
	request("PUT", path, token, "https://panel.example", csrf, next, 200)
	request("PUT", path, token, "https://panel.example", csrf, next, 200)
	next.RequestID = uuid.New()
	request("PUT", path, token, "https://panel.example", csrf, next, 409)
	request("DELETE", path, token, "https://panel.example", csrf, map[string]any{"request_id": uuid.New(), "expected_version": 5}, 200)
	for _, role := range []string{"supervisor", "agent"} {
		exec(`UPDATE memberships SET role=$1 WHERE tenant_id=$2 AND id=$3`, role, tenant, member)
		p.Role = role
		want := 200
		if role == "agent" {
			want = 403
		}
		request("GET", path, token, "", "", nil, want)
		request("GET", path+"/evaluation?instant=2026-11-02T11:00:00Z", token, "", "", nil, want)
		request("PUT", path, token, "https://panel.example", csrf, next, 403)
		request("DELETE", path, token, "https://panel.example", csrf, map[string]any{"request_id": uuid.New(), "expected_version": 6}, 403)
	}
	exec(`UPDATE memberships SET role='admin' WHERE tenant_id=$1 AND id=$2`, tenant, member)
	p.Role = "admin"
	other := p
	other.MembershipID = uuid.New()
	if _, e = svc.Get(ctx, other, inbox); !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("forged member %v", e)
	}
	if _, e = svc.Get(ctx, p, uuid.New()); !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("wrong inbox %v", e)
	}
	exec(`UPDATE memberships SET status='disabled' WHERE tenant_id=$1 AND id=$2`, tenant, member)
	if _, e = svc.Get(ctx, p, inbox); !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("disabled %v", e)
	}
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE memberships SET status='active' WHERE tenant_id=$1 AND id=$2`, tenant, member)
	exec(`UPDATE tenants SET timezone='bad/zone' WHERE id=$1`, tenant)
	if _, e = svc.Get(ctx, p, inbox); !errors.Is(e, bh.ErrInvalid) {
		t.Fatalf("invalid timezone %v", e)
	}
	exec(`UPDATE tenants SET timezone='UTC',status='suspended' WHERE id=$1`, tenant)
	if _, e = svc.Get(ctx, p, inbox); !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("suspended %v", e)
	}
	e = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := bh.EvaluateTx(ctx, tx, instant, tenant, inbox)
		return e
	})
	if !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("suspended evaluator %v", e)
	}
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)
	exec(`UPDATE users SET totp_confirmed_at=NULL WHERE id=$1`, user)
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE users SET totp_confirmed_at=now(),is_superadmin=true WHERE id=$1`, user)
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE sessions SET revoked_at=now() WHERE token_hash=$1`, auth.HashSecret(token))
	request("GET", path, token, "", "", nil, 401)
}
