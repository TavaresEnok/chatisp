package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"github.com/jackc/pgx/v5"
)

// TestRoleMatrixByEndpoint checks the fixed section 12 matrix on every panel
// and platform endpoint that a signed-in role can call.
func TestRoleMatrixByEndpoint(t *testing.T) {
	password := "senha-correta-10"
	tenantID := uuid.New()
	ctx := context.Background()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{
			ID: tenantID, Timezone: "America/Sao_Paulo", Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	type signed struct {
		email   string
		user    uuid.UUID
		member  uuid.UUID
		cookie  string
		csrf    string
		session uuid.UUID
	}
	roles := []string{"admin", "supervisor", "agent"}
	signedIn := map[string]signed{}
	for _, role := range roles {
		item := seedMembership(t, tenantID, role, password)
		cookie, csrf, sessionID := panelLogin(t, nil, item.email, password)
		signedIn[role] = signed{email: item.email, user: item.user, member: item.membership, cookie: cookie, csrf: csrf, session: sessionID}
	}
	spare := seedMembership(t, tenantID, "agent", password)
	spareCookie, _, spareSession := panelLogin(t, nil, spare.email, password)

	superTenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{
			ID: superTenant, Timezone: "America/Sao_Paulo", Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	super := seedMembership(t, superTenant, "admin", password)
	markPanelSuperadmin(t, super.user)
	superCookie, superCSRF, _ := panelLogin(t, nil, super.email, password)

	box := &mail.Fake{}
	svc := panelService(t, box)
	api := New(Dependencies{Auth: svc, Logger: logging.New("error", &strings.Builder{})})
	origin := "https://painel.chatisp.test"

	adminOnly := map[string]int{"admin": http.StatusOK, "supervisor": http.StatusForbidden, "agent": http.StatusForbidden}
	type endpoint struct {
		name   string
		method string
		path   string
		body   any
		want   map[string]int
		write  bool
	}
	inviteEmail := uuid.NewString() + "@example.com"
	endpoints := []endpoint{
		{name: "sessão atual", method: http.MethodGet, path: "/api/panel/session", want: map[string]int{"admin": http.StatusOK, "supervisor": http.StatusOK, "agent": http.StatusOK}},
		{name: "lista de usuários", method: http.MethodGet, path: "/api/panel/memberships", want: adminOnly},
		{name: "lista de sessões", method: http.MethodGet, path: "/api/panel/sessions", want: adminOnly},
		{name: "convite", method: http.MethodPost, path: "/api/panel/invitations", body: map[string]string{"email": inviteEmail, "role": "agent"}, want: map[string]int{"admin": http.StatusCreated, "supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
		{name: "cadastro de MFA", method: http.MethodPost, path: "/api/panel/mfa/enrollment", want: map[string]int{"admin": http.StatusServiceUnavailable, "supervisor": http.StatusServiceUnavailable, "agent": http.StatusServiceUnavailable}, write: true},
		{name: "reset de MFA", method: http.MethodPost, path: "/api/panel/mfa/reset", body: map[string]string{"user_id": spare.user.String()}, want: map[string]int{"admin": http.StatusServiceUnavailable, "supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
		{name: "revogar sessão alheia", method: http.MethodPost, path: "/api/panel/sessions/" + spareSession.String() + "/revoke", want: map[string]int{"supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
		{name: "desativar usuário", method: http.MethodPost, path: "/api/panel/memberships/" + spare.membership.String() + "/deactivate", want: map[string]int{"supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
		{name: "reativar usuário", method: http.MethodPost, path: "/api/panel/memberships/" + spare.membership.String() + "/reactivate", want: map[string]int{"supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
		{name: "listar provedores", method: http.MethodGet, path: "/api/platform/tenants", want: map[string]int{"admin": http.StatusForbidden, "supervisor": http.StatusForbidden, "agent": http.StatusForbidden}},
		{name: "criar provedor", method: http.MethodPost, path: "/api/platform/tenants", body: map[string]any{"timezone": "America/Sao_Paulo", "limits": map[string]int{"max_agents": 2}}, want: map[string]int{"admin": http.StatusForbidden, "supervisor": http.StatusForbidden, "agent": http.StatusForbidden}, write: true},
	}

	for _, role := range roles {
		session := signedIn[role]
		for _, ep := range endpoints {
			want, ok := ep.want[role]
			if !ok {
				continue
			}
			csrf := ""
			if ep.write {
				csrf = session.csrf
			}
			response := panelCall(api.Handler, ep.method, ep.path, "198.51.100.40", ep.body, session.cookie, csrf, origin, "")
			if response.Code != want {
				t.Fatalf("%s como %s: %d, esperado %d, corpo %s", ep.name, role, response.Code, want, response.Body.String())
			}
		}
	}

	revoked := panelCall(api.Handler, http.MethodPost, "/api/panel/sessions/"+signedIn["agent"].session.String()+"/revoke", "198.51.100.41", nil, signedIn["agent"].cookie, signedIn["agent"].csrf, origin, "")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("atendente revogando a própria sessão: %d %s", revoked.Code, revoked.Body.String())
	}
	supervisorOwn := panelCall(api.Handler, http.MethodPost, "/api/panel/sessions/"+signedIn["supervisor"].session.String()+"/revoke", "198.51.100.42", nil, signedIn["supervisor"].cookie, signedIn["supervisor"].csrf, origin, "")
	if supervisorOwn.Code != http.StatusNoContent {
		t.Fatalf("supervisor revogando a própria sessão: %d %s", supervisorOwn.Code, supervisorOwn.Body.String())
	}

	deactivated := panelCall(api.Handler, http.MethodPost, "/api/panel/memberships/"+spare.membership.String()+"/deactivate", "198.51.100.43", nil, signedIn["admin"].cookie, signedIn["admin"].csrf, origin, "")
	if deactivated.Code != http.StatusNoContent {
		t.Fatalf("desativar: %d %s", deactivated.Code, deactivated.Body.String())
	}
	lost := panelCall(api.Handler, http.MethodGet, "/api/panel/session", "198.51.100.44", nil, spareCookie, "", origin, "")
	if lost.Code != http.StatusUnauthorized && lost.Code != http.StatusForbidden {
		t.Fatalf("usuário desativado manteve a sessão: %d %s", lost.Code, lost.Body.String())
	}
	again := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.45", map[string]string{
		"email": spare.email, "password": password,
	}, "", "", origin, "")
	if again.Code == http.StatusOK {
		t.Fatalf("usuário desativado entrou de novo: %s", again.Body.String())
	}

	created := panelCall(api.Handler, http.MethodPost, "/api/platform/tenants", "198.51.100.46", map[string]any{
		"timezone": "America/Fortaleza",
		"limits":   map[string]int{"max_agents": 3},
	}, superCookie, superCSRF, origin, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("superadmin cria provedor: %d %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody.Status != "active" || createdBody.ID == uuid.Nil {
		t.Fatalf("corpo: %s", created.Body.String())
	}
	marker := "atendimento-invisivel-" + uuid.NewString()
	if err := panelStore.WithTenant(ctx, createdBody.ID, func(ctx context.Context) error {
		return panelStore.InsertConversation(ctx, uuid.New(), marker)
	}); err != nil {
		t.Fatal(err)
	}
	shown := panelCall(api.Handler, http.MethodGet, "/api/platform/tenants/"+createdBody.ID.String(), "198.51.100.47", nil, superCookie, "", origin, "")
	if shown.Code != http.StatusOK || strings.Contains(shown.Body.String(), marker) {
		t.Fatalf("consulta do superadmin: %d %s", shown.Code, shown.Body.String())
	}
	suspended := panelCall(api.Handler, http.MethodPost, "/api/platform/tenants/"+createdBody.ID.String()+"/suspend", "198.51.100.48", nil, superCookie, superCSRF, origin, "")
	if suspended.Code != http.StatusOK || !strings.Contains(suspended.Body.String(), `"suspended"`) {
		t.Fatalf("suspensão: %d %s", suspended.Code, suspended.Body.String())
	}
	restored := panelCall(api.Handler, http.MethodPost, "/api/platform/tenants/"+createdBody.ID.String()+"/reactivate", "198.51.100.49", nil, superCookie, superCSRF, origin, "")
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"active"`) {
		t.Fatalf("reativação: %d %s", restored.Code, restored.Body.String())
	}
	denied := panelCall(api.Handler, http.MethodGet, "/api/platform/tenants/"+createdBody.ID.String()+"/conversations", "198.51.100.50", nil, superCookie, "", origin, "")
	if denied.Code != http.StatusNotFound || strings.Contains(denied.Body.String(), marker) {
		t.Fatalf("superadmin leu caminho de atendimento: %d %s", denied.Code, denied.Body.String())
	}
}

type seededMember struct {
	email      string
	user       uuid.UUID
	membership uuid.UUID
}

func seedMembership(t *testing.T, tenantID uuid.UUID, role, password string) seededMember {
	t.Helper()
	item := seededMember{user: uuid.New(), membership: uuid.New()}
	item.email = item.user.String() + "@example.com"
	ctx := context.Background()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateUser(ctx, db.NewUser{ID: item.user, Email: item.email, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := panelStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := panelStore.CreateMembership(ctx, db.NewMembership{
			ID: item.membership, UserID: item.user, Role: role, Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := panelStore.WithAuth(ctx, func(ctx context.Context) error {
		return panelStore.SetPasswordHash(ctx, item.user, hash)
	}); err != nil {
		t.Fatal(err)
	}
	return item
}

func panelService(t *testing.T, box mail.Sender) *auth.Service {
	t.Helper()
	svc, err := auth.New(auth.Options{Store: panelStore, Mail: box, Logger: logging.New("error", &strings.Builder{})})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func panelLogin(t *testing.T, svc *auth.Service, email, password string) (string, string, uuid.UUID) {
	t.Helper()
	if svc == nil {
		svc = panelService(t, &mail.Fake{})
	}
	api := New(Dependencies{Auth: svc, Logger: logging.New("error", &strings.Builder{})})
	response := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.60", map[string]string{
		"email": email, "password": password,
	}, "", "", "https://painel.chatisp.test", "")
	if response.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, response.Code, response.Body.String())
	}
	cookie, csrf := panelCookies(t, response)
	var body struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SessionID == uuid.Nil {
		t.Fatal("sessão sem id")
	}
	return cookie, csrf, body.SessionID
}

func markPanelSuperadmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, panelEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
}
