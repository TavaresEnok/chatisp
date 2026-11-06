package httpapi

import (
	"chatisp/internal/platform/auth"
	"chatisp/internal/retention"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPurgePreviewAuthorizationAndExplicitBlockers(t *testing.T) {
	s := seedTenant(t)
	router := chi.NewRouter()
	Register(router, Dependencies{Auth: stubAuth{p: auth.Principal{UserID: s.admin.UserID, TenantID: s.tenant, MembershipID: s.admin.MembershipID, Role: "admin"}}, Store: testStore})
	req := httptest.NewRequest(http.MethodGet, "/api/panel/retention/purge-preview?limit=12", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var result retention.PurgePreview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Classes) != 5 || result.ExecutionAvailable || result.AutomaticPurge {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	for _, item := range result.Classes {
		if item.Blocker == "" || item.Days != nil || item.Candidates != nil {
			t.Fatalf("undefined policy %+v", item)
		}
	}
	invalid := httptest.NewRequest(http.MethodGet, "/api/panel/retention/purge-preview?limit=0", nil)
	invalid.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, invalid)
	if bad.Code != 422 {
		t.Fatalf("unbounded %d %s", bad.Code, bad.Body.String())
	}
	agentRouter := chi.NewRouter()
	Register(agentRouter, Dependencies{Auth: stubAuth{p: auth.Principal{UserID: s.agent.UserID, TenantID: s.tenant, MembershipID: s.agent.MembershipID, Role: "agent"}}, Store: testStore})
	denied := httptest.NewRecorder()
	agentRouter.ServeHTTP(denied, req)
	if denied.Code != 403 {
		t.Fatalf("agent %d %s", denied.Code, denied.Body.String())
	}
}
