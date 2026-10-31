package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestAssignmentHTTPReplay(t *testing.T) {
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: testStore, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	w := seed(t)
	svc := service.New(testStore, nil)
	now := time.Now().UTC()
	got, err := svc.ReceiveText(context.Background(), service.Principal{UserID: w.user, MembershipID: w.member, TenantID: w.tenant, Role: "agent"}, service.ReceiveInput{InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: uuid.NewString(), Body: "hello", Period: "202610", TrustedAt: now, OpenedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	RegisterAssignment(api, Dependencies{Auth: identity, Attendance: svc, TrustedOrigins: []string{"https://panel.example"}})
	path := "/api/panel/inboxes/" + w.inbox.String() + "/conversations/" + got.Conversation.ID.String() + "/transfer-member"
	request := func(body string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://panel.example")
		req.Header.Set("X-CSRF-Token", w.csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: w.token})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("status %d want %d: %s", rec.Code, want, rec.Body.String())
		}
	}
	body := fmt.Sprintf(`{"expected_version":%d,"client_request_id":"same","target_id":"%s"}`, got.Conversation.Version, w.member)
	request(body, 200)
	request(body, 200)
	request(strings.Replace(body, `"same"`, `"different"`, 1), 409)
}
