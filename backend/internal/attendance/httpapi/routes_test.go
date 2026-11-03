package httpapi

import (
	"bytes"
	"chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	tenant, user, member, inbox, connector, contact, identity uuid.UUID
	token, csrf                                               string
}

func seed(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	w := fixture{tenant: uuid.New(), user: uuid.New(), member: uuid.New(), inbox: uuid.New(), connector: uuid.New(), contact: uuid.New(), identity: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, e := testStore.CreateTenant(ctx, db.NewTenant{ID: w.tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
			return e
		}
		_, e := testStore.CreateUser(ctx, db.NewUser{ID: w.user, Email: w.user.String() + "@example.com", Status: "active"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, e := testStore.CreateMembership(ctx, db.NewMembership{ID: w.member, UserID: w.user, Role: "agent", Status: "active"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{{`INSERT INTO channel_connectors (id,tenant_id,connector_type,remote_origin,status,enabled) VALUES ($1,$2,'meta_cloud',$3,'connected',true)`, []any{w.connector, w.tenant, w.connector.String()}}, {`INSERT INTO inboxes (id,tenant_id,connector_id,name) VALUES ($1,$2,$3,'Inbox')`, []any{w.inbox, w.tenant, w.connector}}, {`INSERT INTO contacts (id,tenant_id,name) VALUES ($1,$2,'Contact')`, []any{w.contact, w.tenant}}, {`INSERT INTO contact_channel_identities (id,tenant_id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,$5)`, []any{w.identity, w.tenant, w.contact, w.connector, w.identity.String()}}} {
			if _, e := tx.Exec(ctx, q.sql, q.args...); e != nil {
				return e
			}
		}
		_, err := tx.Exec(ctx, `WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Padrão',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, w.tenant, uuid.New(), w.inbox, w.member)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	err = testStore.WithAuth(ctx, func(ctx context.Context) error {
		return testStore.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(w.token), UserID: w.user, TenantID: w.tenant, MembershipID: w.member, CSRFHash: auth.HashSecret(w.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func TestHTTPFlow(t *testing.T) {
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: testStore, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	jobsClient, e := jobs.New(nil, jobs.Handlers{})
	if e != nil {
		t.Fatal(e)
	}
	svc := service.New(testStore, jobsClient)
	w := seed(t)
	other := seed(t)
	if err := testStore.WithTenantTx(context.Background(), w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		app := uuid.New()
		waba := uuid.New()
		aad := "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"
		if _, err := tx.Exec(ctx, `SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, app.String(), app, []byte{1}, aad); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meta_wabas (tenant_id, id, meta_app_id, waba_id, status) VALUES ($1, $2, $3, $4, 'active')`, w.tenant, waba, app, waba.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO meta_phone_numbers (tenant_id, id, meta_waba_id, phone_number_id, status, connector_id, inbox_id) VALUES ($1, $2, $3, $4, 'active', $5, $6)`, w.tenant, uuid.New(), waba, uuid.NewString(), w.connector, w.inbox)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p := service.Principal{UserID: w.user, MembershipID: w.member, TenantID: w.tenant, Role: "agent"}
	now := time.Now().UTC()
	received, e := svc.ReceiveText(context.Background(), p, service.ReceiveInput{InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: uuid.NewString(), Body: "hello", Period: "202610", TrustedAt: now, OpenedAt: now})
	if e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{Auth: identity, Attendance: svc, TrustedOrigins: []string{"https://panel.example"}})
	request := func(method, path, body string, actor fixture, origin, csrf string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor.token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: actor.token})
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	base := "/api/panel/inboxes/" + w.inbox.String() + "/conversations"
	item := base + "/" + received.Conversation.ID.String()
	const origin = "https://panel.example"
	var snap struct {
		Conversations []head `json:"conversations"`
		NextCursor    int64  `json:"next_cursor"`
	}
	if e = json.Unmarshal(request("GET", base, "", w, "", "", 200), &snap); e != nil || len(snap.Conversations) != 1 {
		t.Fatalf("snapshot %+v %v", snap, e)
	}
	request("GET", item+"/history", "", w, "", "", 200)
	request("GET", "/api/panel/inboxes/"+w.inbox.String()+"/events?after=0", "", w, "", "", 200)
	var conv conversation
	e = json.Unmarshal(request("POST", item+"/assume", fmt.Sprintf(`{"expected_version":%d}`, received.Conversation.Version), w, origin, w.csrf, 200), &conv)
	if e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"expected_version":%d,"client_request_id":"send-1","body":"reply"}`, conv.Version)
	var first, second outbound
	e = json.Unmarshal(request("POST", item+"/messages", body, w, origin, w.csrf, 201), &first)
	if e != nil {
		t.Fatal(e)
	}
	e = json.Unmarshal(request("POST", item+"/messages", body, w, origin, w.csrf, 201), &second)
	if e != nil || first.MessageID != second.MessageID || !second.Replayed {
		t.Fatalf("replay %+v %+v %v", first, second, e)
	}
	request("POST", item+"/messages", strings.Replace(body, "reply", "different", 1), w, origin, w.csrf, 409)
	closeBody := fmt.Sprintf(`{"expected_version":%d,"client_request_id":"close-1","reason":"done","result":"resolved","summary":"resolved"}`, conv.Version)
	var closed struct {
		Conversation conversation `json:"conversation"`
	}
	closeResponse := request("POST", item+"/close", closeBody, w, origin, w.csrf, 200)
	var closeFields map[string]json.RawMessage
	if err := json.Unmarshal(closeResponse, &closeFields); err != nil || string(closeFields["farewell"]) != "null" {
		t.Fatalf("farewell ausente ou não nulo: %s (%v)", closeResponse, err)
	}
	e = json.Unmarshal(closeResponse, &closed)
	if e != nil {
		t.Fatal(e)
	}
	var reopened conversation
	e = json.Unmarshal(request("POST", item+"/reopen", fmt.Sprintf(`{"expected_version":%d,"client_request_id":"reopen-1"}`, closed.Conversation.Version), w, origin, w.csrf, 200), &reopened)
	if e != nil || reopened.Protocol != conv.Protocol || reopened.CycleNumber != 2 {
		t.Fatalf("reopen %+v %v", reopened, e)
	}
	request("GET", base, "", fixture{}, "", "", 401)
	request("GET", base, "", other, "", "", 403)
	request("POST", item+"/assume", `{"expected_version":1}`, w, origin, "bad", 403)
	request("POST", item+"/assume", `{"expected_version":1}`, w, "https://evil.example", w.csrf, 403)
	bad := request("POST", item+"/messages", `{"body":"secret-sentinel",`, w, origin, w.csrf, 422)
	if bytes.Contains(bad, []byte("secret-sentinel")) {
		t.Fatal("corpo refletido no erro")
	}
	if err := testStore.WithAuth(context.Background(), func(ctx context.Context) error {
		return testStore.RevokeSessionByHash(ctx, auth.HashSecret(w.token), time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	request("GET", base, "", w, "", "", 401)

}
func TestNilDependencies(t *testing.T) {
	r := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(r, cfg)
	Register(api, Dependencies{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/panel/inboxes/"+uuid.NewString()+"/conversations", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}
