package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/testdb"
)

var (
	panelEnv   *testdb.Env
	panelPools *db.Pools
	panelStore *db.Store
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := ensureTestJobsRole(ctx, env.AdminURL); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao criar papel chatisp_jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	panelEnv = env
	panelPools = pools
	panelStore = db.NewStore(pools.App, pools.Platform)
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type panelClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *panelClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *panelClock) Advance(step time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(step)
	c.mu.Unlock()
}

func TestPanelRejectsBadOriginCSRFAndRevokedSession(t *testing.T) {
	clk := &panelClock{at: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)}
	var logs bytes.Buffer
	box := &mail.Fake{}
	policy := auth.DefaultPolicy
	policy.LockoutThreshold = 3
	policy.LockoutBase = time.Minute
	policy.LockoutWindow = 15 * time.Minute
	svc, err := auth.New(auth.Options{
		Store:  panelStore,
		Mail:   box,
		Logger: logging.New("info", &logs),
		Policy: policy,
		Now:    clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	password := "senha-correta-10"
	admin := seedPanelAdmin(t, password)
	api := New(Dependencies{Auth: svc, Logger: logging.New("info", &logs)})

	evil := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.10", map[string]string{
		"email": admin.email, "password": password,
	}, "", "", "https://evil.example", "")
	if evil.Code != http.StatusForbidden || !strings.Contains(evil.Body.String(), "origem não permitida") {
		t.Fatalf("origem: %d %s", evil.Code, evil.Body.String())
	}

	for range 3 {
		bad := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.20", map[string]string{
			"email": admin.email, "password": "senha-errada-10",
		}, "", "", "https://painel.chatisp.test", "")
		if bad.Code != http.StatusUnauthorized {
			t.Fatalf("tentativa: %d %s", bad.Code, bad.Body.String())
		}
	}
	locked := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.20", map[string]string{
		"email": admin.email, "password": password,
	}, "", "", "https://painel.chatisp.test", "")
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("bloqueio: %d %s", locked.Code, locked.Body.String())
	}
	clk.Advance(time.Minute)

	login := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.21", map[string]string{
		"email": admin.email, "password": password,
	}, "", "", "https://painel.chatisp.test", "")
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	sessionCookie, csrfCookie := panelCookies(t, login)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CSRFToken == "" || body.CSRFToken != csrfCookie || strings.Contains(login.Body.String(), sessionCookie) {
		t.Fatal("resposta expôs o cookie de sessão ou omitiu o CSRF")
	}
	if login.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %s", login.Header().Get("Cache-Control"))
	}

	current := panelCall(api.Handler, http.MethodGet, "/api/panel/session", "198.51.100.22", nil, sessionCookie, "", "https://painel.chatisp.test", "")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), admin.email) {
		t.Fatalf("sessão: %d %s", current.Code, current.Body.String())
	}

	guest := uuid.NewString() + "@example.com"
	missing := panelCall(api.Handler, http.MethodPost, "/api/panel/invitations", "198.51.100.23", map[string]string{
		"email": guest, "role": "agent",
	}, sessionCookie, "", "https://painel.chatisp.test", "")
	if missing.Code != http.StatusForbidden {
		t.Fatalf("sem csrf: %d %s", missing.Code, missing.Body.String())
	}
	cross := panelCall(api.Handler, http.MethodPost, "/api/panel/invitations", "198.51.100.24", map[string]string{
		"email": guest, "role": "agent",
	}, sessionCookie, body.CSRFToken, "https://painel.chatisp.test", "cross-site")
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d %s", cross.Code, cross.Body.String())
	}
	created := panelCall(api.Handler, http.MethodPost, "/api/panel/invitations", "198.51.100.25", map[string]string{
		"email": guest, "role": "agent",
	}, sessionCookie, body.CSRFToken, "https://painel.chatisp.test", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("convite: %d %s", created.Code, created.Body.String())
	}
	if len(box.Snapshot()) != 1 || strings.Contains(created.Body.String(), mailToken(box.Snapshot()[0].Text)) {
		t.Fatal("resposta do convite devolveu o token")
	}

	logout := panelCall(api.Handler, http.MethodDelete, "/api/panel/session", "198.51.100.26", nil, sessionCookie, body.CSRFToken, "https://painel.chatisp.test", "")
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	revoked := panelCall(api.Handler, http.MethodGet, "/api/panel/session", "198.51.100.27", nil, sessionCookie, "", "https://painel.chatisp.test", "")
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("sessão revogada: %d %s", revoked.Code, revoked.Body.String())
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), "senha-errada-10") || strings.Contains(logs.String(), sessionCookie) || strings.Contains(logs.String(), body.CSRFToken) {
		t.Fatal("log expôs segredo")
	}
	_ = panelEnv
	_ = panelPools
}

func TestProviderAuditRouteNeedsAuth(t *testing.T) {
	api := New(Dependencies{})
	response := panelCall(api.Handler, http.MethodGet, "/api/panel/audit?from=2026-11-01&to=2026-11-03", "198.51.100.81", nil, "", "", "https://painel.chatisp.test", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
}

func TestPanelWithoutAuthIsUnavailable(t *testing.T) {
	api := New(Dependencies{})
	response := panelCall(api.Handler, http.MethodPost, "/api/panel/session", "198.51.100.80", map[string]string{
		"email": "pessoa@example.com", "password": "senha-correta-10",
	}, "", "", "https://painel.chatisp.test", "")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "senha-correta-10") {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
}

type panelAccount struct {
	email string
}

func seedPanelAdmin(t *testing.T, password string) panelAccount {
	t.Helper()
	userID := uuid.New()
	item := panelAccount{email: userID.String() + "@example.com"}
	tenantID := uuid.New()
	ctx := context.Background()
	err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := panelStore.CreateTenant(ctx, db.NewTenant{
			ID: tenantID, Timezone: "America/Sao_Paulo", Status: "active",
		}); err != nil {
			return err
		}
		_, err := panelStore.CreateUser(ctx, db.NewUser{ID: userID, Email: item.email, Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = panelStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := panelStore.CreateMembership(ctx, db.NewMembership{UserID: userID, Role: "admin", Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	err = panelStore.WithAuth(ctx, func(ctx context.Context) error {
		return panelStore.SetPasswordHash(ctx, userID, hash)
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func panelCall(handler http.Handler, method, path, ip string, body any, cookie, csrf, origin, site string) *httptest.ResponseRecorder {
	var payload io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, payload)
	req.Host = "painel.chatisp.test"
	req.RemoteAddr = ip + ":4242"
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	if csrf != "" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func panelCookies(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var session, csrf string
	for _, line := range rec.Header().Values("Set-Cookie") {
		parsed, err := http.ParseSetCookie(line)
		if err != nil {
			t.Fatal(err)
		}
		switch parsed.Name {
		case auth.SessionCookieName:
			session = parsed.Value
			if !parsed.HttpOnly || !parsed.Secure || parsed.Path != "/" || parsed.SameSite != http.SameSiteLaxMode || parsed.Domain != "" {
				t.Fatalf("cookie de sessão: %#v", parsed)
			}
		case auth.CSRFCookieName:
			csrf = parsed.Value
			if parsed.HttpOnly || !parsed.Secure {
				t.Fatalf("cookie de csrf: %#v", parsed)
			}
		}
	}
	if session == "" || csrf == "" {
		t.Fatal("cookies ausentes")
	}
	return session, csrf
}

func mailToken(text string) string {
	var last string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			last = line
		}
	}
	return last
}

func ensureTestJobsRole(ctx context.Context, adminURL string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'chatisp_jobs')`).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format('%s ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text, 'teste-jobs')
	`, verb).Scan(&stmt); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return err
	}
	for _, query := range []string{
		`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`,
	} {
		var grant string
		if err := conn.QueryRow(ctx, query).Scan(&grant); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, grant); err != nil {
			return err
		}
	}
	return nil
}
