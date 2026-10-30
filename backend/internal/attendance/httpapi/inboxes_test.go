package httpapi

import (
	"bytes"
	"context"
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
)

func TestInboxesSessionAndMembership(t *testing.T) {
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
	Register(humachi.New(router, cfg), Dependencies{Auth: identity, Attendance: service.New(testStore, nil)})
	request := func(w fixture, status int) []byte {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/panel/inboxes", nil)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: w.token})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("got %d want %d: %s", rec.Code, status, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	var result struct {
		Inboxes []service.Inbox `json:"inboxes"`
	}
	if err := json.Unmarshal(request(w, 200), &result); err != nil || len(result.Inboxes) != 1 || result.Inboxes[0].ID != w.inbox {
		t.Fatalf("inboxes: %+v %v", result, err)
	}
	if err := json.Unmarshal(request(other, 200), &result); err != nil || len(result.Inboxes) != 1 || result.Inboxes[0].ID != other.inbox {
		t.Fatalf("isolation: %+v %v", result, err)
	}
	conn := adminConn(t)
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `UPDATE memberships SET role='supervisor' WHERE id=$1`, w.member); err != nil {
		t.Fatal(err)
	}
	request(w, 200)
	if _, err := conn.Exec(context.Background(), `UPDATE memberships SET status='disabled' WHERE id=$1`, w.member); err != nil {
		t.Fatal(err)
	}
	request(w, 403)
	if _, err := conn.Exec(context.Background(), `DELETE FROM inboxes WHERE id=$1`, other.inbox); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request(other, 200), &result); err != nil || len(result.Inboxes) != 0 {
		t.Fatalf("empty: %+v %v", result, err)
	}
	if _, err := conn.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE id=$1`, other.user); err != nil {
		t.Fatal(err)
	}
	request(other, 403)
}
