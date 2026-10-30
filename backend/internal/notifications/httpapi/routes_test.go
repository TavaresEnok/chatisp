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

	"chatisp/internal/notifications"
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

func TestPreferencesHTTPAndIsolation(t *testing.T) {
	ctx := context.Background()
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
	pools, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner, App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	tenant1, tenant2 := uuid.New(), uuid.New()
	if err = store.WithPlatform(ctx, func(c context.Context) error {
		for _, id := range []uuid.UUID{tenant1, tenant2} {
			if _, e := store.CreateTenant(c, db.NewTenant{ID: id, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	type actor struct {
		token, csrf          string
		tenant, user, member uuid.UUID
	}
	seed := func(tenant uuid.UUID) actor {
		a := actor{uuid.NewString(), uuid.NewString(), tenant, uuid.New(), uuid.New()}
		if e := store.WithPlatform(ctx, func(c context.Context) error {
			_, e := store.CreateUser(c, db.NewUser{ID: a.user, Email: fmt.Sprintf("%s@example.com", a.user), Status: "active"})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e := store.WithTenant(ctx, tenant, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.member, UserID: a.user, Role: "agent", Status: "active"})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		now := time.Now()
		if e := store.WithAuth(ctx, func(c context.Context) error {
			return store.InsertSession(c, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: a.tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)})
		}); e != nil {
			t.Fatal(e)
		}
		return a
	}
	a, b, c := seed(tenant1), seed(tenant1), seed(tenant2)
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{Auth: identity, Notifications: notifications.New(store), TrustedOrigins: []string{"https://panel.example"}})
	request := func(method, body string, who actor, origin, csrf string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/api/panel/preferences/notifications", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: who.token})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s: %d want %d: %s", method, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	request("PATCH", `{"sound":false,"browser":true}`, a, "https://evil.example", a.csrf, 403)
	request("PATCH", `{"sound":false}`, a, "https://panel.example", "bad", 403)
	request("PATCH", `{}`, a, "https://panel.example", a.csrf, 422)
	request("PATCH", `{"sound":false,"browser":true}`, a, "https://panel.example", a.csrf, 200)
	for _, x := range []actor{b, c} {
		body := request("GET", "", x, "", "", 200)
		if !strings.Contains(body, `"sound":true`) || !strings.Contains(body, `"browser":false`) {
			t.Fatal(body)
		}
	}
	body := request("GET", "", a, "", "", 200)
	if !strings.Contains(body, `"sound":false`) || !strings.Contains(body, `"browser":true`) {
		t.Fatal(body)
	}
	var audits int
	if err = store.WithTenantTx(ctx, tenant1, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT count(*) FROM audit_logs WHERE action='notification_preferences.update' AND membership_id=$1`, a.member).Scan(&audits)
	}); err != nil || audits != 1 {
		t.Fatalf("audit %d: %v", audits, err)
	}
	nilRouter := chi.NewRouter()
	nilCfg := huma.DefaultConfig("test", "1")
	nilCfg.CreateHooks = nil
	nilAPI := humachi.New(nilRouter, nilCfg)
	Register(nilAPI, Dependencies{})
	rec := httptest.NewRecorder()
	nilRouter.ServeHTTP(rec, httptest.NewRequest("GET", "/api/panel/preferences/notifications", nil))
	if rec.Code != 503 || nilAPI.OpenAPI().Paths["/api/panel/preferences/notifications"].Patch == nil {
		t.Fatal("nil dependencies")
	}
}
