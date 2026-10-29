package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

func TestSmokeAllModeHealthAndLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("PostgreSQL de teste não subiu: %s", err)
	}
	t.Cleanup(env.Close)

	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, migrations); err != nil {
		t.Fatal(err)
	}

	const (
		email    = "fumaca.local@example.com"
		password = "senha-correta-10"
	)
	if err := seedSmokeUser(ctx, urls, email, password); err != nil {
		t.Fatal(err)
	}

	httpPort := freePort(t)
	metricsPort := freePort(t)
	origin := "http://127.0.0.1:" + httpPort
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(runCtx, []string{"all"}, []string{
			"CHATISP_ENV=development",
			"CHATISP_HTTP_ADDR=127.0.0.1:" + httpPort,
			"CHATISP_METRICS_ADDR=127.0.0.1:" + metricsPort,
			"CHATISP_DATABASE_URL=" + urls.Owner,
			"CHATISP_APP_DATABASE_URL=" + urls.App,
			"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
			"CHATISP_MIGRATIONS_DIR=" + migrations,
			"CHATISP_PANEL_ORIGINS=" + origin,
			"CHATISP_LOG_LEVEL=error",
			"CHATISP_SHUTDOWN_TIMEOUT=2s",
		}, io.Discard, io.Discard)
	}()

	base := "http://127.0.0.1:" + httpPort
	client := &http.Client{Timeout: 2 * time.Second}
	waitHealthy(t, ctx, errCh, client, base+"/healthz")

	body := []byte(`{"email":"` + email + `","password":"` + password + `"}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/panel/session", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", response.StatusCode, payload)
	}
	var session struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(payload, &session); err != nil {
		t.Fatal(err)
	}
	if session.Email != email || session.Role != "agent" {
		t.Fatalf("sessão: %+v", session)
	}
	if strings.Contains(string(payload), password) {
		t.Fatal("a resposta de login devolveu a senha")
	}

	stop()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("o modo all não encerrou")
	}
}

func seedSmokeUser(ctx context.Context, urls db.RoleURLs, email, password string) error {
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform})
	if err != nil {
		return err
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	tenantID := uuid.New()
	userID := uuid.New()
	membershipID := uuid.New()
	if err := store.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := store.CreateTenant(ctx, db.NewTenant{
			ID:       tenantID,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
		}); err != nil {
			return err
		}
		_, err := store.CreateUser(ctx, db.NewUser{ID: userID, Email: email, Status: "active"})
		return err
	}); err != nil {
		return err
	}
	if err := store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := store.CreateMembership(ctx, db.NewMembership{
			ID:     membershipID,
			UserID: userID,
			Role:   "agent",
			Status: "active",
		})
		return err
	}); err != nil {
		return err
	}
	return store.WithAuth(ctx, func(ctx context.Context) error {
		return store.SetPasswordHash(ctx, userID, hash)
	})
}

func waitHealthy(t *testing.T, ctx context.Context, errCh <-chan error, client *http.Client, url string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode == http.StatusOK && bytes.Contains(body, []byte(`"ok"`)) {
				return
			}
		}
		select {
		case err := <-errCh:
			t.Fatalf("modo all encerrou antes de /healthz: %v", err)
		case <-deadline.C:
			t.Fatal("/healthz não respondeu")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}
