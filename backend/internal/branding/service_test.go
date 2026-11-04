package branding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/tenantcache"
	"chatisp/internal/platform/testdb"
)

var (
	testStore *db.Store
	testEnv   *testdb.Env
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	testEnv = env
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
	testStore = db.NewStore(pools.App, pools.Platform)
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type seeded struct {
	tenant     uuid.UUID
	other      uuid.UUID
	admin      Principal
	lead       Principal
	agent      Principal
	otherAdmin Principal
}

func seed(t *testing.T) seeded {
	t.Helper()
	ctx := context.Background()
	s := seeded{tenant: uuid.New(), other: uuid.New()}
	adminUser, leadUser, agentUser, otherUser := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s.admin = Principal{TenantID: s.tenant, UserID: adminUser, MembershipID: uuid.New(), Role: "admin"}
	s.lead = Principal{TenantID: s.tenant, UserID: leadUser, MembershipID: uuid.New(), Role: "supervisor"}
	s.agent = Principal{TenantID: s.tenant, UserID: agentUser, MembershipID: uuid.New(), Role: "agent"}
	s.otherAdmin = Principal{TenantID: s.other, UserID: otherUser, MembershipID: uuid.New(), Role: "admin"}
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		for _, tenant := range []uuid.UUID{s.tenant, s.other} {
			if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
				return err
			}
		}
		for _, user := range []struct {
			id   uuid.UUID
			mail string
		}{
			{adminUser, "admin-" + adminUser.String() + "@example.com"},
			{leadUser, "lead-" + leadUser.String() + "@example.com"},
			{agentUser, "agent-" + agentUser.String() + "@example.com"},
			{otherUser, "other-" + otherUser.String() + "@example.com"},
		} {
			if _, err := testStore.CreateUser(ctx, db.NewUser{ID: user.id, Email: user.mail, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []Principal{s.admin, s.lead, s.agent} {
		if err := testStore.WithTenant(ctx, s.tenant, func(ctx context.Context) error {
			_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: member.MembershipID, UserID: member.UserID, Role: member.Role, Status: "active"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := testStore.WithTenant(ctx, s.other, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: s.otherAdmin.MembershipID, UserID: s.otherAdmin.UserID, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNamePreservesDeadlinesAndLogoStaysInTenant(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	svc := Service{DB: testStore}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_retention_profiles (
				tenant_id, display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
			) VALUES ($1, 'Antigo', 11, 22, 33, 44, 55, false)
		`, s.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.SaveName(ctx, s.admin, "  Nexo Fibra  ")
	if err != nil || saved.DisplayName != "Nexo Fibra" || saved.FallbackName != FallbackName || saved.HasLogo {
		t.Fatalf("nome: %+v %v", saved, err)
	}
	var name string
	var content, media, envelope, logDays, job int
	var purge bool
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
			FROM tenant_retention_profiles WHERE tenant_id = $1
		`, s.tenant).Scan(&name, &content, &media, &envelope, &logDays, &job, &purge)
	}); err != nil {
		t.Fatal(err)
	}
	if name != "Nexo Fibra" || content != 11 || media != 22 || envelope != 33 || logDays != 44 || job != 55 || purge {
		t.Fatalf("prazos alterados: %s %d %d %d %d %d %v", name, content, media, envelope, logDays, job, purge)
	}
	if _, err := svc.SaveLogo(ctx, s.agent, "image/png", pngBytes(4, 4)); err == nil {
		t.Fatal("atendente gravou logo")
	}
	if _, err := svc.SaveName(ctx, s.admin, strings.Repeat("a", MaxNameRunes+1)); err == nil {
		t.Fatal("nome longo")
	}
	logo, err := svc.SaveLogo(ctx, s.admin, "image/png", append(pngBytes(5, 6), []byte("<script>no</script>")...))
	if err != nil || !logo.HasLogo || logo.ContentType != "image/png" || logo.Width != 5 || logo.Height != 6 || logo.DisplayName != "Nexo Fibra" {
		t.Fatalf("logo: %+v %v", logo, err)
	}
	raw, err := svc.Logo(ctx, s.agent)
	if err != nil || !Matches("image/png", raw.Bytes) || bytes.Contains(raw.Bytes, []byte("<script>")) || raw.Width != 5 {
		t.Fatalf("leitura do logo: %+v %v", raw.ContentType, err)
	}
	other, err := svc.Get(ctx, s.otherAdmin)
	if err != nil || other.HasLogo || other.DisplayName != "" {
		t.Fatalf("outro provedor viu identidade: %+v %v", other, err)
	}
	if _, err := svc.Logo(ctx, s.otherAdmin); err == nil {
		t.Fatal("outro provedor leu o logo")
	}
	if err := testStore.WithTenantTx(ctx, s.other, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenant_provider_logos`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("RLS cruzado: %d", n)
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT cross_tenant`); err != nil {
			return err
		}
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO tenant_provider_logos (tenant_id, content_type, image, width, height, byte_size)
			VALUES ($1, 'image/png', $2, 1, 1, octet_length($2))
		`, s.tenant, pngBytes(1, 1))
		if insertErr == nil {
			t.Fatal("gravou logo em outro provedor")
		}
		_, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT cross_tenant`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', '', true)`); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenant_provider_logos`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("sem tenant: %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := svc.RemoveLogo(ctx, s.admin)
	if err != nil || cleared.HasLogo || cleared.DisplayName != "Nexo Fibra" {
		t.Fatalf("remoção: %+v %v", cleared, err)
	}
	if _, err := svc.Logo(ctx, s.admin); err == nil {
		t.Fatal("logo permaneceu")
	}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var content int
		var purge bool
		var actions string
		if err := tx.QueryRow(ctx, `
			SELECT content_days, automatic_purge FROM tenant_retention_profiles WHERE tenant_id = $1
		`, s.tenant).Scan(&content, &purge); err != nil {
			return err
		}
		if content != 11 || purge {
			t.Fatalf("remoção mexeu na retenção: %d %v", content, purge)
		}
		if err := tx.QueryRow(ctx, `
			SELECT coalesce(string_agg(action || ' ' || result, ' '), '')
			FROM audit_logs WHERE tenant_id = $1 AND action LIKE 'branding.%'
		`, s.tenant).Scan(&actions); err != nil {
			return err
		}
		if !strings.Contains(actions, "branding.name") || !strings.Contains(actions, "branding.logo.replace") || !strings.Contains(actions, "branding.logo.remove") || strings.Contains(actions, "<script>") || strings.Contains(actions, "http") {
			t.Fatalf("auditoria: %s", actions)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	super := s.admin
	super.IsSuperadmin = true
	if _, err := svc.Get(ctx, super); err == nil {
		t.Fatal("superadmin leu a identidade")
	}
	enroll := s.admin
	enroll.EnrollmentOnly = true
	if _, err := svc.SaveLogo(ctx, enroll, "image/png", pngBytes(2, 2)); err == nil {
		t.Fatal("admin sem MFA completo gravou logo")
	}
}

func TestLogoTableIsForcedAndMinimallyGranted(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var forced bool
	if err := conn.QueryRow(ctx, `
		SELECT c.relrowsecurity AND c.relforcerowsecurity
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = 'tenant_provider_logos'
	`).Scan(&forced); err != nil {
		t.Fatal(err)
	}
	if !forced {
		t.Fatal("RLS sem FORCE")
	}
	var appSelect, appInsert, appUpdate, appDelete bool
	var platformSelect, ingressSelect, publicSelect bool
	if err := conn.QueryRow(ctx, `
		SELECT
			has_table_privilege('chatisp_app', 'public.tenant_provider_logos', 'SELECT'),
			has_table_privilege('chatisp_app', 'public.tenant_provider_logos', 'INSERT'),
			has_table_privilege('chatisp_app', 'public.tenant_provider_logos', 'UPDATE'),
			has_table_privilege('chatisp_app', 'public.tenant_provider_logos', 'DELETE'),
			has_table_privilege('chatisp_platform', 'public.tenant_provider_logos', 'SELECT'),
			has_table_privilege('chatisp_ingress', 'public.tenant_provider_logos', 'SELECT'),
			has_table_privilege('public', 'public.tenant_provider_logos', 'SELECT')
	`).Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &platformSelect, &ingressSelect, &publicSelect); err != nil {
		t.Fatal(err)
	}
	if !appSelect || !appInsert || !appUpdate || !appDelete || platformSelect || ingressSelect || publicSelect {
		t.Fatalf("grants app=%v/%v/%v/%v platform=%v ingress=%v public=%v", appSelect, appInsert, appUpdate, appDelete, platformSelect, ingressSelect, publicSelect)
	}
}

func TestHTTPRolesMFAOriginAndImageHeaders(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_retention_profiles (
				tenant_id, display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
			) VALUES ($1, 'Antigo', 7, 8, 9, 10, 11, false)
		`, s.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	router := mountAs(s.admin, false)
	bad := request(http.MethodPut, "/api/panel/branding", `{"display_name":"Nova","automatic_purge":true}`, true)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expurgo pelo nome: %d %s", rec.Code, rec.Body.String())
	}
	noOrigin := request(http.MethodPut, "/api/panel/branding", `{"display_name":"Nova"}`, false)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, noOrigin)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sem origem: %d", rec.Code)
	}
	noCSRF := request(http.MethodPut, "/api/panel/branding", `{"display_name":"Nova"}`, true)
	noCSRF.Header.Del("X-CSRF-Token")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, noCSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sem csrf: %d", rec.Code)
	}
	ok := request(http.MethodPut, "/api/panel/branding", `{"display_name":"Nova Fibra"}`, true)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, ok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"display_name":"Nova Fibra"`) || strings.Contains(rec.Body.String(), "content_days") {
		t.Fatalf("nome http: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("cabeçalhos json: %v", rec.Header())
	}
	logoReq := requestBytes(http.MethodPut, "/api/panel/branding/logo", "image/png", pngBytes(4, 3))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, logoReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"has_logo":true`) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	svg := request(http.MethodPut, "/api/panel/branding/logo", "<svg></svg>", true)
	svg.Header.Set("Content-Type", "image/svg+xml")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, svg)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("svg: %d %s", rec.Code, rec.Body.String())
	}
	getLogo := request(http.MethodGet, "/api/panel/branding/logo", "", false)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, getLogo)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.HasPrefix(rec.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("imagem: %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	agentRouter := mountAs(s.agent, false)
	agentGet := request(http.MethodGet, "/api/panel/branding", "", false)
	rec = httptest.NewRecorder()
	agentRouter.ServeHTTP(rec, agentGet)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Nova Fibra") {
		t.Fatalf("atendente lê: %d %s", rec.Code, rec.Body.String())
	}
	agentPut := request(http.MethodDelete, "/api/panel/branding/logo", "", true)
	rec = httptest.NewRecorder()
	agentRouter.ServeHTTP(rec, agentPut)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("atendente remove: %d", rec.Code)
	}
	enroll := mountAs(s.admin, true)
	rec = httptest.NewRecorder()
	enroll.ServeHTTP(rec, request(http.MethodGet, "/api/panel/branding", "", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("leitura sem MFA completo: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	enroll.ServeHTTP(rec, request(http.MethodPut, "/api/panel/branding", `{"display_name":"Não"}`, true))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("edição sem MFA completo: %d", rec.Code)
	}
	super := s.admin
	super.IsSuperadmin = true
	superRouter := mountPrincipal(super)
	rec = httptest.NewRecorder()
	superRouter.ServeHTTP(rec, request(http.MethodGet, "/api/panel/branding/logo", "", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("superadmin: %d %s", rec.Code, rec.Body.String())
	}
	anon := chi.NewRouter()
	Mount(anon, Dependencies{Auth: &stubAuth{err: auth.ErrUnauthenticated}, Store: testStore, TrustedOrigins: []string{"https://painel.test"}})
	rec = httptest.NewRecorder()
	anon.ServeHTTP(rec, request(http.MethodGet, "/api/panel/branding", "", false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anônimo: %d", rec.Code)
	}
	del := request(http.MethodDelete, "/api/panel/branding/logo", "", true)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, del)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"has_logo":false`) || !strings.Contains(rec.Body.String(), "Nova Fibra") {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var content, media, envelope, logDays, job int
		var purge bool
		var name string
		if err := tx.QueryRow(ctx, `
			SELECT display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
			FROM tenant_retention_profiles WHERE tenant_id = $1
		`, s.tenant).Scan(&name, &content, &media, &envelope, &logDays, &job, &purge); err != nil {
			return err
		}
		if name != "Nova Fibra" || content != 7 || media != 8 || envelope != 9 || logDays != 10 || job != 11 || purge {
			t.Fatalf("http alterou prazos: %s %d %d %d %d %d %v", name, content, media, envelope, logDays, job, purge)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mountAs(p Principal, enrollment bool) http.Handler {
	p.EnrollmentOnly = enrollment
	return mountPrincipal(p)
}

func mountPrincipal(p Principal) http.Handler {
	router := chi.NewRouter()
	Mount(router, Dependencies{
		Auth:           &stubAuth{p: auth.Principal{UserID: p.UserID, TenantID: p.TenantID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin, EnrollmentOnly: p.EnrollmentOnly}},
		Store:          testStore,
		TrustedOrigins: []string{"https://painel.test"},
	})
	return router
}

type stubAuth struct {
	p   auth.Principal
	err error
}

func (s *stubAuth) Authenticate(_ context.Context, raw, csrf string, require bool) (auth.Principal, error) {
	if s.err != nil {
		return auth.Principal{}, s.err
	}
	if raw == "" {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	if require && csrf == "" {
		return auth.Principal{}, auth.ErrCSRF
	}
	return s.p, nil
}

func request(method, path, body string, origin bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	prepare(req, origin)
	if method == http.MethodPut && path == "/api/panel/branding" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func requestBytes(method, path, contentType string, raw []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	prepare(req, true)
	req.Header.Set("Content-Type", contentType)
	return req
}

func prepare(req *http.Request, origin bool) {
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: "sessao"})
	req.Header.Set("X-CSRF-Token", "csrf")
	if origin {
		req.Header.Set("Origin", "https://painel.test")
	}
}

func TestIdentityCacheServesTheTenantAndDropsOnWrite(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	cache, err := tenantcache.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	svc := Service{DB: testStore, Cache: cache}
	saved, err := svc.SaveName(ctx, s.admin, "Antigo")
	if err != nil || saved.DisplayName != "Antigo" {
		t.Fatalf("gravação: %+v %v", saved, err)
	}
	got, err := svc.Get(ctx, s.admin)
	if err != nil || got.DisplayName != "Antigo" {
		t.Fatalf("primeira leitura: %+v %v", got, err)
	}
	version, ok := tenantcache.PermissionVersion("admin")
	var role string
	if !ok || !cache.Get(s.admin.TenantID.String(), tenantcache.KindPermission, s.admin.MembershipID.String(), version, &role) || role != "admin" {
		t.Fatalf("permissão versionada: %q %v", role, ok)
	}
	if cache.Get(s.admin.TenantID.String(), tenantcache.KindPermission, s.admin.MembershipID.String(), version+1, &role) {
		t.Fatal("versão divergente da permissão")
	}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenant_retention_profiles SET display_name = 'Trocado' WHERE tenant_id = $1`, s.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.Get(ctx, s.admin)
	if err != nil || stale.DisplayName != "Antigo" {
		t.Fatalf("cache não segurou a configuração: %+v %v", stale, err)
	}
	other, err := svc.Get(ctx, s.otherAdmin)
	if err != nil || other.DisplayName == "Antigo" || other.DisplayName == "Trocado" {
		t.Fatalf("outro provedor: %+v %v", other, err)
	}
	renamed, err := svc.SaveName(ctx, s.admin, "Corrigido")
	if err != nil || renamed.DisplayName != "Corrigido" {
		t.Fatalf("invalidação: %+v %v", renamed, err)
	}
	fresh, err := svc.Get(ctx, s.admin)
	if err != nil || fresh.DisplayName != "Corrigido" {
		t.Fatalf("leitura depois da gravação: %+v %v", fresh, err)
	}
	logo, err := svc.Logo(ctx, s.admin)
	if err == nil || len(logo.Bytes) != 0 {
		t.Fatal("o cache da identidade devolveu bytes de logo")
	}
}
