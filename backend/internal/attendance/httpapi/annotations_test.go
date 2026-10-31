package httpapi

import (
	"bytes"
	"chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
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

func TestAnnotationsHTTP(t *testing.T) {
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: testStore, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	w := seed(t)
	other := fixture{tenant: w.tenant, user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
	ctx := context.Background()
	if err = testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, e := testStore.CreateUser(ctx, db.NewUser{ID: other.user, Email: other.user.String() + "@example.com", Status: "active"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, e := testStore.CreateMembership(ctx, db.NewMembership{ID: other.member, UserID: other.user, Role: "agent", Status: "active"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO sector_memberships SELECT tenant_id,sector_id,$3 FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, w.tenant, w.member, other.member)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = testStore.WithAuth(ctx, func(ctx context.Context) error {
		now := time.Now()
		return testStore.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(other.token), UserID: other.user, TenantID: other.tenant, MembershipID: other.member, CSRFHash: auth.HashSecret(other.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	svc := service.New(testStore, nil)
	p := service.Principal{TenantID: w.tenant, UserID: w.user, MembershipID: w.member, Role: "agent"}
	now := time.Now().UTC()
	c, err := svc.ReceiveText(context.Background(), p, service.ReceiveInput{InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: uuid.NewString(), Body: "hi", Period: "202610", TrustedAt: now, OpenedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	RegisterAnnotations(api, Dependencies{Auth: identity, Attendance: svc, TrustedOrigins: []string{"https://panel.example"}})
	path := "/api/panel/inboxes/" + w.inbox.String() + "/conversations/" + c.Conversation.ID.String() + "/annotations"
	req := func(method, path, body, origin, csrf string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(&http.Cookie{Name: "__Host-session", Value: w.token})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		if out.Code != want {
			t.Fatalf("%s %s: %d expected %d: %s", method, path, out.Code, want, out.Body.String())
		}
	}
	req("POST", path, `{"kind":"note","value":"privado","request_id":"n1"}`, "https://panel.example", w.csrf, 200)
	req("POST", path, `{"kind":"priority","value":"urgent"}`, "https://panel.example", w.csrf, 403)
	req("POST", path, `{"kind":"note","value":"privado","request_id":"n2"}`, "https://evil.example", w.csrf, 403)
	req("POST", path, `{"kind":"note","value":"privado","request_id":"n2"}`, "https://panel.example", "bad", 403)
	req("GET", path, "", "", "", 200)
	readAs := func(actor fixture) map[string]any {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "__Host-session", Value: actor.token})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		if out.Code != 200 {
			t.Fatalf("read: %d %s", out.Code, out.Body.String())
		}
		var v map[string]any
		if err := json.Unmarshal(out.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	history, err := svc.History(ctx, p, w.inbox, c.Conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	req("POST", path+"/read", fmt.Sprintf(`{"watermark":%d,"unread":false,"read_version":0}`, history.NextCursor), "https://panel.example", w.csrf, 200)
	second := readAs(other)
	if second["watermark"] != float64(0) || second["unread_count"] != float64(1) {
		t.Fatalf("other marker: %v", second)
	}
	if readAs(w)["unread"] != false {
		t.Fatal("read marker did not clear")
	}
}

func TestAnnotationsNilDependencies(t *testing.T) {
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	RegisterAnnotations(api, Dependencies{})
	r := httptest.NewRequest("GET", "/api/panel/annotation-tags", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("got %d", w.Code)
	}
}
