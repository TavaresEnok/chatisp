package publicapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
)

func TestTokenScopeTenantAndRateLimit(t *testing.T) {
	ctx := context.Background()
	own := seedConversation(t, 60)
	other := seedConversation(t, 60)
	raw, id, err := IssueScoped(ctx, testStore, own.tenant, []string{"conversations.read"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var resolved uuid.UUID
	err = testStore.WithPublicRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_id FROM api_token_resolve($1)`, auth.HashSecret(raw)).Scan(&resolved)
	})
	if err != nil || resolved != id {
		t.Fatalf("hash não resolve o token: %v %s", err, resolved)
	}
	err = testStore.WithPublicRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_id FROM api_token_resolve($1)`, []byte(raw)).Scan(&resolved)
	})
	if err == nil {
		t.Fatal("o segredo cru foi aceito como hash")
	}

	panel := chi.NewRouter()
	MountTokenPanel(panel, TokenPanelDeps{
		Auth:           stubTokenAuth{principal: auth.Principal{TenantID: own.tenant, Role: "admin"}},
		Store:          testStore,
		TrustedOrigins: []string{"https://panel.example"},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/panel/api-tokens", strings.NewReader(`{"scopes":["contacts.read"],"request_limit":10}`))
	createReq.Header.Set("Origin", "https://panel.example")
	createReq.Header.Set(auth.CSRFHeader, "csrf")
	createReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sessao"})
	createRec := httptest.NewRecorder()
	panel.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated || !strings.Contains(createRec.Body.String(), `"secret"`) {
		t.Fatalf("criação %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID     uuid.UUID `json:"id"`
		Secret string    `json:"secret"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil || created.Secret == "" {
		t.Fatal(err)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/panel/api-tokens", nil)
	listReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sessao"})
	listRec := httptest.NewRecorder()
	panel.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || strings.Contains(listRec.Body.String(), created.Secret) || strings.Contains(listRec.Body.String(), `"secret"`) {
		t.Fatalf("lista revelou segredo: %s", listRec.Body.String())
	}

	first := call(http.MethodGet, "/v1/conversations/"+own.protocol, raw, false)
	if first.Code != http.StatusOK {
		t.Fatalf("leitura %d %s", first.Code, first.Body.String())
	}
	second := call(http.MethodGet, "/v1/conversations/"+own.protocol, raw, false)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("limite %d %s", second.Code, second.Body.String())
	}

	narrow, _, err := IssueScoped(ctx, testStore, own.tenant, []string{"templates.send"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	missing := call(http.MethodGet, "/v1/conversations/"+own.protocol, narrow, false)
	if missing.Code != http.StatusForbidden {
		t.Fatalf("escopo %d %s", missing.Code, missing.Body.String())
	}
	missingAgain := call(http.MethodGet, "/v1/conversations/"+own.protocol, narrow, false)
	if missingAgain.Code != http.StatusForbidden {
		t.Fatalf("escopo não pode virar limite: %d", missingAgain.Code)
	}

	reader, _, err := IssueScoped(ctx, testStore, own.tenant, []string{"conversations.read"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	foreign := call(http.MethodGet, "/v1/conversations/"+other.protocol, reader, false)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("outro tenant %d %s", foreign.Code, foreign.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodPost, "/api/panel/api-tokens/"+created.ID.String()+"/revoke", nil)
	revokeReq.Header.Set("Origin", "https://panel.example")
	revokeReq.Header.Set(auth.CSRFHeader, "csrf")
	revokeReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sessao"})
	revokeRec := httptest.NewRecorder()
	panel.ServeHTTP(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revogação %d %s", revokeRec.Code, revokeRec.Body.String())
	}
	revoked := call(http.MethodGet, "/v1/contacts", created.Secret, false)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revogado %d %s", revoked.Code, revoked.Body.String())
	}
}

type stubTokenAuth struct{ principal auth.Principal }

func (s stubTokenAuth) Authenticate(context.Context, string, string, bool) (auth.Principal, error) {
	return s.principal, nil
}
