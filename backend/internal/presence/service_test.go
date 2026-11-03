package presence_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/presence"
	presenceapi "chatisp/internal/presence/httpapi"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPresencePostgres(t *testing.T) {
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
	tenant, other := uuid.New(), uuid.New()
	sectorA, sectorB := uuid.New(), uuid.New()
	exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active'),($2,'UTC','active')`, tenant, other)
	exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte'),($1,$3,'Financeiro')`, tenant, sectorA, sectorB)

	far := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	type actor struct {
		user, member, session uuid.UUID
		token, csrf           string
		p                     presence.Principal
	}
	makeActor := func(tenantID uuid.UUID, role, email string, totp bool) actor {
		t.Helper()
		a := actor{user: uuid.New(), member: uuid.New(), session: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
		if totp {
			exec(`INSERT INTO users(id,email,status,totp_ciphertext,totp_key_version,totp_aad_tenant,totp_confirmed_at) VALUES($1,$2,'active',$3,'v1',$4,now())`, a.user, email, bytes.Repeat([]byte{7}, 32), tenantID)
		} else {
			exec(`INSERT INTO users(id,email,status) VALUES($1,$2,'active')`, a.user, email)
		}
		exec(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,$4,'active')`, a.member, tenantID, a.user, role)
		a.p = presence.Principal{SessionID: a.session, UserID: a.user, TenantID: tenantID, MembershipID: a.member, Role: role}
		now := time.Now().UTC()
		if err := store.WithAuth(ctx, func(ctx context.Context) error {
			return store.InsertSession(ctx, db.NewSession{ID: a.session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: tenantID, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: far, AbsoluteExpiresAt: far})
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	adminA := makeActor(tenant, "admin", "admin@example.test", true)
	agent := makeActor(tenant, "agent", "agent@example.test", false)
	second := uuid.New()
	secondToken := uuid.NewString()
	if err := store.WithAuth(ctx, func(ctx context.Context) error {
		return store.InsertSession(ctx, db.NewSession{ID: second, TokenHash: auth.HashSecret(secondToken), UserID: agent.user, TenantID: tenant, MembershipID: agent.member, CSRFHash: auth.HashSecret(uuid.NewString()), Now: time.Now().UTC(), IdleExpiresAt: far, AbsoluteExpiresAt: far})
	}); err != nil {
		t.Fatal(err)
	}
	agentB := makeActor(tenant, "agent", "agentb@example.test", false)
	agentC := makeActor(tenant, "agent", "agentc@example.test", false)
	agentD := makeActor(tenant, "agent", "agentd@example.test", false)
	outsider := makeActor(other, "agent", "outro@example.test", false)
	exec(`INSERT INTO sector_memberships VALUES($1,$2,$3),($1,$2,$4),($1,$2,$5),($1,$6,$7)`, tenant, sectorA, agent.member, agentB.member, agentD.member, sectorB, agentC.member)

	clock := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	svc := presence.New(store)
	svc.Now = func() time.Time { return clock }
	tab1, tab2 := uuid.New(), uuid.New()
	snap, err := svc.Heartbeat(ctx, agent.p, tab1)
	if err != nil || snap.State != presence.StateAvailable || snap.TabCount != 1 || !snap.Assignable || snap.ToleranceSeconds != 300 {
		t.Fatalf("heartbeat %+v %v", snap, err)
	}
	otherSession := agent.p
	otherSession.SessionID = second
	snap, err = svc.Heartbeat(ctx, otherSession, tab2)
	if err != nil || snap.TabCount != 2 || snap.State != presence.StateAvailable {
		t.Fatalf("segunda aba %+v %v", snap, err)
	}
	snap, err = svc.Close(ctx, agent.p, tab1)
	if err != nil || snap.TabCount != 1 || snap.State != presence.StateAvailable || snap.OfflineSince != nil || snap.OfflineForUnassign {
		t.Fatalf("uma aba permanece %+v %v", snap, err)
	}
	if _, err = svc.Heartbeat(ctx, agentB.p, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Heartbeat(ctx, agentC.p, uuid.New()); err != nil {
		t.Fatal(err)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		list, err := presence.ListAvailable(ctx, tx, tenant, sectorA, clock)
		if err != nil || len(list) != 2 {
			t.Fatalf("lista %d %v", len(list), err)
		}
		seen := map[uuid.UUID]bool{}
		for _, item := range list {
			seen[item.UserID] = true
			if !item.Assignable || item.State != presence.StateAvailable {
				t.Fatalf("item %+v", item)
			}
		}
		if !seen[agent.user] || !seen[agentB.user] || seen[agentC.user] {
			t.Fatalf("membros %#v", seen)
		}
		otherSector, err := presence.ListAvailable(ctx, tx, tenant, sectorB, clock)
		if err != nil || len(otherSector) != 1 || otherSector[0].UserID != agentC.user {
			t.Fatalf("outro setor %+v %v", otherSector, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	manual := presence.ManualCommand{RequestID: uuid.New(), State: presence.ManualBusy}
	snap, err = svc.SetManual(ctx, agent.p, manual)
	if err != nil || snap.State != presence.StateBusy || snap.Assignable || snap.Manual != presence.ManualBusy || snap.TabCount != 1 {
		t.Fatalf("ocupado %+v %v", snap, err)
	}
	replay, err := svc.SetManual(ctx, agent.p, manual)
	if err != nil || replay.State != presence.StateBusy || replay.LastHeartbeat == nil || snap.LastHeartbeat == nil || !replay.LastHeartbeat.Equal(*snap.LastHeartbeat) {
		t.Fatalf("replay %+v %v", replay, err)
	}
	changed := manual
	changed.State = presence.ManualAvailable
	if _, err = svc.SetManual(ctx, agent.p, changed); !errors.Is(err, presence.ErrConflict) {
		t.Fatalf("hash %v", err)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		list, err := presence.ListAvailable(ctx, tx, tenant, sectorA, clock)
		if err != nil || len(list) != 1 || list[0].UserID != agentB.user {
			t.Fatalf("ocupado fora da lista %+v %v", list, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SetManual(ctx, agent.p, presence.ManualCommand{RequestID: uuid.New(), State: presence.ManualAvailable}); err != nil {
		t.Fatal(err)
	}
	snap, err = svc.Close(ctx, otherSession, tab2)
	if err != nil || snap.State != presence.StateOffline || snap.Connected || snap.OfflineSince == nil || !snap.OfflineSince.Equal(clock) || snap.OfflineForUnassign {
		t.Fatalf("offline imediato %+v %v", snap, err)
	}
	var conversations int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE tenant_id=$1`, tenant).Scan(&conversations); err != nil || conversations != 0 {
		t.Fatalf("conversas alteradas %d %v", conversations, err)
	}
	clock = clock.Add(299 * time.Second)
	snap, err = presenceAt(ctx, store, tenant, agent.user, clock)
	if err != nil || snap.OfflineForUnassign || snap.State != presence.StateOffline {
		t.Fatalf("dentro da tolerância %+v %v", snap, err)
	}
	clock = clock.Add(time.Second)
	snap, err = presenceAt(ctx, store, tenant, agent.user, clock)
	if err != nil || !snap.OfflineForUnassign || snap.Assignable {
		t.Fatalf("tolerância vencida %+v %v", snap, err)
	}
	clock = time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	snap, err = svc.Heartbeat(ctx, agent.p, tab1)
	if err != nil || snap.State != presence.StateAvailable || snap.OfflineSince != nil {
		t.Fatalf("reconexão %+v %v", snap, err)
	}
	clock = clock.Add(presence.HeartbeatTTL)
	snap, err = presenceAt(ctx, store, tenant, agent.user, clock)
	if err != nil || snap.Connected || snap.OfflineSince == nil || !snap.OfflineSince.Equal(clock) || snap.OfflineForUnassign {
		t.Fatalf("expiração %+v %v", snap, err)
	}
	clock = clock.Add(presence.DefaultOfflineTolerance)
	snap, err = presenceAt(ctx, store, tenant, agent.user, clock)
	if err != nil || !snap.OfflineForUnassign {
		t.Fatalf("expiração e tolerância %+v %v", snap, err)
	}
	expiredAt := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC).Add(presence.HeartbeatTTL)
	snap, err = svc.Close(ctx, agent.p, uuid.New())
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(expiredAt) || !snap.OfflineForUnassign {
		t.Fatalf("aba inexistente após expirar %+v %v", snap, err)
	}
	snap, err = svc.Close(ctx, agent.p, tab1)
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(expiredAt) || !snap.OfflineForUnassign {
		t.Fatalf("close tardio %+v %v", snap, err)
	}
	snap, err = svc.Close(ctx, agent.p, tab1)
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(expiredAt) || !snap.OfflineForUnassign {
		t.Fatalf("close repetido %+v %v", snap, err)
	}
	idle := makeActor(tenant, "agent", "ocioso@example.test", false)
	snap, err = svc.Close(ctx, idle.p, uuid.New())
	if err != nil || snap.State != presence.StateOffline || snap.OfflineSince != nil || snap.OfflineForUnassign || snap.TabCount != 0 {
		t.Fatalf("quem nunca conectou %+v %v", snap, err)
	}
	snap, err = presenceAt(ctx, store, tenant, idle.user, clock.Add(24*time.Hour))
	if err != nil || snap.OfflineSince != nil || snap.OfflineForUnassign {
		t.Fatalf("sem início depois da tolerância %+v %v", snap, err)
	}
	mark := clock
	keeper := makeActor(tenant, "agent", "keeper@example.test", false)
	base := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	clock = base
	tabEarly, tabLate := uuid.New(), uuid.New()
	if _, err = svc.Heartbeat(ctx, keeper.p, tabEarly); err != nil {
		t.Fatal(err)
	}
	clock = base.Add(40 * time.Second)
	if _, err = svc.Heartbeat(ctx, keeper.p, tabLate); err != nil {
		t.Fatal(err)
	}
	snap, err = svc.Close(ctx, keeper.p, tabLate)
	closedAt := base.Add(40 * time.Second)
	if err != nil || snap.Connected || snap.OfflineSince == nil || !snap.OfflineSince.Equal(closedAt) || snap.OfflineForUnassign {
		t.Fatalf("fecho da última aba viva %+v %v", snap, err)
	}
	clock = base.Add(100 * time.Second)
	snap, err = svc.Close(ctx, keeper.p, tabEarly)
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(closedAt) || snap.OfflineForUnassign {
		t.Fatalf("aba vencida não move o início %+v %v", snap, err)
	}
	snap, err = presenceAt(ctx, store, tenant, keeper.user, closedAt.Add(presence.DefaultOfflineTolerance))
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(closedAt) || !snap.OfflineForUnassign {
		t.Fatalf("tolerância a partir do fecho real %+v %v", snap, err)
	}
	pair := makeActor(tenant, "agent", "par@example.test", false)
	clock = base
	tabP1, tabP2 := uuid.New(), uuid.New()
	if _, err = svc.Heartbeat(ctx, pair.p, tabP1); err != nil {
		t.Fatal(err)
	}
	clock = base.Add(10 * time.Second)
	if _, err = svc.Heartbeat(ctx, pair.p, tabP2); err != nil {
		t.Fatal(err)
	}
	laterExp := base.Add(40 * time.Second)
	clock = base.Add(80 * time.Second)
	snap, err = svc.Close(ctx, pair.p, tabP1)
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(laterExp) || snap.OfflineForUnassign {
		t.Fatalf("maior expiração permanece %+v %v", snap, err)
	}
	snap, err = svc.Close(ctx, pair.p, uuid.New())
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(laterExp) || snap.OfflineForUnassign {
		t.Fatalf("aba inexistente não reinicia %+v %v", snap, err)
	}
	snap, err = presenceAt(ctx, store, tenant, pair.user, laterExp.Add(presence.DefaultOfflineTolerance))
	if err != nil || snap.OfflineSince == nil || !snap.OfflineSince.Equal(laterExp) || !snap.OfflineForUnassign {
		t.Fatalf("tolerância a partir da expiração %+v %v", snap, err)
	}
	clock = mark

	exec(`UPDATE users SET status='disabled' WHERE id=$1`, agentB.user)
	snap, err = presenceAt(ctx, store, tenant, agentB.user, time.Date(2026, 11, 3, 15, 0, 10, 0, time.UTC))
	if err != nil || snap.State != presence.StateOffline || snap.Assignable || snap.Connected {
		t.Fatalf("usuário desativado %+v %v", snap, err)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		list, err := presence.ListAvailable(ctx, tx, tenant, sectorA, time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC))
		if err != nil {
			return err
		}
		for _, item := range list {
			if item.UserID == agentB.user {
				t.Fatal("desativado listado")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Heartbeat(ctx, agentB.p, uuid.New()); !errors.Is(err, presence.ErrForbidden) {
		t.Fatalf("heartbeat de desativado %v", err)
	}
	exec(`UPDATE users SET status='active' WHERE id=$1`, agentB.user)

	if _, err = svc.Heartbeat(ctx, agentD.p, uuid.New()); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE memberships SET status='disabled' WHERE tenant_id=$1 AND id=$2`, tenant, agentD.member)
	snap, err = presenceAt(ctx, store, tenant, agentD.user, clock)
	if err != nil || snap.Assignable || snap.State != presence.StateOffline {
		t.Fatalf("matrícula desativada %+v %v", snap, err)
	}
	exec(`UPDATE memberships SET status='active' WHERE tenant_id=$1 AND id=$2`, tenant, agentD.member)

	exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, tenant)
	if _, err = presenceAt(ctx, store, tenant, agent.user, clock); !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("tenant suspenso %v", err)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := presence.ListAvailable(ctx, tx, tenant, sectorA, clock)
		return err
	})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("lista suspensa %v", err)
	}
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)

	if _, err = svc.Heartbeat(ctx, outsider.p, uuid.New()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"presence_connections", "presence_operator", "presence_commands"} {
		var n int
		if err = pools.App.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("sem escopo %s %d %v", table, n, err)
		}
		err = store.WithTenantTx(ctx, uuid.New(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
		})
		if err != nil || n != 0 {
			t.Fatalf("outro tenant %s %d %v", table, n, err)
		}
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := presence.Effective(ctx, tx, other, outsider.user, clock)
		return err
	})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("escopo trocado %v", err)
	}
	err = store.WithTenantTx(ctx, other, func(ctx context.Context, tx pgx.Tx) error {
		snap, err := presence.Effective(ctx, tx, other, agent.user, clock)
		if err != nil || snap.Assignable || snap.State != presence.StateOffline {
			t.Fatalf("usuário de outro tenant %+v %v", snap, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	settings, err := svc.PutTolerance(ctx, adminA.p, presence.ToleranceCommand{RequestID: uuid.New(), OfflineToleranceSeconds: 0})
	if err != nil || settings.Version != 1 || !settings.Configured || settings.OfflineToleranceSeconds != 0 {
		t.Fatalf("tolerância %+v %v", settings, err)
	}
	stale := presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 0, OfflineToleranceSeconds: 10}
	if _, err = svc.PutTolerance(ctx, adminA.p, stale); !errors.Is(err, presence.ErrConflict) {
		t.Fatalf("versão %v", err)
	}
	if _, err = svc.PutTolerance(ctx, agent.p, presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 1, OfflineToleranceSeconds: 10}); !errors.Is(err, presence.ErrForbidden) {
		t.Fatalf("agente configura %v", err)
	}
	clock = time.Date(2026, 11, 3, 17, 0, 0, 0, time.UTC)
	snap, err = svc.Close(ctx, agent.p, tab1)
	if err != nil || !snap.OfflineForUnassign || snap.ToleranceSeconds != 0 {
		t.Fatalf("tolerância zero %+v %v", snap, err)
	}
	var audits int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action IN ('presence.manual','presence.tolerance')`, tenant).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audit %d %v", audits, err)
	}

	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	presenceapi.Register(humachi.New(router, cfg), presenceapi.Dependencies{Auth: identity, Presence: svc, TrustedOrigins: []string{"https://panel.example"}})
	call := func(method, path, token, origin, csrf string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	call("GET", "/api/panel/presence", "", "", "", nil, 401)
	call("GET", "/api/panel/presence/settings", adminA.token, "", "", nil, 200)
	call("PUT", "/api/panel/presence/settings", adminA.token, "https://evil.example", adminA.csrf, presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 1, OfflineToleranceSeconds: 120}, 403)
	call("PUT", "/api/panel/presence/settings", adminA.token, "https://panel.example", "wrong", presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 1, OfflineToleranceSeconds: 120}, 403)
	call("PUT", "/api/panel/presence/settings", adminA.token, "https://panel.example", adminA.csrf, presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 1, OfflineToleranceSeconds: 120}, 200)
	body := call("POST", "/api/panel/presence/heartbeat", adminA.token, "https://panel.example", adminA.csrf, map[string]any{"tab_id": uuid.New()}, 200)
	var got presence.Snapshot
	if err = json.Unmarshal(body, &got); err != nil || got.State != presence.StateAvailable || got.UserID != adminA.user {
		t.Fatalf("http %+v %v %s", got, err, body)
	}
	call("GET", "/api/panel/sectors/"+sectorA.String()+"/presence", adminA.token, "", "", nil, 200)
	call("GET", "/api/panel/sectors/"+uuid.NewString()+"/presence", adminA.token, "", "", nil, 404)
	exec(`UPDATE memberships SET role='agent' WHERE tenant_id=$1 AND id=$2`, tenant, adminA.member)
	call("GET", "/api/panel/sectors/"+sectorA.String()+"/presence", adminA.token, "", "", nil, 403)
	call("PUT", "/api/panel/presence/settings", adminA.token, "https://panel.example", adminA.csrf, presence.ToleranceCommand{RequestID: uuid.New(), ExpectedVersion: 2, OfflineToleranceSeconds: 30}, 403)
	call("POST", "/api/panel/presence/heartbeat", adminA.token, "https://panel.example", adminA.csrf, map[string]any{"tab_id": uuid.Nil}, 422)
	exec(`UPDATE memberships SET role='supervisor' WHERE tenant_id=$1 AND id=$2`, tenant, adminA.member)
	call("GET", "/api/panel/sectors/"+sectorA.String()+"/presence", adminA.token, "", "", nil, 200)
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, adminA.user)
	call("GET", "/api/panel/presence", adminA.token, "", "", nil, 403)
	exec(`UPDATE users SET status='active' WHERE id=$1`, adminA.user)
	exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, tenant)
	call("GET", "/api/panel/presence", adminA.token, "", "", nil, 403)
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)
	exec(`UPDATE sessions SET revoked_at=now() WHERE id=$1`, adminA.session)
	call("GET", "/api/panel/presence", adminA.token, "", "", nil, 401)
}

func presenceAt(ctx context.Context, store *db.Store, tenant, user uuid.UUID, now time.Time) (presence.Snapshot, error) {
	var snap presence.Snapshot
	err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		snap, err = presence.Effective(ctx, tx, tenant, user, now)
		return err
	})
	return snap, err
}
