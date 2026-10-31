package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestQueueHTTP(t *testing.T) {
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: testStore, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	w := seed(t)
	svc := service.New(testStore, nil)
	p := service.Principal{TenantID: w.tenant, UserID: w.user, MembershipID: w.member, Role: "agent"}
	now := time.Now().UTC()
	_, e = svc.ReceiveText(context.Background(), p, service.ReceiveInput{InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: uuid.NewString(), Body: "busca exemplo", Period: "202610", TrustedAt: now, OpenedAt: now})
	if e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	RegisterQueue(api, Dependencies{Auth: identity, Attendance: svc})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/panel/queue?search=exemplo", 200}, {"/api/panel/queue?tab=wrong", 422}, {"/api/panel/queue?page_cursor=broken", 422}, {"/api/panel/queue?sector=broken", 422}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.AddCookie(&http.Cookie{Name: "__Host-session", Value: w.token})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		if out.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, out.Code, out.Body.String())
		}
		if tc.status == 200 {
			var v struct {
				Items []any `json:"items"`
			}
			if e = json.Unmarshal(out.Body.Bytes(), &v); e != nil || len(v.Items) != 1 {
				t.Fatalf("response: %s %v", out.Body.String(), e)
			}
		}
	}
}
func TestQueueNilDependencies(t *testing.T) {
	r := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	RegisterQueue(humachi.New(r, cfg), Dependencies{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/panel/queue", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestQueueHTTPContinuationConflict(t *testing.T) {
	ctx := context.Background()
	w := seed(t)
	svc := service.New(testStore, nil)
	p := service.Principal{TenantID: w.tenant, UserID: w.user, MembershipID: w.member, Role: "agent"}
	now := time.Now().UTC()
	receive := func(body string) {
		t.Helper()
		_, e := svc.ReceiveText(ctx, p, service.ReceiveInput{InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: uuid.NewString(), Body: body, Period: "202610", TrustedAt: now, OpenedAt: now})
		if e != nil {
			t.Fatal(e)
		}
	}
	receive("primeira")
	if e := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id) SELECT $1,gen_random_uuid(),$2,$3,$4,'closed','BOUNDARY','202610',2,sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2`, w.tenant, w.inbox, w.contact, w.identity)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: testStore, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	RegisterQueue(humachi.New(router, cfg), Dependencies{Auth: identity, Attendance: svc})
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "__Host-session", Value: w.token})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	first := get("/api/panel/queue?limit=1&order=lastMessage")
	var page struct {
		PageCursor      string `json:"page_cursor"`
		EventNextCursor int64  `json:"event_next_cursor"`
	}
	if e = json.Unmarshal(first.Body.Bytes(), &page); e != nil || first.Code != 200 || page.PageCursor == "" || page.EventNextCursor <= 0 {
		t.Fatalf("first: %d %s %v", first.Code, first.Body.String(), e)
	}
	receive("segunda")
	next := get("/api/panel/queue?limit=1&order=lastMessage&page_cursor=" + url.QueryEscape(page.PageCursor))
	if next.Code != 409 {
		t.Fatalf("continuation: %d %s", next.Code, next.Body.String())
	}
}
