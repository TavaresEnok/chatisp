package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestDirectoryHTTP(t *testing.T) {
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: testStore, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	w, other := seed(t), seed(t)
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	RegisterDirectory(api, Dependencies{Auth: identity, Attendance: service.New(testStore, nil), TrustedOrigins: []string{"https://panel.example"}})
	get := func(inbox uuid.UUID, query string, f fixture) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/panel/inboxes/"+inbox.String()+"/directory"+query, nil)
		if f.token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: f.token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	rec := get(w.inbox, "?limit=10", w)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []struct{ MembershipID, Role, Label string } `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 || body.Items[0].Label != w.user.String()+"@example.com" {
		t.Fatalf("body %s err %v", rec.Body, err)
	}
	if rec.Body.String() == "" || bytes.Contains(rec.Body.Bytes(), []byte("password")) {
		t.Fatal("unexpected sensitive field")
	}
	for name, c := range map[string]struct {
		code  int
		inbox uuid.UUID
		query string
		f     fixture
	}{
		"anonymous":    {401, w.inbox, "", fixture{}},
		"cross tenant": {403, w.inbox, "", other},
		"bad cursor":   {422, w.inbox, "?after=x", w},
		"bad limit":    {422, w.inbox, "?limit=101", w},
	} {
		if got := get(c.inbox, c.query, c.f); got.Code != c.code && !(c.code == 403 && got.Code == 404) {
			t.Errorf("%s: status %d want %d: %s", name, got.Code, c.code, got.Body)
		}
	}
}
