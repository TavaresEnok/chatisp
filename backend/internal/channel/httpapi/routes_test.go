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

	"chatisp/internal/channel/configuration"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type actor struct {
	token, csrf              string
	tenant, user, membership uuid.UUID
}

func seed(t *testing.T, ring *crypto.Ring, role string, confirmed bool) actor {
	t.Helper()
	ctx := context.Background()
	a := actor{token: uuid.NewString(), csrf: uuid.NewString(), tenant: uuid.New(), user: uuid.New(), membership: uuid.New()}
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: a.tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: a.user, Email: fmt.Sprintf("%s@example.com", a.user), Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, a.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: a.membership, UserID: a.user, Role: role, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		if confirmed {
			sealed, err := ring.Encrypt([]byte("totp-local-test"), crypto.Context{TenantID: a.tenant.String(), Entity: "user", EntityID: a.user.String(), Field: "totp_secret"})
			if err != nil {
				return err
			}
			if err = testStore.BeginMFA(ctx, a.user, a.tenant, sealed.Ciphertext, sealed.Version); err != nil {
				return err
			}
			if ok, err := testStore.ConfirmMFA(ctx, a.user, now); err != nil || !ok {
				return fmt.Errorf("MFA: %v", err)
			}
		}
		return testStore.InsertSession(ctx, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: a.tenant, MembershipID: a.membership, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	return a
}
func TestHTTPFlow(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: testStore, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := configuration.New(testStore, testPools.Ingress, ring)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{Auth: identity, Channels: channels, TrustedOrigins: []string{"https://panel.example"}})
	a := seed(t, ring, "admin", true)
	other := seed(t, ring, "admin", true)
	agent := seed(t, ring, "agent", true)
	pending := seed(t, ring, "admin", false)
	request := func(method, path, body string, who actor, origin, csrf string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if who.token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: who.token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		data := rec.Body.Bytes()
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, data)
		}
		if bytes.Contains(data, []byte("secret-local-test")) || bytes.Contains(data, []byte("token-local-test")) {
			t.Fatalf("segredo na resposta: %s", data)
		}
		return data
	}
	const path = "/api/panel/channels/meta"
	const origin = "https://panel.example"
	body := func(phone string) string {
		return fmt.Sprintf(`{"inbox_name":"Inbox","display_number":"+5511999999999","app_id":"app-http","app_secret":"secret-local-test","waba_id":"waba-http","phone_number_id":"%s","access_token":"token-local-test"}`, phone)
	}
	var first box
	if err = json.Unmarshal(request("POST", path, body("phone-http-1"), a, origin, a.csrf, 200), &first); err != nil {
		t.Fatal(err)
	}
	request("POST", path, body("phone-http-2"), a, origin, a.csrf, 200)
	var status struct {
		Boxes  []box   `json:"boxes"`
		Alerts []alert `json:"alerts"`
	}
	if err = json.Unmarshal(request("GET", path, "", a, "", "", 200), &status); err != nil || len(status.Boxes) != 2 {
		t.Fatalf("status: %+v %v", status, err)
	}
	request("GET", path+"/inspection", "", a, "", "", 200)
	if err = channels.Server().ReportInvalidCredential(context.Background(), a.tenant, first.ConnectorID, first.Version); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(request("GET", path, "", a, "", "", 200), &status); err != nil || len(status.Alerts) != 1 {
		t.Fatalf("alerta: %+v %v", status, err)
	}
	request("POST", path+"/alerts/"+status.Alerts[0].ID.String()+"/ack", "", a, origin, a.csrf, 204)
	request("POST", path, body("phone-http-3"), a, "https://evil.example", a.csrf, 403)
	request("POST", path, body("phone-http-3"), a, origin, "bad", 403)
	request("GET", path, "", actor{}, "", "", 401)
	request("GET", path, "", agent, "", "", 403)
	request("GET", path, "", pending, "", "", 403)
	request("GET", path, "", other, "", "", 200)
	conflict := strings.ReplaceAll(body("phone-http-other"), "secret-local-test", "different-secret-local-test")
	request("POST", path, conflict, other, origin, other.csrf, 409)
	foreign := strings.ReplaceAll(body("phone-http-1"), "waba-http", "waba-http-foreign")
	request("POST", path, foreign, other, origin, other.csrf, 409)
	request("POST", path, `{"app_secret":"secret-local-test",`, a, origin, a.csrf, 422)
	conn := adminConn(t)
	defer conn.Close(context.Background())
	if _, err = conn.Exec(context.Background(), `UPDATE users SET is_superadmin=true WHERE id=$1`, other.user); err != nil {
		t.Fatal(err)
	}
	request("GET", path, "", other, "", "", 403)
	if _, err = conn.Exec(context.Background(), `UPDATE tenants SET status='suspended' WHERE id=$1`, a.tenant); err != nil {
		t.Fatal(err)
	}
	request("GET", path, "", a, "", "", 403)
	if _, err = conn.Exec(context.Background(), `UPDATE tenants SET status='active' WHERE id=$1`, a.tenant); err != nil {
		t.Fatal(err)
	}
	request("GET", path, "", a, "", "", 200)

	if _, err = conn.Exec(context.Background(), `UPDATE memberships SET status='disabled' WHERE id=$1`, a.membership); err != nil {
		t.Fatal(err)
	}
	request("GET", path, "", a, "", "", 403)
}
func TestNilDependencies(t *testing.T) {
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{})
	content := api.OpenAPI().Paths["/api/panel/channels/meta"].Post.RequestBody.Content
	if len(content) != 1 || content["application/json"] == nil {
		t.Fatalf("conteúdo OpenAPI inesperado: %v", content)
	}
	r := httptest.NewRecorder()
	router.ServeHTTP(r, httptest.NewRequest("GET", "/api/panel/channels/meta", nil))
	if r.Code != 503 {
		t.Fatalf("got %d", r.Code)
	}
}
