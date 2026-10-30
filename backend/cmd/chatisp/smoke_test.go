package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/channel/configuration"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/runtime"
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
	if err := ensureSmokeJobsRole(ctx, env.AdminURL); err != nil {
		t.Fatal(err)
	}
	jobsURL, err := withJobsUser(urls.App, "chatisp_jobs", "teste-jobs")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	migrateCfg, err := config.Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=" + urls.Owner,
		"CHATISP_MIGRATIONS_DIR=" + migrations,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, migrateCfg, nil); err != nil {
		t.Fatal(err)
	}

	const (
		email    = "fumaca.local@example.com"
		password = "senha-correta-10"
	)
	if err := seedSmokeUser(ctx, urls, email, password); err != nil {
		t.Fatal(err)
	}

	const (
		smokeAppID     = "smoke-app"
		smokeAppSecret = "smoke-app-secret-12345"
		smokePhoneID   = "5511999990001"
		smokeWABAID    = "smoke-waba"
		smokeVerifyTok = "smoke-verify-token"
	)
	if err := seedSmokeMetaBox(ctx, urls, smokeAppID, smokeWABAID, smokePhoneID, smokeAppSecret, "smoke-token-12345"); err != nil {
		t.Fatal(err)
	}
	const secondAppID, secondSecret = "smoke-app-b", "smoke-app-b-secret"
	if err := seedSmokeMetaBox(ctx, urls, secondAppID, "smoke-waba-b", "5511999990002", secondSecret, "smoke-token-b"); err != nil {
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
			"CHATISP_APP_DATABASE_URL=" + urls.App,
			"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
			"CHATISP_INGRESS_DATABASE_URL=" + urls.Ingress,
			"CHATISP_JOBS_DATABASE_URL=" + jobsURL,
			"CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=",
			"CHATISP_DATA_KEY_VERSION=1",
			"CHATISP_META_SIMULATION=true",
			"CHATISP_META_VERIFY_TOKEN=" + smokeVerifyTok,
			"CHATISP_META_APP_ID=" + smokeAppID,
			"CHATISP_MIGRATIONS_DIR=" + migrations,
			"CHATISP_PANEL_ORIGINS=" + origin,
			"CHATISP_LOG_LEVEL=error",
			"CHATISP_SHUTDOWN_TIMEOUT=2s",
		}, io.Discard, io.Discard)
	}()

	base := "http://127.0.0.1:" + httpPort
	client := &http.Client{Timeout: 2 * time.Second}
	waitHealthy(t, ctx, errCh, client, base+"/healthz", "ok")
	waitHealthy(t, ctx, errCh, client, base+"/readyz", "ready")

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

	// 1. Teste de GET challenge no webhook (com token correto e rejeição com token errado)
	challengeURL := fmt.Sprintf("%s/api/webhooks/meta?hub.mode=subscribe&hub.verify_token=%s&hub.challenge=teste-challenge-123&app_id=%s", base, smokeVerifyTok, smokeAppID)
	reqChallenge, err := http.NewRequestWithContext(ctx, http.MethodGet, challengeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	respChallenge, err := client.Do(reqChallenge)
	if err != nil {
		t.Fatal(err)
	}
	defer respChallenge.Body.Close()
	challengeBody, _ := io.ReadAll(respChallenge.Body)
	if respChallenge.StatusCode != http.StatusOK || string(challengeBody) != "teste-challenge-123" {
		t.Fatalf("webhook GET challenge: status %d, body %s", respChallenge.StatusCode, challengeBody)
	}

	badChallengeURL := fmt.Sprintf("%s/api/webhooks/meta?hub.mode=subscribe&hub.verify_token=wrong-token&hub.challenge=teste-challenge-123&app_id=%s", base, smokeAppID)
	reqBadChallenge, err := http.NewRequestWithContext(ctx, http.MethodGet, badChallengeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	respBadChallenge, err := client.Do(reqBadChallenge)
	if err != nil {
		t.Fatal(err)
	}
	respBadChallenge.Body.Close()
	if respBadChallenge.StatusCode != http.StatusUnauthorized {
		t.Fatalf("webhook GET challenge com token errado: esperava 401, obteve %d", respBadChallenge.StatusCode)
	}

	// 2. Teste de POST assinado com HMAC SHA-256 no webhook (wiring real)
	secondBody := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	secondMAC := hmac.New(sha256.New, []byte(secondSecret))
	secondMAC.Write(secondBody)
	secondReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/webhooks/meta?app_id="+secondAppID, bytes.NewReader(secondBody))
	if err != nil {
		t.Fatal(err)
	}
	secondReq.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(secondMAC.Sum(nil)))
	secondResp, err := client.Do(secondReq)
	if err != nil {
		t.Fatal(err)
	}
	secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("app B: status %d", secondResp.StatusCode)
	}
	secondPool, err := pgxpool.New(ctx, urls.Ingress)
	if err != nil {
		t.Fatal(err)
	}
	var secondCount int
	err = secondPool.QueryRow(ctx, `SELECT count(*) FROM ingress_deliveries WHERE app_id = $1`, secondAppID).Scan(&secondCount)
	secondPool.Close()
	if err != nil || secondCount == 0 {
		t.Fatalf("app B não persistido: %d %v", secondCount, err)
	}
	badSecond, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/webhooks/meta?app_id="+secondAppID, bytes.NewReader(secondBody))
	if err != nil {
		t.Fatal(err)
	}
	badSecond.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	badSecondResp, err := client.Do(badSecond)
	if err != nil {
		t.Fatal(err)
	}
	badSecondResp.Body.Close()
	if badSecondResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("assinatura inválida app B: %d", badSecondResp.StatusCode)
	}
	rawWebhookBody := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "` + smokeWABAID + `",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {
						"display_phone_number": "` + smokePhoneID + `",
						"phone_number_id": "` + smokePhoneID + `"
					},
					"messages": [{
						"from": "5511988887777",
						"id": "wamid.smoke-real-123",
						"timestamp": "1700000000",
						"text": {
							"body": "Mensagem webhook via wiring real"
						},
						"type": "text"
					}]
				},
				"field": "messages"
			}]
		}]
	}`)

	mac := hmac.New(sha256.New, []byte(smokeAppSecret))
	mac.Write(rawWebhookBody)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// Rejeição com assinatura HMAC incorreta
	reqBadSig, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/webhooks/meta?app_id="+smokeAppID, bytes.NewReader(rawWebhookBody))
	if err != nil {
		t.Fatal(err)
	}
	reqBadSig.Header.Set("Content-Type", "application/json")
	reqBadSig.Header.Set("X-Hub-Signature-256", "sha256=invalid0000000000000000000000000000000000000000000000000000000000")
	respBadSig, err := client.Do(reqBadSig)
	if err != nil {
		t.Fatal(err)
	}
	respBadSig.Body.Close()
	if respBadSig.StatusCode != http.StatusUnauthorized {
		t.Fatalf("webhook POST com HMAC errado: esperava 401, obteve %d", respBadSig.StatusCode)
	}

	// Aceitação e persistência com assinatura HMAC válida
	reqPost, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/webhooks/meta?app_id="+smokeAppID, bytes.NewReader(rawWebhookBody))
	if err != nil {
		t.Fatal(err)
	}
	reqPost.Header.Set("Content-Type", "application/json")
	reqPost.Header.Set("X-Hub-Signature-256", validSig)
	respPost, err := client.Do(reqPost)
	if err != nil {
		t.Fatal(err)
	}
	defer respPost.Body.Close()
	if respPost.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(respPost.Body)
		t.Fatalf("webhook POST com HMAC válido: esperava 200, obteve %d (%s)", respPost.StatusCode, respBody)
	}

	// 3. Verificar persistência em ingress_deliveries e processamento assíncrono pelo worker River
	ingressPool, err := pgxpool.New(ctx, urls.Ingress)
	if err != nil {
		t.Fatal(err)
	}
	defer ingressPool.Close()

	var deliveryID uuid.UUID
	err = ingressPool.QueryRow(ctx, `
		SELECT id FROM ingress_deliveries WHERE app_id = $1 ORDER BY received_at DESC LIMIT 1
	`, smokeAppID).Scan(&deliveryID)
	if err != nil {
		t.Fatalf("falha ao localizar entrega persistida pelo webhook real: %v", err)
	}

	var finalState string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err := ingressPool.QueryRow(ctx, `
			SELECT state::text FROM river.river_job
			WHERE args::text LIKE '%' || $1 || '%'
			ORDER BY id DESC LIMIT 1
		`, deliveryID.String()).Scan(&state); err == nil {
			finalState = state
			if state == "completed" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalState != "completed" {
		t.Fatalf("job River para entrega %s não completou, estado final: %s", deliveryID, finalState)
	}

	waitHealthy(t, ctx, errCh, client, base+"/readyz", "ready")

	stop()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("o modo all não encerrou")
	}
	apiCtx, stopAPI := context.WithCancel(ctx)
	apiDone := make(chan error, 1)
	apiPort, apiMetricsPort := freePort(t), freePort(t)
	go func() {
		apiDone <- run(apiCtx, []string{"api"}, []string{
			"CHATISP_ENV=development", "CHATISP_HTTP_ADDR=127.0.0.1:" + apiPort,
			"CHATISP_METRICS_ADDR=127.0.0.1:" + apiMetricsPort,
			"CHATISP_APP_DATABASE_URL=" + urls.App, "CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
			"CHATISP_INGRESS_DATABASE_URL=" + urls.Ingress,
			"CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=", "CHATISP_DATA_KEY_VERSION=1",
			"CHATISP_META_VERIFY_TOKEN=" + smokeVerifyTok, "CHATISP_LOG_LEVEL=error",
		}, io.Discard, io.Discard)
	}()
	waitHealthy(t, ctx, apiDone, client, "http://127.0.0.1:"+apiPort+"/readyz", "ready")
	stopAPI()
	if err := <-apiDone; err != nil {
		t.Fatalf("api sem owner: %v", err)
	}

	// An active River job must retain its pool until cancellation is observed
	// and River has persisted its retry state.
	pool, err := pgxpool.New(ctx, jobsURL)
	if err != nil {
		t.Fatal(err)
	}
	active := &smokeBlockedIngress{started: make(chan struct{}), release: make(chan struct{})}
	worker, err := jobs.New(pool, jobs.Handlers{Ingress: active}, jobs.WithTestTiming(100*time.Millisecond, nil, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	metricsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- runtime.Run(workerCtx, runtime.Options{Mode: config.ModeWorker, Config: config.Config{ShutdownTimeout: 500 * time.Millisecond}, Logger: logging.New("error", io.Discard), MetricsListener: metricsListener, JobsClient: worker, Cleanup: func() { pool.Close(); close(cleaned) }})
	}()
	producer, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := ingressPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := producer.EnqueueIngress(ctx, tx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.started:
	case <-ctx.Done():
		t.Fatal("job não iniciou")
	}
	cancelWorker()
	time.Sleep(350 * time.Millisecond) // force timeout has elapsed; final confirmation is pending
	select {
	case <-cleaned:
		t.Fatal("pool fechado com job ativo")
	case <-workerDone:
		t.Fatal("runtime retornou antes da confirmação do River")
	default:
	}
	close(active.release)
	if err = <-workerDone; err == nil {
		t.Fatal("timeout de job ativo deve retornar erro")
	}
	select {
	case <-cleaned:
	case <-time.After(10 * time.Second):
		t.Fatal("worker não confirmou parada")
	}
	var state string
	if err = ingressPool.QueryRow(ctx, `SELECT state::text FROM river.river_job WHERE id=$1`, jobID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "completed" || state == "discarded" || state == "cancelled" || state == "running" {
		t.Fatalf("estado após interrupção: %s", state)
	}
}

type smokeBlockedIngress struct {
	started chan struct{}
	release chan struct{}
}

func (h *smokeBlockedIngress) ExpandIngress(ctx context.Context, _ uuid.UUID) error {
	close(h.started)
	<-ctx.Done()
	<-h.release
	return ctx.Err()
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

func seedSmokeMetaBox(ctx context.Context, urls db.RoleURLs, appID, wabaID, phoneID, appSecret, token string) error {
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		return err
	}
	defer pools.Close()

	ring, err := crypto.Load("1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=", "", "1")
	if err != nil {
		return err
	}
	store := db.NewStore(pools.App, pools.Platform)

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
		_, err := store.CreateUser(ctx, db.NewUser{
			ID:     userID,
			Email:  fmt.Sprintf("admin-%s@example.com", userID),
			Status: "active",
		})
		return err
	}); err != nil {
		return err
	}

	if err := store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := store.CreateMembership(ctx, db.NewMembership{
			ID:     membershipID,
			UserID: userID,
			Role:   "admin",
			Status: "active",
		})
		return err
	}); err != nil {
		return err
	}

	sessionID := uuid.New()
	tokenHash := make([]byte, 32)
	csrfHash := make([]byte, 32)
	if _, err := rand.Read(tokenHash); err != nil {
		return err
	}
	if _, err := rand.Read(csrfHash); err != nil {
		return err
	}
	now := time.Now().UTC()

	if err := store.WithAuth(ctx, func(ctx context.Context) error {
		sealed, err := ring.Encrypt([]byte("totp-smoke-test"), crypto.Context{
			TenantID: tenantID.String(),
			Entity:   "user",
			EntityID: userID.String(),
			Field:    "totp_secret",
		})
		if err != nil {
			return err
		}
		if err := store.BeginMFA(ctx, userID, tenantID, sealed.Ciphertext, sealed.Version); err != nil {
			return err
		}
		if ok, err := store.ConfirmMFA(ctx, userID, now); err != nil || !ok {
			return fmt.Errorf("falha ao confirmar MFA: %w", err)
		}
		if err := store.InsertSession(ctx, db.NewSession{
			ID:                sessionID,
			TokenHash:         tokenHash,
			UserID:            userID,
			TenantID:          tenantID,
			MembershipID:      membershipID,
			CSRFHash:          csrfHash,
			Now:               now,
			IdleExpiresAt:     now.Add(time.Hour),
			AbsoluteExpiresAt: now.Add(2 * time.Hour),
		}); err != nil {
			return err
		}
		return store.SetSessionAssurance(ctx, sessionID, "full")
	}); err != nil {
		return err
	}

	channelSvc, err := configuration.New(store, pools.Ingress, ring)
	if err != nil {
		return err
	}

	_, err = channelSvc.Configure(ctx, tokenHash, configuration.Manual{
		InboxName:     "Caixa Smoke",
		DisplayNumber: "+" + phoneID,
		AppID:         appID,
		WABAID:        wabaID,
		PhoneNumberID: phoneID,
		AppSecret:     appSecret,
		AccessToken:   token,
	})
	return err
}

func waitHealthy(t *testing.T, ctx context.Context, errCh <-chan error, client *http.Client, url, expectedBody string) {
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
			if response.StatusCode == http.StatusOK && (expectedBody == "" || bytes.Contains(body, []byte(expectedBody))) {
				return
			}
		}
		select {
		case err := <-errCh:
			t.Fatalf("modo all encerrou antes de %s: %v", url, err)
		case <-deadline.C:
			t.Fatalf("%s não respondeu", url)
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

func ensureSmokeJobsRole(ctx context.Context, adminURL string) error {
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

func withJobsUser(rawURL, user, pass string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(user, pass)
	return u.String(), nil
}
