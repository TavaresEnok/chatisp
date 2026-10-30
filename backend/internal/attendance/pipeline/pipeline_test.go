package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

type recordingDriver struct {
	mu         sync.Mutex
	calls      int
	local      whatsapp.LocalState
	remote     whatsapp.RemoteEvidence
	providerID string
	rejectCode string
	detail     string
}

func (d *recordingDriver) Capabilities() whatsapp.Capabilities {
	return whatsapp.Capabilities{Text: true, DeliveryReceipts: true}
}

func (d *recordingDriver) SendText(_ context.Context, req whatsapp.TextRequest) (whatsapp.Result, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return whatsapp.Result{
		AttemptID:         req.AttemptID,
		Local:             d.local,
		Remote:            d.remote,
		ProviderMessageID: d.providerID,
		RejectionCode:     d.rejectCode,
		Detail:            d.detail,
		Simulation:        true,
	}, nil
}

func (d *recordingDriver) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestWebhook_VerificationAndPersist(t *testing.T) {
	ctx := context.Background()
	appID := "app-webhook-test"
	appSecret := "secret-webhook-test-12345"
	verifyToken := "verify-token-distinct-987"
	phoneID := "5511999990001"
	wabaID := "waba-webhook-test"

	st := seedTenantWithMeta(t, appID, wabaID, phoneID, "token-webhook", appSecret)

	verifyTokens := StaticVerifyTokens{appID: verifyToken}
	handler, err := NewWebhookHandler(WebhookConfig{
		AppID:        appID,
		AppResolver:  testConfigServer,
		VerifyTokens: verifyTokens,
		IngressPool:  testPools.Ingress,
		JobsClient:   testJobs,
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MaxBodyBytes: 1048576,
	})
	if err != nil {
		t.Fatalf("falha ao criar handler: %v", err)
	}

	t.Run("UnresolvedAppRejectsBeforeHMAC", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id=unknown-app", bytes.NewReader([]byte("{}")))
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("esperado 401 para app desconhecido, obteve %d", rec.Code)
		}
	})

	t.Run("GETChallenge_RejectsIfVerifyTokenEqualsAppSecret", func(t *testing.T) {
		badTokens := StaticVerifyTokens{appID: appSecret}
		badHandler, _ := NewWebhookHandler(WebhookConfig{
			AppID:        appID,
			AppResolver:  testConfigServer,
			VerifyTokens: badTokens,
			IngressPool:  testPools.Ingress,
			JobsClient:   testJobs,
			Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
			MaxBodyBytes: 1048576,
		})

		query := url.Values{}
		query.Set("app_id", appID)
		query.Set("hub.mode", "subscribe")
		query.Set("hub.verify_token", appSecret)
		query.Set("hub.challenge", "challenge-123")
		req := httptest.NewRequest(http.MethodGet, "/meta/webhook?"+query.Encode(), nil)
		rec := httptest.NewRecorder()

		badHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("esperado 401 quando verifyToken == AppSecret, obteve %d", rec.Code)
		}
	})

	t.Run("GETChallenge_SuccessWithDistinctToken", func(t *testing.T) {
		query := url.Values{}
		query.Set("app_id", appID)
		query.Set("hub.mode", "subscribe")
		query.Set("hub.verify_token", verifyToken)
		query.Set("hub.challenge", "challenge-pass-456")
		req := httptest.NewRequest(http.MethodGet, "/meta/webhook?"+query.Encode(), nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("esperado 200 para desafio válido, obteve %d", rec.Code)
		}
		if rec.Body.String() != "challenge-pass-456" {
			t.Fatalf("resposta do desafio incorreta: %q", rec.Body.String())
		}
	})

	body := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "%s",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"contacts": [{"profile": {"name": "Cliente"}, "wa_id": "5511988881111"}],
					"messages": [{"from": "5511988881111", "id": "wamid.hook.001", "timestamp": "1700000000", "text": {"body": "Primeiro contato"}, "type": "text"}]
				},
				"field": "messages"
			}]
		}]
	}`, wabaID, phoneID, phoneID))

	t.Run("POST_InvalidHMACFails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("esperado 401 para HMAC inválido, obteve %d", rec.Code)
		}
	})

	t.Run("POST_ValidHMACPersistsAndEnqueues", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", signPayload(appSecret, body))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("esperado 200, obteve %d: %s", rec.Code, rec.Body.String())
		}

		// Verify stored in ingress_deliveries
		var delID uuid.UUID
		var delState string
		err := testPools.Ingress.QueryRow(ctx, `
			SELECT id, state
			FROM ingress_deliveries
			WHERE app_id = $1
		`, appID).Scan(&delID, &delState)
		if err != nil {
			t.Fatalf("delivery não encontrado: %v", err)
		}
		if delState != "stored" {
			t.Fatalf("estado da delivery = %s, esperado stored", delState)
		}

		// Verify River ingress job enqueued
		var jobCount int
		err = testPools.Owner.QueryRow(ctx, `
			SELECT count(*)
			FROM river.river_job
			WHERE queue = 'ingress' AND args->>'delivery_id' = $1
		`, delID.String()).Scan(&jobCount)
		if err != nil {
			t.Fatalf("falha ao consultar jobs River: %v", err)
		}
		if jobCount != 1 {
			t.Fatalf("esperado 1 job River de ingresso, obteve %d", jobCount)
		}
	})

	t.Run("POST_ReplayDoesNotDuplicateJob", func(t *testing.T) {
		var jobCountBefore int
		_ = testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue = 'ingress'`).Scan(&jobCountBefore)

		req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", signPayload(appSecret, body))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("replay deve retornar 200, obteve %d", rec.Code)
		}

		var jobCountAfter int
		_ = testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue = 'ingress'`).Scan(&jobCountAfter)
		if jobCountAfter != jobCountBefore {
			t.Fatalf("replay enfileirou job duplicado: antes %d, depois %d", jobCountBefore, jobCountAfter)
		}
	})

	t.Run("POST_CancelledContextFailsWithoutACK200", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // already cancelled

		newBody := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
		req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(newBody))
		req = req.WithContext(cancelCtx)
		req.Header.Set("X-Hub-Signature-256", signPayload(appSecret, newBody))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatal("contexto cancelado não pode retornar ACK 200")
		}
	})

	t.Run("POST_CommitFailureRollsBackWithoutACK200", func(t *testing.T) {
		failPool, err := pgxpool.New(ctx, "postgres://invalid:invalid@127.0.0.1:1/nonexistent?connect_timeout=1")
		if err == nil {
			defer failPool.Close()
			failHandler, _ := NewWebhookHandler(WebhookConfig{
				AppID:        appID,
				AppResolver:  testConfigServer,
				VerifyTokens: verifyTokens,
				IngressPool:  failPool,
				JobsClient:   testJobs,
				Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
				MaxBodyBytes: 1048576,
			})
			newBody := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"del-fail-001"}]}`)
			req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(newBody))
			req.Header.Set("X-Hub-Signature-256", signPayload(appSecret, newBody))
			rec := httptest.NewRecorder()

			failHandler.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				t.Fatal("falha de persistência/commit não pode emitir ACK 200")
			}
		}
	})
	_ = st
}

func TestExpansion_MultiTenant(t *testing.T) {
	ctx := context.Background()
	appID := "app-multi-a"
	appSecret := "secret-multi-test-999"

	tenantA := seedTenantWithMeta(t, appID, "waba-tenant-a", "5511999990011", "tok-a", appSecret)
	tenantB := seedTenantWithMeta(t, appID, "waba-tenant-b", "5511999990022", "tok-b", appSecret)

	// Use real integrated configuration.Server without artificial mocks.
	ingressWorker := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer)
	inboundWorker := NewInboundHandler(testStore, testAttendanceSvc)

	// Multi-tenant delivery body with items for Tenant A and Tenant B
	envelopeBody := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [
			{
				"id": "waba-tenant-a",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"metadata": {"display_phone_number": "+5511999990011", "phone_number_id": "5511999990011"},
						"contacts": [{"profile": {"name": "Cliente A"}, "wa_id": "5511911111111"}],
						"messages": [{"from": "5511911111111", "id": "wamid.multi.a.001", "timestamp": "1700000010", "text": {"body": "Msg A"}, "type": "text"}]
					},
					"field": "messages"
				}]
			},
			{
				"id": "waba-tenant-b",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"metadata": {"display_phone_number": "+5511999990022", "phone_number_id": "5511999990022"},
						"contacts": [{"profile": {"name": "Cliente B"}, "wa_id": "5511922222222"}],
						"messages": [{"from": "5511922222222", "id": "wamid.multi.b.001", "timestamp": "1700000020", "text": {"body": "Msg B"}, "type": "text"}]
					},
					"field": "messages"
				}]
			}
		]
	}`)

	// Test HTTP Webhook receives multi-tenant envelope, validates HMAC, stores delivery and enqueues ingress job
	webhookHandler, err := NewWebhookHandler(WebhookConfig{
		AppID:        appID,
		AppResolver:  testConfigServer,
		VerifyTokens: StaticVerifyTokens{appID: "distinct-token"},
		IngressPool:  testPools.Ingress,
		JobsClient:   testJobs,
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MaxBodyBytes: 1048576,
	})
	if err != nil {
		t.Fatalf("falha ao criar WebhookHandler: %v", err)
	}

	sig := signPayload(appSecret, envelopeBody)
	req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+appID, bytes.NewReader(envelopeBody))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()

	webhookHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP webhook deveria retornar 200 ACK, obteve %d (%s)", rec.Code, rec.Body.String())
	}

	// Retrieve stored delivery
	sum := sha256.Sum256(envelopeBody)
	var deliveryID uuid.UUID
	err = testPools.Ingress.QueryRow(ctx, `
		SELECT id FROM ingress_deliveries WHERE app_id = $1 AND body_sha256 = $2
	`, appID, sum[:]).Scan(&deliveryID)
	if err != nil {
		t.Fatalf("falha ao localizar delivery gravado pelo webhook HTTP: %v", err)
	}

	// Expand the delivery
	if err := ingressWorker.ExpandIngress(ctx, deliveryID); err != nil {
		t.Fatalf("falha em ExpandIngress: %v", err)
	}

	// 1. Verify delivery state is 'routed'
	var delState string
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&delState)
	if delState != "routed" {
		t.Fatalf("esperado state routed, obteve %s", delState)
	}

	// 2. Verify webhook_items segregated under tenant isolation
	var itemIDA uuid.UUID
	var countA int
	err = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM webhook_items
			WHERE tenant_id = $1 AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		`, tenantA.tenantID).Scan(&countA); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT id FROM webhook_items
			WHERE tenant_id = $1 AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			LIMIT 1
		`, tenantA.tenantID).Scan(&itemIDA)
	})
	if err != nil || countA != 1 {
		t.Fatalf("tenant A deve ver exatamente 1 item, obteve %d (err: %v)", countA, err)
	}

	var itemIDB uuid.UUID
	var countB int
	err = testStore.WithTenantTx(ctx, tenantB.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id = $1`, tenantB.tenantID).Scan(&countB); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id FROM webhook_items WHERE tenant_id = $1 LIMIT 1`, tenantB.tenantID).Scan(&itemIDB)
	})
	if err != nil || countB != 1 {
		t.Fatalf("tenant B deve ver exatamente 1 item: %d (err: %v)", countB, err)
	}
	var qCount int
	if err := testPools.Ingress.QueryRow(ctx, `SELECT count(*) FROM ingress_quarantine WHERE delivery_id = $1`, deliveryID).Scan(&qCount); err != nil || qCount != 0 {
		t.Fatalf("quarentena inesperada: %d (err: %v)", qCount, err)
	}

	// Cross-tenant isolation: Tenant A cannot see Tenant B's items
	var crossCount int
	err = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM webhook_items
			WHERE tenant_id = $1
		`, tenantB.tenantID).Scan(&crossCount)
	})
	if err != nil || crossCount != 0 {
		t.Fatalf("vazamento de dados: tenant A conseguiu ver itens do tenant B: %d", crossCount)
	}

	// Tenant cannot read raw ingress_deliveries
	err = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var dummy int
		return tx.QueryRow(ctx, `SELECT count(*) FROM ingress_deliveries`).Scan(&dummy)
	})
	if err == nil {
		t.Fatal("tenant app role não pode ter permissão de SELECT em ingress_deliveries")
	}

	// 3. Process inbound item for Tenant A
	if err := inboundWorker.ApplyInbound(ctx, tenantA.tenantID, itemIDA); err != nil {
		t.Fatalf("falha ao processar inbound Tenant A: %v", err)
	}

	if err := inboundWorker.ApplyInbound(ctx, tenantB.tenantID, itemIDB); err != nil {
		t.Fatalf("falha ao processar inbound Tenant B: %v", err)
	}
	var messagesB int
	if err := testStore.WithTenantTx(ctx, tenantB.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, tenantB.tenantID).Scan(&messagesB)
	}); err != nil || messagesB != 1 {
		t.Fatalf("mensagens tenant B: %d (err: %v)", messagesB, err)
	}

	// Verify conversation and message created for Tenant A
	var convCountA int
	_ = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id = $1`, tenantA.tenantID).Scan(&convCountA)
	})
	if convCountA != 1 {
		t.Fatalf("Tenant A deveria ter 1 mensagem processada, obteve %d", convCountA)
	}

	// 4. Replay test: ExpandIngress called again does not duplicate items or jobs
	var jobCountBefore int
	_ = testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue = 'inbound'`).Scan(&jobCountBefore)

	if err := ingressWorker.ExpandIngress(ctx, deliveryID); err != nil {
		t.Fatalf("segunda chamada ExpandIngress falhou: %v", err)
	}
	var jobCountAfter int
	_ = testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue = 'inbound'`).Scan(&jobCountAfter)
	if jobCountAfter != jobCountBefore {
		t.Fatalf("replay de expansão duplicou jobs River: antes %d, depois %d", jobCountBefore, jobCountAfter)
	}
}

func TestExpansion_WABAMismatch_WithConfigServer(t *testing.T) {
	ctx := context.Background()
	appID := "app-waba-mismatch"
	appSecret := "secret-waba-mismatch-888"
	registeredWABA := "waba-correct-111"
	mismatchedWABA := "waba-mismatched-999"
	phoneID := "5511988880001"

	tenant := seedTenantWithMeta(t, appID, registeredWABA, phoneID, "tok-mismatch", appSecret)

	// Ingress handler configured with the real configuration.Server
	ingressWorker := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer)

	// Webhook payload with mismatched WABA (mismatchedWABA != registeredWABA)
	body := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "%s",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"contacts": [{"profile": {"name": "Cliente"}, "wa_id": "5511911112222"}],
					"messages": [{"from": "5511911112222", "id": "wamid.mismatch.001", "timestamp": "1700000030", "text": {"body": "Ola"}, "type": "text"}]
				},
				"field": "messages"
			}]
		}]
	}`, mismatchedWABA, phoneID, phoneID))

	sum := sha256.Sum256(body)
	deliveryID := uuid.New()
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ingress_deliveries (id, source, app_id, body_sha256, body, state)
			VALUES ($1, 'meta_cloud', $2, $3, $4, 'stored')
		`, deliveryID, appID, sum[:], body)
		return err
	})
	if err != nil {
		t.Fatalf("falha ao inserir delivery: %v", err)
	}

	// Expand the delivery
	if err := ingressWorker.ExpandIngress(ctx, deliveryID); err != nil {
		t.Fatalf("ExpandIngress falhou: %v", err)
	}

	// Verify delivery is quarantined
	var delState string
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&delState)
	if delState != "quarantined" {
		t.Fatalf("esperado state quarantined devido ao mismatch de WABA, obteve %s", delState)
	}

	// Verify item was recorded in ingress_quarantine with reason 'incompatible_binding'
	var qReason string
	err = testPools.Ingress.QueryRow(ctx, `
		SELECT reason FROM ingress_quarantine WHERE delivery_id = $1
	`, deliveryID).Scan(&qReason)
	if err != nil || qReason != "incompatible_binding" {
		t.Fatalf("esperado incompatible_binding em ingress_quarantine, obteve %s (err: %v)", qReason, err)
	}

	// Verify no webhook_items created for tenant
	var count int
	_ = testStore.WithTenantTx(ctx, tenant.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id = $1`, tenant.tenantID).Scan(&count)
	})
	if count != 0 {
		t.Fatalf("nenhum item deveria ter sido criado no tenant para WABA incompatível, obteve %d", count)
	}
}

func TestExpansion_ResolverUnavailable_Recovery(t *testing.T) {
	ctx := context.Background()
	appID := "app-resolver-unavail"
	appSecret := "secret-unavail-222"
	phoneID := "5511966660001"

	tenant := seedTenantWithMeta(t, appID, "waba-unavail", phoneID, "tok-unavail", appSecret)

	// Create a flappable RouteResolver
	resolver := &flappableResolver{
		underlying: testConfigServer,
		failing:    true,
	}

	ingressWorker := NewIngressHandler(testPools.Ingress, testJobs, resolver)

	body := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "waba-unavail",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"contacts": [{"profile": {"name": "Cliente"}, "wa_id": "5511911113333"}],
					"messages": [{"from": "5511911113333", "id": "wamid.unavail.001", "timestamp": "1700000040", "text": {"body": "Teste Recuperacao"}, "type": "text"}]
				},
				"field": "messages"
			}]
		}]
	}`, phoneID, phoneID))

	sum := sha256.Sum256(body)
	deliveryID := uuid.New()
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ingress_deliveries (id, source, app_id, body_sha256, body, state)
			VALUES ($1, 'meta_cloud', $2, $3, $4, 'stored')
		`, deliveryID, appID, sum[:], body)
		return err
	})
	if err != nil {
		t.Fatalf("falha ao inserir delivery: %v", err)
	}

	// 1. First run: resolver fails with ErrUnavailable
	err = ingressWorker.ExpandIngress(ctx, deliveryID)
	if !errors.Is(err, configuration.ErrUnavailable) {
		t.Fatalf("esperado ErrUnavailable na falha operacional, obteve %v", err)
	}

	// Verify delivery state is STILL 'stored' (neither 'quarantined' nor 'routed')
	var state string
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&state)
	if state != "stored" {
		t.Fatalf("falha operacional não deve alterar state de delivery (esperado stored, obteve %s)", state)
	}

	// Verify nothing was quarantined
	var qCount int
	_ = testPools.Ingress.QueryRow(ctx, `SELECT count(*) FROM ingress_quarantine WHERE delivery_id = $1`, deliveryID).Scan(&qCount)
	if qCount != 0 {
		t.Fatalf("falha operacional não deve gravar em ingress_quarantine, obteve %d itens", qCount)
	}

	// 2. Recovery: resolver recovers
	resolver.failing = false

	// Retry the job (as River would do)
	err = ingressWorker.ExpandIngress(ctx, deliveryID)
	if err != nil {
		t.Fatalf("esperado sucesso após recuperação do resolvedor, obteve %v", err)
	}

	// Verify delivery state is now 'routed'
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&state)
	if state != "routed" {
		t.Fatalf("esperado state routed após recuperação, obteve %s", state)
	}

	// Verify item was created in webhook_items
	var itemCount int
	_ = testStore.WithTenantTx(ctx, tenant.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id = $1`, tenant.tenantID).Scan(&itemCount)
	})
	if itemCount != 1 {
		t.Fatalf("esperado 1 item em webhook_items após recuperação, obteve %d", itemCount)
	}
}

func TestExpansion_ResolverPermission_Recovery(t *testing.T) {
	ctx := context.Background()
	appID := "app-resolver-permission"
	appSecret := "secret-unavail-222"
	phoneID := "5511966660099"

	tenant := seedTenantWithMeta(t, appID, "waba-permission", phoneID, "tok-unavail", appSecret)

	ingressWorker := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer)

	body := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "waba-permission",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"contacts": [{"profile": {"name": "Cliente"}, "wa_id": "5511911113333"}],
					"messages": [{"from": "5511911113333", "id": "wamid.unavail.001", "timestamp": "1700000040", "text": {"body": "Teste Recuperacao"}, "type": "text"}]
				},
				"field": "messages"
			}]
		}]
	}`, phoneID, phoneID))

	sum := sha256.Sum256(body)
	deliveryID := uuid.New()
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ingress_deliveries (id, source, app_id, body_sha256, body, state)
			VALUES ($1, 'meta_cloud', $2, $3, $4, 'stored')
		`, deliveryID, appID, sum[:], body)
		return err
	})
	if err != nil {
		t.Fatalf("falha ao inserir delivery: %v", err)
	}

	// The real configuration server encounters SQLSTATE 42501.
	if _, err := testPools.Owner.Exec(ctx, `REVOKE SELECT ON meta_phone_numbers FROM chatisp_ingress`); err != nil {
		t.Fatal(err)
	}
	defer testPools.Owner.Exec(ctx, `GRANT SELECT ON meta_phone_numbers TO chatisp_ingress`)
	err = ingressWorker.ExpandIngress(ctx, deliveryID)
	if !errors.Is(err, configuration.ErrForbidden) {
		t.Fatalf("esperado ErrForbidden na falha operacional, obteve %v", err)
	}

	// Verify delivery state is STILL 'stored' (neither 'quarantined' nor 'routed')
	var state string
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&state)
	if state != "stored" {
		t.Fatalf("falha operacional não deve alterar state de delivery (esperado stored, obteve %s)", state)
	}

	// Verify nothing was quarantined
	var qCount int
	_ = testPools.Ingress.QueryRow(ctx, `SELECT count(*) FROM ingress_quarantine WHERE delivery_id = $1`, deliveryID).Scan(&qCount)
	if qCount != 0 {
		t.Fatalf("falha operacional não deve gravar em ingress_quarantine, obteve %d itens", qCount)
	}

	// 2. Recovery: resolver recovers
	if _, err := testPools.Owner.Exec(ctx, `GRANT SELECT ON meta_phone_numbers TO chatisp_ingress`); err != nil {
		t.Fatal(err)
	}

	// Retry the job (as River would do)
	err = ingressWorker.ExpandIngress(ctx, deliveryID)
	if err != nil {
		t.Fatalf("esperado sucesso após recuperação do resolvedor, obteve %v", err)
	}

	// Verify delivery state is now 'routed'
	_ = testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id = $1`, deliveryID).Scan(&state)
	if state != "routed" {
		t.Fatalf("esperado state routed após recuperação, obteve %s", state)
	}

	// Verify item was created in webhook_items
	var itemCount int
	_ = testStore.WithTenantTx(ctx, tenant.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id = $1`, tenant.tenantID).Scan(&itemCount)
	})
	if itemCount != 1 {
		t.Fatalf("esperado 1 item em webhook_items após recuperação, obteve %d", itemCount)
	}
}

type flappableResolver struct {
	underlying RouteResolver
	failing    bool
}

func (f *flappableResolver) ResolveRoute(ctx context.Context, appID, phoneID string) (store.Route, error) {
	if f.failing {
		return store.Route{}, configuration.ErrUnavailable
	}
	return f.underlying.ResolveRoute(ctx, appID, phoneID)
}

func TestExpansion_MismatchQuarantine(t *testing.T) {
	ctx := context.Background()
	appID := "app-mismatch-a"
	appSecret := "secret-mismatch-test"
	tenantA := seedTenantWithMeta(t, appID, "waba-mismatch-a", "5511999990033", "tok-a", appSecret)

	ingressWorker := NewIngressHandler(testPools.Ingress, testJobs)
	repo := store.Repo{}

	// Envelope for app-mismatch-a containing a phone registered to another app without shared route
	envelopeBody := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [
			{
				"id": "waba-mismatch-a",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"metadata": {"display_phone_number": "+5511999990033", "phone_number_id": "5511999990033"},
						"contacts": [{"profile": {"name": "Cliente A"}, "wa_id": "5511911111111"}],
						"messages": [{"from": "5511911111111", "id": "wamid.mismatch.a.001", "timestamp": "1700000010", "text": {"body": "Msg A"}, "type": "text"}]
					},
					"field": "messages"
				}]
			},
			{
				"id": "waba-other",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"metadata": {"display_phone_number": "+5599999999999", "phone_number_id": "5599999999999"},
						"contacts": [{"profile": {"name": "Cliente Mismatch"}, "wa_id": "5599922222222"}],
						"messages": [{"from": "5599922222222", "id": "wamid.mismatch.unk.001", "timestamp": "1700000020", "text": {"body": "Msg Unk"}, "type": "text"}]
					},
					"field": "messages"
				}]
			}
		]
	}`)

	var delivery store.Delivery
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		delivery, err = repo.StoreDelivery(ctx, tx, "app-mismatch-a", envelopeBody, "", "")
		return err
	})
	if err != nil {
		t.Fatalf("falha ao persistir delivery: %v", err)
	}

	if err := ingressWorker.ExpandIngress(ctx, delivery.ID); err != nil {
		t.Fatalf("falha em ExpandIngress: %v", err)
	}

	// Verify Tenant A got its valid item
	var countA int
	_ = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id = $1`, tenantA.tenantID).Scan(&countA)
	})
	if countA != 1 {
		t.Fatalf("esperado 1 item para Tenant A, obteve %d", countA)
	}

	// Verify the unknown/mismatch item was quarantined
	var quarCount int
	_ = testPools.Ingress.QueryRow(ctx, `
		SELECT count(*) FROM ingress_quarantine WHERE delivery_id = $1
	`, delivery.ID).Scan(&quarCount)
	if quarCount != 1 {
		t.Fatalf("esperado 1 item em ingress_quarantine, obteve %d", quarCount)
	}

	// 5. Unknown phone is quarantined without tenant leakage
	unknownBody := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "waba-unknown",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+5599999999999", "phone_number_id": "5599999999999"},
					"messages": [{"from": "5511900000000", "id": "wamid.unk.001", "timestamp": "1700000030", "text": {"body": "Desconhecido"}, "type": "text"}]
				},
				"field": "messages"
			}]
		}]
	}`)
	var unkDelivery store.Delivery
	_ = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		unkDelivery, err = repo.StoreDelivery(ctx, tx, appID, unknownBody, "", "")
		return err
	})
	if err := ingressWorker.ExpandIngress(ctx, unkDelivery.ID); err != nil {
		t.Fatalf("falha ao expandir número desconhecido: %v", err)
	}
	var unkQuarCount int
	_ = testPools.Ingress.QueryRow(ctx, `
		SELECT count(*) FROM ingress_quarantine
		WHERE delivery_id = $1 AND reason = 'unknown_phone' AND remote_ref = '5599999999999'
	`, unkDelivery.ID).Scan(&unkQuarCount)
	if unkQuarCount != 1 {
		t.Fatalf("esperado 1 item em quarentena para telefone desconhecido, obteve %d", unkQuarCount)
	}

	// 6. Large DedupKey / status callback data exceeding 256 bytes is bounded and stable
	largeExtID := strings.Repeat("a", 280)
	largeCallback := strings.Repeat("c", 400)
	largeBody := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "%s",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"statuses": [{
						"id": "%s",
						"status": "delivered",
						"timestamp": "1700000099",
						"recipient_id": "5511999990000",
						"biz_opaque_callback_data": "%s"
					}]
				},
				"field": "messages"
			}]
		}]
	}`, tenantA.wabaID, tenantA.phoneID, tenantA.phoneID, largeExtID, largeCallback))

	var largeDel store.Delivery
	err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		largeDel, err = repo.StoreDelivery(ctx, tx, tenantA.appID, largeBody, "", "")
		return err
	})
	if err != nil {
		t.Fatalf("falha ao persistir delivery grande: %v", err)
	}
	if err := ingressWorker.ExpandIngress(ctx, largeDel.ID); err != nil {
		t.Fatalf("ExpandIngress falhou com DedupKey grande: %v", err)
	}

	var itemKey string
	err = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT external_key FROM webhook_items
			WHERE delivery_id = $1
		`, largeDel.ID).Scan(&itemKey)
	})
	if err != nil {
		t.Fatalf("webhook_item não encontrado: %v", err)
	}
	if len(itemKey) > 256 {
		t.Fatalf("external_key excede 256 bytes: %d", len(itemKey))
	}

	// 7. Known phone with quarantined item stored as kind=other/state=quarantined preserving reason
	groupBody := []byte(fmt.Sprintf(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "%s",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "+%s", "phone_number_id": "%s"},
					"messages": [{
						"from": "5511900000000",
						"id": "wamid.group.001",
						"timestamp": "1700000100",
						"group_id": "12345-67890@g.us",
						"type": "image",
						"image": {"id": "img-001"}
					}]
				},
				"field": "messages"
			}]
		}]
	}`, tenantA.wabaID, tenantA.phoneID, tenantA.phoneID))

	var qDel store.Delivery
	err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		qDel, err = repo.StoreDelivery(ctx, tx, tenantA.appID, groupBody, "", "")
		return err
	})
	if err != nil {
		t.Fatalf("falha ao persistir delivery de grupo: %v", err)
	}
	if err := ingressWorker.ExpandIngress(ctx, qDel.ID); err != nil {
		t.Fatalf("ExpandIngress falhou em item quarentenado: %v", err)
	}

	var itemState, kind string
	var rawPayload []byte
	err = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT state, kind, payload FROM webhook_items
			WHERE delivery_id = $1
		`, qDel.ID).Scan(&itemState, &kind, &rawPayload)
	})
	if err != nil {
		t.Fatalf("item em quarentena não encontrado em webhook_items: %v", err)
	}
	if itemState != "quarantined" {
		t.Fatalf("esperado state = quarantined em webhook_items, obteve %s", itemState)
	}
	if kind != "other" {
		t.Fatalf("esperado kind = other, obteve %s", kind)
	}
	var parsedPayload struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(rawPayload, &parsedPayload)
	if parsedPayload.Reason == "" {
		t.Fatalf("payload da quarentena deve preservar o motivo: %s", string(rawPayload))
	}
}

func createTestAttempt(t *testing.T, st seededTenant, body, reqID string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	p := service.Principal{
		UserID:       st.userID,
		MembershipID: st.memberID,
		TenantID:     st.tenantID,
		Role:         "admin",
	}
	contactID := uuid.New()
	identityID := uuid.New()
	phone := fmt.Sprintf("55119%08d", time.Now().UnixNano()%100000000)
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Teste')`, contactID, st.tenantID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, identityID, st.tenantID, contactID, st.connectorID, phone)
		return err
	}); err != nil {
		t.Fatalf("falha ao criar contato/identidade de teste: %v", err)
	}

	recv, err := testAttendanceSvc.ReceiveText(ctx, p, service.ReceiveInput{
		InboxID:           st.inboxID,
		ContactID:         contactID,
		IdentityID:        identityID,
		ConnectorID:       st.connectorID,
		ExternalMessageID: "wamid.inbound." + reqID,
		Body:              "Início " + reqID,
		Period:            time.Now().UTC().Format("200601"),
		TrustedAt:         time.Now().Add(-10 * time.Minute),
		OpenedAt:          time.Now().Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("falha em ReceiveText: %v", err)
	}

	conv, err := testAttendanceSvc.Assume(ctx, p, st.inboxID, recv.Conversation.ID, recv.Conversation.Version, time.Now())
	if err != nil {
		t.Fatalf("falha em Assume: %v", err)
	}

	res, err := testAttendanceSvc.Send(ctx, p, service.SendInput{
		InboxID:         st.inboxID,
		ConversationID:  conv.ID,
		ExpectedVersion: conv.Version,
		ClientRequestID: reqID,
		Body:            body,
		At:              time.Now(),
	})
	if err != nil {
		t.Fatalf("falha em Send: %v", err)
	}
	return res.AttemptID, res.MessageID
}

func TestInbound_MessageAndStatusReconciliation(t *testing.T) {
	ctx := context.Background()
	appID := "app-inbound-test"
	appSecret := "secret-inbound-test"
	st := seedTenantWithMeta(t, appID, "waba-inbound-t", "5511999990002", "tok", appSecret)

	inboundWorker := NewInboundHandler(testStore, testAttendanceSvc)

	t.Run("ApplyInbound_MessageOpensConversationAndContact", func(t *testing.T) {
		deliveryID := uuid.New()
		_ = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			del, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, []byte(`{"dummy":"msg"}`), "", "")
			deliveryID = del.ID
			return err
		})

		msgPayload := MessageItemPayload{
			AppID:         appID,
			WABAID:        st.wabaID,
			PhoneNumberID: st.phoneID,
			From:          "5511987654321",
			Text:          "Olá, preciso de suporte",
			ExternalID:    "wamid.inbound.msg.101",
			DedupKey:      "message:wamid.inbound.msg.101",
			OriginAt:      time.Now(),
			ProfileName:   "Cliente VIP",
		}
		rawMsg, _ := json.Marshal(msgPayload)

		var itemID uuid.UUID
		err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    deliveryID,
				InboxID:       st.inboxID,
				ConnectorID:   st.connectorID,
				PhoneNumberID: st.phoneID,
				ExternalKey:   msgPayload.DedupKey,
				Kind:          "message",
				Payload:       rawMsg,
			})
			itemID = res.ID
			return err
		})
		if err != nil {
			t.Fatalf("falha ao gravar webhook_item: %v", err)
		}

		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, itemID); err != nil {
			t.Fatalf("falha ao processar ApplyInbound: %v", err)
		}

		// Verify contact created
		var contactName, contactPhone string
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT name, phone FROM contacts
				WHERE tenant_id = $1 AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			`, st.tenantID).Scan(&contactName, &contactPhone)
		})
		if err != nil {
			t.Fatalf("contato não encontrado: %v", err)
		}
		if contactName != "Cliente VIP" || contactPhone != "+5511987654321" {
			t.Fatalf("dados do contato incorretos: %s / %s", contactName, contactPhone)
		}

		// Verify conversation created
		var convID uuid.UUID
		var protocol string
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT id, protocol FROM conversations
				WHERE tenant_id = $1 AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			`, st.tenantID).Scan(&convID, &protocol)
		})
		if err != nil || convID == uuid.Nil || protocol == "" {
			t.Fatalf("conversa ou protocolo não criado: %v (protocol: %s)", err, protocol)
		}

		// Verify message stored with external id
		var msgBody string
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT body FROM messages
				WHERE tenant_id = $1 AND external_message_id = 'wamid.inbound.msg.101'
			`, st.tenantID).Scan(&msgBody)
		})
		if err != nil || msgBody != "Olá, preciso de suporte" {
			t.Fatalf("mensagem não encontrada ou corpo incorreto: %v", err)
		}

		// Verify webhook_items state is processed
		var itemState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE id = $1`, itemID).Scan(&itemState)
		})
		if itemState != "processed" {
			t.Fatalf("esperado item state processed, obteve %s", itemState)
		}

		// Replay test: ApplyInbound again is a no-op
		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, itemID); err != nil {
			t.Fatalf("replay de ApplyInbound falhou: %v", err)
		}
	})

	t.Run("ApplyInbound_EarlyStatusPendingThenReconciled", func(t *testing.T) {
		deliveryID := uuid.New()
		_ = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			del, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, []byte(`{"dummy":"status"}`), "", "")
			deliveryID = del.ID
			return err
		})

		earlyMessageID := "wamid.early.status.202"
		statusPayload := StatusItemPayload{
			AppID:             appID,
			WABAID:            st.wabaID,
			PhoneNumberID:     st.phoneID,
			ExternalMessageID: earlyMessageID,
			ExternalEventID:   "evt-delivered-1",
			Kind:              "delivered",
			OriginAt:          time.Now(),
		}
		rawStatus, _ := json.Marshal(statusPayload)

		var statusItemID uuid.UUID
		_ = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    deliveryID,
				InboxID:       st.inboxID,
				ConnectorID:   st.connectorID,
				PhoneNumberID: st.phoneID,
				ExternalKey:   "status:" + earlyMessageID + ":delivered",
				Kind:          "status",
				Payload:       rawStatus,
			})
			statusItemID = res.ID
			return err
		})

		// Apply early status BEFORE message exists
		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, statusItemID); err != nil {
			t.Fatalf("falha ao processar status antecipado: %v", err)
		}

		// Verify status is stored as pending = true
		var isPending bool
		var msgIDNull *uuid.UUID
		err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT pending, message_id
				FROM message_status_events
				WHERE tenant_id = $1 AND external_message_id = $2
			`, st.tenantID, earlyMessageID).Scan(&isPending, &msgIDNull)
		})
		if err != nil {
			t.Fatalf("recibo antecipado não encontrado: %v", err)
		}
		if !isPending || msgIDNull != nil {
			t.Fatalf("recibo antecipado deveria estar pending=true e message_id=NULL, obteve pending=%v, msgID=%v", isPending, msgIDNull)
		}

		// Now the message arrives
		msgPayload := MessageItemPayload{
			AppID:         appID,
			WABAID:        st.wabaID,
			PhoneNumberID: st.phoneID,
			From:          "5511987654321",
			Text:          "Mensagem correlacionada ao status",
			ExternalID:    earlyMessageID,
			DedupKey:      "message:" + earlyMessageID,
			OriginAt:      time.Now(),
		}
		rawMsg, _ := json.Marshal(msgPayload)
		var msgItemID uuid.UUID
		_ = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    deliveryID,
				InboxID:       st.inboxID,
				ConnectorID:   st.connectorID,
				PhoneNumberID: st.phoneID,
				ExternalKey:   msgPayload.DedupKey,
				Kind:          "message",
				Payload:       rawMsg,
			})
			msgItemID = res.ID
			return err
		})

		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, msgItemID); err != nil {
			t.Fatalf("falha ao processar mensagem tardia: %v", err)
		}

		// Verify the pending status was automatically reconciled and linked
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT pending, message_id
				FROM message_status_events
				WHERE tenant_id = $1 AND external_message_id = $2
			`, st.tenantID, earlyMessageID).Scan(&isPending, &msgIDNull)
		})
		if err != nil {
			t.Fatalf("falha ao verificar reconciliação do status: %v", err)
		}
		if isPending || msgIDNull == nil || *msgIDNull == uuid.Nil {
			t.Fatalf("status deveria estar reconciliado (pending=false, message_id!=nil), obteve pending=%v, msgID=%v", isPending, msgIDNull)
		}
	})

	t.Run("StatusReceipt_ReconcilesUncertainAttemptViaCallbackData", func(t *testing.T) {
		attemptID, messageID := createTestAttempt(t, st, "resposta com envio incerto", "req-unc-callback-001")

		workerID := uuid.New()
		res, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
			TenantID:     st.tenantID,
			InboxID:      st.inboxID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: 0,
			ExpiresAt:    time.Now().Add(time.Minute),
			At:           time.Now(),
		})
		if err != nil {
			t.Fatalf("falha ao reservar dispatch: %v", err)
		}

		// Move attempt to uncertain (simulating lost HTTP response after dispatch)
		_, err = testAttendanceSvc.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     st.tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: res.LeaseVersion,
			To:           "uncertain",
			ErrorCode:    "resposta_perdida",
			ErrorMessage: "conexão encerrada sem resposta HTTP",
		})
		if err != nil {
			t.Fatalf("falha ao mover para uncertain: %v", err)
		}
		_ = testAttendanceSvc.ConsumeReservation(ctx, st.tenantID, res.ID, workerID)

		// Verify attempt is uncertain and message has NO external_message_id yet
		var msgExtID *string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT external_message_id FROM messages WHERE id = $1`, messageID).Scan(&msgExtID)
		})
		if msgExtID != nil {
			t.Fatalf("mensagem não deveria ter external_message_id ainda: %v", *msgExtID)
		}

		// Meta delivers status receipt carrying CallbackData = attemptID.String()
		wamid := "wamid.reconciled.lost.001"
		statusPayload, _ := json.Marshal(StatusItemPayload{
			AppID:             appID,
			WABAID:            st.wabaID,
			PhoneNumberID:     st.phoneID,
			ExternalMessageID: wamid,
			Kind:              "delivered",
			OriginAt:          time.Now(),
			CallbackData:      attemptID.String(),
		})

		var itemID uuid.UUID
		err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			del, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, []byte(`{"dummy":"callback-reconcile"}`), "", "")
			if err != nil {
				return err
			}
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    del.ID,
				InboxID:       st.inboxID,
				ConnectorID:   st.connectorID,
				PhoneNumberID: st.phoneID,
				ExternalKey:   "status:" + wamid + ":delivered",
				Kind:          "status",
				Payload:       statusPayload,
			})
			if err != nil {
				return err
			}
			itemID = res.ID
			return nil
		})
		if err != nil {
			t.Fatalf("falha ao persistir delivery e webhook item: %v", err)
		}

		// Inbound worker processes status receipt
		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, itemID); err != nil {
			t.Fatalf("ApplyInbound falhou ao reconciliar: %v", err)
		}

		// Verify attempt was reconciled to accepted via observation
		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, attemptID).Scan(&attemptState)
		})
		if attemptState != "accepted" {
			t.Fatalf("esperado tentativa reconciliada para accepted, obteve %s", attemptState)
		}

		// Verify message now has the external_message_id linked
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT external_message_id FROM messages WHERE id = $1`, messageID).Scan(&msgExtID)
		})
		if msgExtID == nil || *msgExtID != wamid {
			t.Fatalf("mensagem deveria ter external_message_id %s, obteve %v", wamid, msgExtID)
		}
	})

	t.Run("StatusReceipt_CrossBoxCallbackMismatch_Quarantined", func(t *testing.T) {
		// Attempt belongs to Box 1 (st.connectorID, st.inboxID)
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE inbox_send_leases SET expires_at = now() - interval '1 second' WHERE tenant_id = $1 AND inbox_id = $2`, st.tenantID, st.inboxID)
			return err
		})
		attemptID, messageID := createTestAttempt(t, st, "resposta box 1", "req-unc-crossbox-001")
		workerID := uuid.New()
		res, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
			TenantID:     st.tenantID,
			InboxID:      st.inboxID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: 0,
			ExpiresAt:    time.Now().Add(time.Minute),
			At:           time.Now(),
		})
		if err != nil {
			t.Fatalf("falha ao reservar dispatch: %v", err)
		}
		_, err = testAttendanceSvc.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     st.tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: res.LeaseVersion,
			To:           "uncertain",
			ErrorCode:    "resposta_perdida",
			ErrorMessage: "conexão encerrada sem resposta HTTP",
		})
		if err != nil {
			t.Fatalf("falha ao mover para uncertain: %v", err)
		}
		_ = testAttendanceSvc.ConsumeReservation(ctx, st.tenantID, res.ID, workerID)

		// Seed second box in the SAME tenant
		conn2, inbox2 := seedSecondBoxForTenant(t, st, "5511999990099")

		// Status receipt arrives for Box 2 (conn2, inbox2) carrying CallbackData pointing to attemptID of Box 1
		wamidCross := "wamid.crossbox.mismatch.001"
		statusPayload, _ := json.Marshal(StatusItemPayload{
			AppID:             appID,
			WABAID:            st.wabaID,
			PhoneNumberID:     "5511999990099",
			ExternalMessageID: wamidCross,
			Kind:              "delivered",
			OriginAt:          time.Now(),
			CallbackData:      attemptID.String(),
		})

		var itemID uuid.UUID
		err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			del, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, []byte(`{"dummy":"crossbox-callback"}`), "", "")
			if err != nil {
				return err
			}
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    del.ID,
				InboxID:       inbox2,
				ConnectorID:   conn2,
				PhoneNumberID: "5511999990099",
				ExternalKey:   "status:" + wamidCross + ":delivered",
				Kind:          "status",
				Payload:       statusPayload,
			})
			if err != nil {
				return err
			}
			itemID = res.ID
			return nil
		})
		if err != nil {
			t.Fatalf("falha ao persistir webhook item crossbox: %v", err)
		}

		// Inbound worker processes the mismatching item
		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, itemID); err != nil {
			t.Fatalf("ApplyInbound falhou: %v", err)
		}

		// Verify the item was quarantined because callback does not match the attempt's connector/inbox
		var itemState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE id = $1`, itemID).Scan(&itemState)
		})
		if itemState != "quarantined" {
			t.Fatalf("esperado webhook item em quarentena por cross-box mismatch, obteve %s", itemState)
		}

		// Verify attempt of Box 1 remained uncertain and was NOT reconciled by Box 2 receipt
		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, attemptID).Scan(&attemptState)
		})
		if attemptState != "uncertain" {
			t.Fatalf("tentativa da caixa 1 não deveria ser reconciliada por recibo da caixa 2, obteve %s", attemptState)
		}

		// Message of Box 1 must still have NO external_message_id
		var msgExtID *string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT external_message_id FROM messages WHERE id = $1`, messageID).Scan(&msgExtID)
		})
		if msgExtID != nil {
			t.Fatalf("mensagem da caixa 1 não deveria receber ID remoto da caixa 2, obteve %s", *msgExtID)
		}
	})

	t.Run("StatusReceipt_CorrelationFailure_PropagatedAndRetried", func(t *testing.T) {
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE inbox_send_leases SET expires_at = now() - interval '1 second' WHERE tenant_id = $1 AND inbox_id = $2`, st.tenantID, st.inboxID)
			return err
		})
		attemptID, _ := createTestAttempt(t, st, "resposta com falha transiente", "req-transient-corr-001")
		workerID := uuid.New()
		res, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
			TenantID:     st.tenantID,
			InboxID:      st.inboxID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: 0,
			ExpiresAt:    time.Now().Add(time.Minute),
			At:           time.Now(),
		})
		if err != nil {
			t.Fatalf("falha ao reservar dispatch: %v", err)
		}
		_, err = testAttendanceSvc.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     st.tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: res.LeaseVersion,
			To:           "uncertain",
			ErrorCode:    "resposta_perdida",
			ErrorMessage: "conexao perdida",
		})
		if err != nil {
			t.Fatalf("falha ao mover para uncertain: %v", err)
		}
		_ = testAttendanceSvc.ConsumeReservation(ctx, st.tenantID, res.ID, workerID)

		wamid := "wamid.transient.corr.001"
		statusPayload, _ := json.Marshal(StatusItemPayload{
			AppID:             appID,
			WABAID:            st.wabaID,
			PhoneNumberID:     st.phoneID,
			ExternalMessageID: wamid,
			Kind:              "delivered",
			OriginAt:          time.Now(),
			CallbackData:      attemptID.String(),
		})

		var itemID uuid.UUID
		err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			del, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, []byte(`{"dummy":"transient-receipt"}`), "", "")
			if err != nil {
				return err
			}
			res, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{
				TenantID:      st.tenantID,
				DeliveryID:    del.ID,
				InboxID:       st.inboxID,
				ConnectorID:   st.connectorID,
				PhoneNumberID: st.phoneID,
				ExternalKey:   "status:" + wamid + ":delivered",
				Kind:          "status",
				Payload:       statusPayload,
			})
			if err != nil {
				return err
			}
			itemID = res.ID
			return nil
		})
		if err != nil {
			t.Fatalf("falha ao persistir item: %v", err)
		}

		// 1. Cancelled context during ApplyInbound causes failure before concluding correlation
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()
		err = inboundWorker.ApplyInbound(cancelCtx, st.tenantID, itemID)
		if err == nil {
			t.Fatal("esperado erro ao executar ApplyInbound com contexto cancelado")
		}

		// Item MUST still be pending (not processed)
		var itemState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE id = $1`, itemID).Scan(&itemState)
		})
		if itemState != "pending" {
			t.Fatalf("item não deveria ser marcado como processed após falha, obteve %s", itemState)
		}

		// 2. Retry with active context succeeds and marks item processed
		if err := inboundWorker.ApplyInbound(ctx, st.tenantID, itemID); err != nil {
			t.Fatalf("retry de ApplyInbound falhou: %v", err)
		}

		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE id = $1`, itemID).Scan(&itemState)
		})
		if itemState != "processed" {
			t.Fatalf("item deveria ser marcado processed após retry com sucesso, obteve %s", itemState)
		}
	})
}

func TestOutbound_CoordinationAndDispatch(t *testing.T) {
	ctx := context.Background()
	appID := "app-outbound-test"
	appSecret := "secret-outbound-test"
	st := seedTenantWithMeta(t, appID, "waba-outbound-t", "5511999990005", "tok-out", appSecret)

	agentPrincipal := service.Principal{
		UserID:       st.userID,
		MembershipID: st.memberID,
		TenantID:     st.tenantID,
		Role:         "admin",
	}

	contactID := uuid.New()
	identityID := uuid.New()
	_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Teste')`, contactID, st.tenantID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, '5511988880005')
		`, identityID, st.tenantID, contactID, st.connectorID)
		return err
	})

	// 1. Establish contact and open conversation via inbound message
	receiveRes, err := testAttendanceSvc.ReceiveText(ctx, agentPrincipal, service.ReceiveInput{
		InboxID:           st.inboxID,
		ContactID:         contactID,
		IdentityID:        identityID,
		ConnectorID:       st.connectorID,
		ExternalMessageID: "wamid.seed.inbound.outboundtest",
		Body:              "Início conversa para envio",
		Period:            time.Now().UTC().Format("200601"),
		TrustedAt:         time.Now().Add(-10 * time.Minute),
		OpenedAt:          time.Now().Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("falha ao inicializar conversa de teste: %v", err)
	}

	// 2. Assume conversation
	conv, err := testAttendanceSvc.Assume(ctx, agentPrincipal, st.inboxID, receiveRes.Conversation.ID, receiveRes.Conversation.Version, time.Now())
	if err != nil {
		t.Fatalf("falha ao assumir conversa: %v", err)
	}

	sendMsg := func(t *testing.T, body, reqID string) service.SendResult {
		t.Helper()
		var version int64
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE id = $1`, conv.ID).Scan(&version)
		})
		res, err := testAttendanceSvc.Send(ctx, agentPrincipal, service.SendInput{
			InboxID:         st.inboxID,
			ConversationID:  conv.ID,
			ExpectedVersion: version,
			ClientRequestID: reqID,
			Body:            body,
			At:              time.Now(),
		})
		if err != nil {
			t.Fatalf("falha ao enviar: %v", err)
		}
		return res
	}

	resetLease := func() {
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE inbox_send_leases
				SET expires_at = now() - interval '1 second'
				WHERE tenant_id = $1 AND inbox_id = $2
			`, st.tenantID, st.inboxID)
			return err
		})
	}

	t.Run("LeaseCoordination_AllowOutboundSlotLimitAndInboxLease", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem de teste de coordenação", "req-coord-001")

		leaseCoord := NewOutboundLease(testStore)

		// Initially allowed
		allowed, err := leaseCoord.AllowOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil || !allowed {
			t.Fatalf("deveria permitir envio inicialmente: allowed=%v err=%v", allowed, err)
		}

		// Hold 4 outbound slots (PG limit)
		workerSlots := make([]store.JobReservation, 4)
		for i := 0; i < 4; i++ {
			workerID := uuid.New()
			slot, err := testAttendanceSvc.ReserveWorker(ctx, st.tenantID, workerID, "outbound", time.Now().Add(time.Minute))
			if err != nil {
				t.Fatalf("falha ao ocupar slot %d: %v", i, err)
			}
			workerSlots[i] = slot
		}

		// Slot limit reached -> AllowOutbound must return false
		allowed, err = leaseCoord.AllowOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("erro ao verificar coordenação com slots cheios: %v", err)
		}
		if allowed {
			t.Fatal("não deveria permitir envio quando teto de 4 slots estiver esgotado")
		}

		// Release slots
		for _, s := range workerSlots {
			_ = testAttendanceSvc.ReleaseWorker(ctx, st.tenantID, s.ID, uuid.New(), "outbound")
			// Direct delete to ensure cleanup
			_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `DELETE FROM tenant_job_reservations WHERE id = $1`, s.ID)
				return err
			})
		}

		// Now allowed again
		allowed, err = leaseCoord.AllowOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil || !allowed {
			t.Fatalf("deveria permitir envio após liberar slots: allowed=%v, err=%v", allowed, err)
		}

		// Hold inbox send lease directly
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO inbox_send_leases (tenant_id, inbox_id, owner_id, lease_version, expires_at)
				VALUES ($1, $2, $3, 1, now() + interval '30 seconds')
				ON CONFLICT (tenant_id, inbox_id) DO UPDATE
				SET owner_id = EXCLUDED.owner_id, expires_at = EXCLUDED.expires_at
			`, st.tenantID, st.inboxID, uuid.New())
			return err
		})

		// Inbox lease held -> AllowOutbound must return false
		allowed, err = leaseCoord.AllowOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("erro ao verificar coordenação com lease ocupado: %v", err)
		}
		if allowed {
			t.Fatal("não deveria permitir envio quando inbox lease estiver ocupado")
		}

		resetLease()

		allowed, err = leaseCoord.AllowOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil || !allowed {
			t.Fatalf("deveria permitir envio após expiração do lease: allowed=%v, err=%v", allowed, err)
		}
	})

	t.Run("Dispatch_SuccessAcceptedAndConsumed", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem despachada com sucesso", "req-succ-001")

		driver := &recordingDriver{
			local:      whatsapp.StateAccepted,
			remote:     whatsapp.EvidenceNone,
			providerID: "wamid.outbound.remote.999",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("falha ao despachar: %v", err)
		}

		if driver.CallCount() != 1 {
			t.Fatalf("esperado 1 chamada remota, obteve %d", driver.CallCount())
		}

		// Verify state is accepted with external message id
		var attemptState, extID string
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT o.state, coalesce(m.external_message_id, '')
				FROM outbound_attempts o
				JOIN messages m ON m.tenant_id = o.tenant_id AND m.id = o.message_id
				WHERE o.id = $1
			`, sendRes.AttemptID).Scan(&attemptState, &extID)
		})
		if err != nil {
			t.Fatalf("falha ao consultar tentativa: %v", err)
		}
		if attemptState != "accepted" || extID != "wamid.outbound.remote.999" {
			t.Fatalf("esperado accepted / wamid.outbound.remote.999, obteve %s / %s", attemptState, extID)
		}

		// Verify send_reservations is consumed
		var resState string
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM send_reservations WHERE attempt_id = $1`, sendRes.AttemptID).Scan(&resState)
		})
		if err != nil || resState != "consumed" {
			t.Fatalf("reserva de envio não foi consumida: state=%s, err=%v", resState, err)
		}
	})

	t.Run("Dispatch_TokenInvalidDegradesConnectorAndAlerts", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem com token inválido", "req-token-inv-001")

		driver := &recordingDriver{
			local:      whatsapp.StateFailed,
			rejectCode: metacloud.RejectionTokenInvalid,
			detail:     "Error validating access token: Session has expired",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("DispatchOutbound não deve retornar erro ao consumidor River em token inválido: %v", err)
		}

		// Verify attempt failed
		var attemptState, errCode string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT state, error_code
				FROM outbound_attempts
				WHERE id = $1
			`, sendRes.AttemptID).Scan(&attemptState, &errCode)
		})
		if attemptState != "failed" || errCode != "token_invalid" {
			t.Fatalf("esperado failed/token_invalid, obteve %s/%s", attemptState, errCode)
		}

		// Verify connector is degraded in configuration
		var connStatus string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT status
				FROM channel_connectors
				WHERE id = $1
			`, st.connectorID).Scan(&connStatus)
		})
		if connStatus != "degraded" {
			t.Fatalf("esperado conector degradado após token_invalid, obteve %s", connStatus)
		}

		// Restore connector for remaining tests
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE channel_connectors SET status = 'connected', version = '1' WHERE id = $1`, st.connectorID)
			return err
		})
	})

	t.Run("Dispatch_NotDispatchedEvidenceRetriesWithRiverBackoff", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem conexão recusada", "req-not-disp-001")

		driver := &recordingDriver{
			local:  whatsapp.StateRetrying,
			detail: "dial tcp 127.0.0.1: connection refused",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err == nil {
			t.Fatal("esperado erro para sinalizar retentativa ao River")
		}

		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, sendRes.AttemptID).Scan(&attemptState)
		})
		if attemptState != "retrying" {
			t.Fatalf("esperado state retrying, obteve %s", attemptState)
		}
	})

	t.Run("Dispatch_CrashRecoveryLeavesUncertainWithoutSecondRemoteCall", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem para testar queda", "req-crash-001")

		// 1. Manually transition attempt to sending to simulate crash during dispatch
		workerID := uuid.New()
		res, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
			TenantID:     st.tenantID,
			InboxID:      st.inboxID,
			AttemptID:    sendRes.AttemptID,
			OwnerID:      workerID,
			LeaseVersion: 0,
			ExpiresAt:    time.Now().Add(time.Minute),
			At:           time.Now(),
		})
		if err != nil || res.State != "sending" {
			t.Fatalf("falha ao reservar dispatch: %v", err)
		}

		// 2. Next worker execution runs DispatchOutbound on the 'sending' attempt
		driver := &recordingDriver{
			local:      whatsapp.StateAccepted,
			providerID: "wamid.should.never.call",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("DispatchOutbound retornou erro em crash recovery: %v", err)
		}

		// Crash recovery MUST NOT make a remote call
		if driver.CallCount() != 0 {
			t.Fatalf("chamada remota indevida durante recuperação de queda: %d chamadas", driver.CallCount())
		}

		// Verify state became uncertain
		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, sendRes.AttemptID).Scan(&attemptState)
		})
		if attemptState != "uncertain" {
			t.Fatalf("esperado estado uncertain após crash recovery, obteve %s", attemptState)
		}

		// 3. Subsequent worker restart / re-run MUST NOT send again
		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("segunda execução retornou erro: %v", err)
		}
		if driver.CallCount() != 0 {
			t.Fatalf("segunda chamada remota após incerto: %d chamadas", driver.CallCount())
		}
	})

	t.Run("Dispatch_LostResponseLeavesUncertain", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem timeout de resposta", "req-timeout-001")

		driver := &recordingDriver{
			local:  whatsapp.StateUncertain,
			detail: "timeout waiting for remote response",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("erro em lost response dispatch: %v", err)
		}

		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, sendRes.AttemptID).Scan(&attemptState)
		})
		if attemptState != "uncertain" {
			t.Fatalf("resposta perdida deve deixar tentativa uncertain, obteve %s", attemptState)
		}
	})

	t.Run("Dispatch_CancelledPriorToRemoteCallSetsFailedAndFreesReservation", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem cancelada antes do envio", "req-cancel-001")

		driver := &recordingDriver{
			local:  whatsapp.StateCancelled,
			detail: "context canceled before writing request",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("DispatchOutbound falhou em cancelamento prévio: %v", err)
		}

		var (
			attemptState string
			errorCode    *string
		)
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state, error_code FROM outbound_attempts WHERE id = $1`, sendRes.AttemptID).Scan(&attemptState, &errorCode)
		})
		if attemptState != "failed" {
			t.Fatalf("esperado tentativa failed para StateCancelled, obteve %s", attemptState)
		}
		if errorCode == nil || *errorCode != "cancelado" {
			t.Fatalf("esperado error_code 'cancelado', obteve %v", errorCode)
		}
	})

	t.Run("Dispatch_ObsoleteEpochCancelledBeforeRemoteCall", func(t *testing.T) {
		resetLease()
		sendRes := sendMsg(t, "Mensagem obsoleta de época antiga", "req-obsolete-001")
		if err != nil {
			t.Fatalf("falha ao enviar: %v", err)
		}

		// Bump conversation controller epoch (e.g. agent assumption changed or closed)
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE conversations SET controller_epoch = controller_epoch + 1 WHERE id = $1`, conv.ID)
			return err
		})

		driver := &recordingDriver{
			local:      whatsapp.StateAccepted,
			providerID: "wamid.epoch.never.call",
		}
		factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
		outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)

		err = outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
		if err != nil {
			t.Fatalf("DispatchOutbound não deve falhar em epoch obsoleta: %v", err)
		}

		if driver.CallCount() != 0 {
			t.Fatalf("chamada remota efetuada para mensagem com época obsoleta: %d chamadas", driver.CallCount())
		}

		var attemptState string
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, sendRes.AttemptID).Scan(&attemptState)
		})
		if attemptState != "cancelled" {
			t.Fatalf("esperado tentativa cancelled para época obsoleta, obteve %s", attemptState)
		}
	})
}

func TestDispatch_Fencing_DelayedResolution_LeaseLost(t *testing.T) {
	ctx := context.Background()
	appID := "app-dispatch-fence"
	appSecret := "secret-dispatch-fence-111"
	phoneID := "5511977770001"

	st := seedTenantWithMeta(t, appID, "waba-dispatch-fence", phoneID, "tok-fence", appSecret)

	agentPrincipal := service.Principal{
		UserID:       st.userID,
		MembershipID: st.memberID,
		TenantID:     st.tenantID,
		Role:         "admin",
	}

	contactID := uuid.New()
	identityID := uuid.New()
	_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Fencing')`, contactID, st.tenantID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, '5511988880007')
		`, identityID, st.tenantID, contactID, st.connectorID)
		return err
	})

	receiveRes, err := testAttendanceSvc.ReceiveText(ctx, agentPrincipal, service.ReceiveInput{
		InboxID:           st.inboxID,
		ContactID:         contactID,
		IdentityID:        identityID,
		ConnectorID:       st.connectorID,
		ExternalMessageID: "wamid.seed.inbound.fence",
		Body:              "Início conversa para fencing",
		Period:            time.Now().UTC().Format("200601"),
		TrustedAt:         time.Now().Add(-10 * time.Minute),
		OpenedAt:          time.Now().Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("falha ao inicializar conversa: %v", err)
	}

	conv, err := testAttendanceSvc.Assume(ctx, agentPrincipal, st.inboxID, receiveRes.Conversation.ID, receiveRes.Conversation.Version, time.Now())
	if err != nil {
		t.Fatalf("falha ao assumir conversa: %v", err)
	}

	var version int64
	_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE id = $1`, conv.ID).Scan(&version)
	})
	sendRes, err := testAttendanceSvc.Send(ctx, agentPrincipal, service.SendInput{
		InboxID:         st.inboxID,
		ConversationID:  conv.ID,
		ExpectedVersion: version,
		ClientRequestID: "req-fence-001",
		Body:            "Mensagem com quebra de fencing",
		At:              time.Now(),
	})
	if err != nil {
		t.Fatalf("falha ao criar envio: %v", err)
	}

	fakeDrv := &recordingDriver{}
	factory, err := NewDriverFactory(DriverFactoryConfig{
		Simulation: true,
		FakeDriver: fakeDrv,
	})
	if err != nil {
		t.Fatalf("falha ao criar factory: %v", err)
	}

	// Interceptor that simulates delay in material resolution where another worker acquires the lease
	worker2ID := uuid.New()
	interceptor := &delayedMaterialResolver{
		underlying: testConfigServer,
		onResolve: func() {
			// Simulate that worker 2 acquired the lease before worker 1 reached the dispatch boundary
			_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `
					UPDATE inbox_send_leases
					SET owner_id = $1, lease_version = lease_version + 1, expires_at = now() + interval '30 seconds'
					WHERE tenant_id = $2
					  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
					  AND inbox_id = $3
				`, worker2ID, st.tenantID, st.inboxID)
				return err
			})
		},
	}

	outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, interceptor, testConfigServer, factory)

	// Worker 1 runs DispatchOutbound
	dispatchErr := outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
	if !errors.Is(dispatchErr, store.ErrLeaseHeld) {
		t.Fatalf("esperado ErrLeaseHeld devido a quebra de fencing/lease perdido, obteve %v", dispatchErr)
	}

	// Verify that the expired owner did NOT dispatch the remote network call!
	if fakeDrv.CallCount() != 0 {
		t.Fatalf("violação de fencing: worker com lease vencido/perdido disparou chamada remota (calls=%d)", fakeDrv.CallCount())
	}
}

func TestDispatch_BoundaryRevalidates(t *testing.T) {
	for _, change := range []string{"membership", "epoch", "configuration"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			appID := "app-boundary-" + change
			appSecret := "secret-dispatch-fence-111"
			phoneID := map[string]string{"membership": "5511977771011", "epoch": "5511977771012", "configuration": "5511977771013"}[change]

			st := seedTenantWithMeta(t, appID, "waba-boundary-"+change, phoneID, "tok-fence", appSecret)

			agentPrincipal := service.Principal{
				UserID:       st.userID,
				MembershipID: st.memberID,
				TenantID:     st.tenantID,
				Role:         "admin",
			}

			contactID := uuid.New()
			identityID := uuid.New()
			_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Fencing')`, contactID, st.tenantID)
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, '5511988880007')
		`, identityID, st.tenantID, contactID, st.connectorID)
				return err
			})

			receiveRes, err := testAttendanceSvc.ReceiveText(ctx, agentPrincipal, service.ReceiveInput{
				InboxID:           st.inboxID,
				ContactID:         contactID,
				IdentityID:        identityID,
				ConnectorID:       st.connectorID,
				ExternalMessageID: "wamid.seed.inbound.fence",
				Body:              "Início conversa para fencing",
				Period:            time.Now().UTC().Format("200601"),
				TrustedAt:         time.Now().Add(-10 * time.Minute),
				OpenedAt:          time.Now().Add(-10 * time.Minute),
			})
			if err != nil {
				t.Fatalf("falha ao inicializar conversa: %v", err)
			}

			conv, err := testAttendanceSvc.Assume(ctx, agentPrincipal, st.inboxID, receiveRes.Conversation.ID, receiveRes.Conversation.Version, time.Now())
			if err != nil {
				t.Fatalf("falha ao assumir conversa: %v", err)
			}

			var version int64
			_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE id = $1`, conv.ID).Scan(&version)
			})
			sendRes, err := testAttendanceSvc.Send(ctx, agentPrincipal, service.SendInput{
				InboxID:         st.inboxID,
				ConversationID:  conv.ID,
				ExpectedVersion: version,
				ClientRequestID: "req-fence-001",
				Body:            "Mensagem com quebra de fencing",
				At:              time.Now(),
			})
			if err != nil {
				t.Fatalf("falha ao criar envio: %v", err)
			}

			fakeDrv := &recordingDriver{}
			factory, err := NewDriverFactory(DriverFactoryConfig{
				Simulation: true,
				FakeDriver: fakeDrv,
			})
			if err != nil {
				t.Fatalf("falha ao criar factory: %v", err)
			}

			interceptor := &delayedMaterialResolver{
				underlying: testConfigServer,
				afterResolve: func() {
					err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
						switch change {
						case "membership":
							_, err := tx.Exec(ctx, `UPDATE memberships SET status='disabled' WHERE id=$1`, st.memberID)
							return err
						case "epoch":
							_, err := tx.Exec(ctx, `UPDATE conversations SET controller_epoch=controller_epoch+1 WHERE id=$1`, conv.ID)
							return err
						default:
							_, err := tx.Exec(ctx, `UPDATE channel_connectors SET version='2' WHERE id=$1`, st.connectorID)
							return err
						}
					})
					if err != nil {
						t.Fatalf("falha ao alterar estado: %v", err)
					}
				},
			}

			outboundHandler := NewOutboundHandler(testStore, testAttendanceSvc, interceptor, testConfigServer, factory)

			// Worker 1 runs DispatchOutbound
			dispatchErr := outboundHandler.DispatchOutbound(ctx, st.tenantID, sendRes.AttemptID)
			if dispatchErr != nil {
				t.Fatalf("dispatch: %v", dispatchErr)
			}
			var state string
			if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id=$1`, sendRes.AttemptID).Scan(&state)
			}); err != nil || state != "failed" {
				t.Fatalf("estado=%s erro=%v", state, err)
			}

			// Verify that the expired owner did NOT dispatch the remote network call!
			if fakeDrv.CallCount() != 0 {
				t.Fatalf("violação de fencing: worker com lease vencido/perdido disparou chamada remota (calls=%d)", fakeDrv.CallCount())
			}

		})
	}
}

type delayedMaterialResolver struct {
	underlying   MaterialResolver
	onResolve    func()
	afterResolve func()
}

func (d *delayedMaterialResolver) ResolveMaterial(ctx context.Context, tenantID, connectorID uuid.UUID, version int64) (configuration.Material, error) {
	if d.onResolve != nil {
		d.onResolve()
	}
	material, err := d.underlying.ResolveMaterial(ctx, tenantID, connectorID, version)
	if d.afterResolve != nil {
		d.afterResolve()
	}
	return material, err
}

func TestFactory_SimulationMode(t *testing.T) {
	fake := whatsapp.NewFake()

	// 1. In official mode (Simulation = false), injecting a fake driver must fail
	_, err := NewDriverFactory(DriverFactoryConfig{
		Simulation: false,
		FakeDriver: fake,
	})
	if !errors.Is(err, ErrSimulationOnly) {
		t.Fatalf("esperado ErrSimulationOnly ao injetar fake em modo oficial, obteve: %v", err)
	}

	// 2. In simulation mode (Simulation = true), injecting a fake driver must succeed
	factory, err := NewDriverFactory(DriverFactoryConfig{
		Simulation: true,
		FakeDriver: fake,
	})
	if err != nil {
		t.Fatalf("falha ao criar factory em modo simulação: %v", err)
	}

	mat := configuration.Material{
		PhoneNumberID: "5511999990000",
		AccessToken:   "test-token",
		Version:       1,
	}
	driver, err := factory.DriverFor(context.Background(), uuid.New(), uuid.New(), mat)
	if err != nil {
		t.Fatalf("DriverFor falhou: %v", err)
	}
	if driver != fake {
		t.Fatal("driver retornado difere do fake injetado")
	}

	// 3. In official mode without fake, DriverFor builds an official metacloud driver
	officialFactory, err := NewDriverFactory(DriverFactoryConfig{Simulation: false})
	if err != nil {
		t.Fatalf("falha ao criar factory oficial: %v", err)
	}
	officialDriver, err := officialFactory.DriverFor(context.Background(), uuid.New(), uuid.New(), mat)
	if err != nil {
		t.Fatalf("DriverFor oficial falhou: %v", err)
	}
	if officialDriver == nil {
		t.Fatal("driver oficial retornado é nil")
	}
}

func TestWorkers_Factory(t *testing.T) {
	// Missing dependencies returns ErrInvalid
	_, err := NewWorkers(WorkersConfig{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperado ErrInvalid para WorkersConfig vazio, obteve: %v", err)
	}

	driverFactory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: whatsapp.NewFake()})

	// Missing CredentialReporter returns ErrInvalid
	_, err = NewWorkers(WorkersConfig{
		DB:                testStore,
		IngressPool:       testPools.Ingress,
		JobsClient:        testJobs,
		AttendanceService: testAttendanceSvc,
		MaterialResolver:  testConfigServer,
		DriverFactory:     driverFactory,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperado ErrInvalid sem CredentialReporter, obteve: %v", err)
	}

	handlers, err := NewWorkers(WorkersConfig{
		DB:                 testStore,
		IngressPool:        testPools.Ingress,
		JobsClient:         testJobs,
		AttendanceService:  testAttendanceSvc,
		MaterialResolver:   testConfigServer,
		CredentialReporter: testConfigServer,
		DriverFactory:      driverFactory,
		Logger:             slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("falha ao criar workers: %v", err)
	}
	if handlers.Ingress == nil || handlers.Inbound == nil || handlers.Outbound == nil || handlers.Lease == nil {
		t.Fatal("algum handler de NewWorkers retornou nil")
	}
}

func TestDispatch_RealHttptest_CrashRecoveryAndRiverRestart(t *testing.T) {
	ctx := context.Background()
	appID := "app-crash-test"
	appSecret := "secret-crash-test"
	st := seedTenantWithMeta(t, appID, "waba-crash-t", "5511999990003", "tok-crash", appSecret)

	var remoteCalls atomic.Int64
	var mu sync.Mutex
	var seenCallbackData []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var p struct {
			To                    string `json:"to"`
			BizOpaqueCallbackData string `json:"biz_opaque_callback_data"`
		}
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		seenCallbackData = append(seenCallbackData, p.BizOpaqueCallbackData)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.real.httptest.001"}]}`))
	}))
	defer ts.Close()

	driverFactory, err := NewDriverFactory(DriverFactoryConfig{
		Simulation: true,
		StandInURL: ts.URL,
	})
	if err != nil {
		t.Fatalf("falha ao criar factory: %v", err)
	}

	attemptID, _ := createTestAttempt(t, st, "mensagem teste de queda real", "req-crash-real-001")

	// 1. Worker reserves dispatch and transitions attempt to 'sending'
	workerID := uuid.New()
	_, err = testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
		TenantID:     st.tenantID,
		InboxID:      st.inboxID,
		AttemptID:    attemptID,
		OwnerID:      workerID,
		LeaseVersion: 0,
		ExpiresAt:    time.Now().Add(time.Minute),
		At:           time.Now(),
	})
	if err != nil {
		t.Fatalf("falha ao reservar dispatch: %v", err)
	}

	// Perform the remote dispatch outside transaction to ts.URL
	mat, err := testConfigServer.ResolveMaterial(ctx, st.tenantID, st.connectorID, 1)
	if err != nil {
		t.Fatalf("falha ao resolver material: %v", err)
	}
	driver, err := driverFactory.DriverFor(ctx, st.tenantID, st.connectorID, mat)
	if err != nil {
		t.Fatalf("falha ao obter driver: %v", err)
	}
	resp, _ := driver.SendText(ctx, whatsapp.TextRequest{
		AttemptID:     whatsapp.AttemptID(attemptID.String()),
		ConnectionID:  whatsapp.ConnectionID(st.connectorID.String()),
		ConnectorType: whatsapp.ConnectorMetaCloud,
		RecipientID:   whatsapp.RecipientID(st.phoneID),
		CallbackData:  attemptID.String(),
		Text:          "mensagem teste de queda real",
	})
	if resp.Local != whatsapp.StateAccepted {
		t.Fatalf("esperado aceite remoto pelo httptest, obteve %+v", resp)
	}

	if remoteCalls.Load() != 1 {
		t.Fatalf("esperado exatamente 1 chamada remota, obteve %d", remoteCalls.Load())
	}
	mu.Lock()
	if len(seenCallbackData) != 1 || seenCallbackData[0] != attemptID.String() {
		t.Fatalf("callback_data não enviado ao servidor remoto: %v", seenCallbackData)
	}
	mu.Unlock()

	// 2. NOW simulate process crash/kill before RecordAttemptResult was persisted.
	// 3. Restart River workers with a REAL working client
	handlers, err := NewWorkers(WorkersConfig{
		DB:                 testStore,
		IngressPool:        testPools.Ingress,
		JobsClient:         testJobs,
		AttendanceService:  testAttendanceSvc,
		MaterialResolver:   testConfigServer,
		CredentialReporter: testConfigServer,
		DriverFactory:      driverFactory,
		Logger:             slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("falha ao criar handlers: %v", err)
	}

	workerClient, err := jobs.New(testJobsPool, handlers, jobs.WithTestTiming(10*time.Millisecond, jobs.Backoff{}, 50*time.Millisecond))
	if err != nil {
		t.Fatalf("falha ao criar worker River: %v", err)
	}
	if err := workerClient.Start(ctx); err != nil {
		t.Fatalf("falha ao iniciar worker River: %v", err)
	}
	defer func() { _ = workerClient.Stop(ctx) }()

	// Enqueue the outbound job on the crashed attempt into River
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := testJobs.EnqueueOutbound(ctx, tx, st.tenantID, attemptID)
		return err
	})
	if err != nil {
		t.Fatalf("falha ao enfileirar job outbound: %v", err)
	}

	// Wait for River worker to process the job and transition attempt to uncertain
	var attemptState string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id = $1`, attemptID).Scan(&attemptState)
		})
		if attemptState == "uncertain" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if attemptState != "uncertain" {
		t.Fatalf("esperado tentativa recuperada para uncertain após restart River, obteve %s", attemptState)
	}

	// 4. Verification: River worker must NOT have made a second remote call!
	if remoteCalls.Load() != 1 {
		t.Fatalf("segunda chamada remota indevida após reinício: %d chamadas", remoteCalls.Load())
	}
}

func TestOutbound_ConcurrentLease_Dispute(t *testing.T) {
	ctx := context.Background()
	appID := "app-lease-dispute"
	appSecret := "secret-lease-dispute"
	st := seedTenantWithMeta(t, appID, "waba-lease-d", "5511999990004", "tok-lease", appSecret)

	attemptID, _ := createTestAttempt(t, st, "mensagem disputa", "req-lease-001")

	// Two workers dispute lease simultaneously
	worker1 := uuid.New()
	worker2 := uuid.New()

	var winCount atomic.Int64
	var heldCount atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)

	tryReserve := func(wID uuid.UUID) {
		defer wg.Done()
		_, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{
			TenantID:     st.tenantID,
			InboxID:      st.inboxID,
			AttemptID:    attemptID,
			OwnerID:      wID,
			LeaseVersion: 0,
			ExpiresAt:    time.Now().Add(time.Minute),
			At:           time.Now(),
		})
		if err == nil {
			winCount.Add(1)
		} else if errors.Is(err, store.ErrLeaseHeld) || errors.Is(err, store.ErrInvalid) {
			heldCount.Add(1)
		}
	}

	go tryReserve(worker1)
	go tryReserve(worker2)
	wg.Wait()

	if winCount.Load() != 1 {
		t.Fatalf("esperado exatamente 1 vencedor na disputa de lease, obteve %d (held=%d)", winCount.Load(), heldCount.Load())
	}
	if heldCount.Load() != 1 {
		t.Fatalf("esperado exatamente 1 perdedor rejeitado por lease ocupada, obteve %d", heldCount.Load())
	}
}

func TestOutbound_NoisyTenant_Fairness(t *testing.T) {
	ctx := context.Background()
	appIDA := "app-noisy-a"
	appIDB := "app-noisy-b"
	tenantA := seedTenantWithMeta(t, appIDA, "waba-noisy-a", "5511999990088", "tok-na", "sec-a")
	tenantB := seedTenantWithMeta(t, appIDB, "waba-noisy-b", "5511999990089", "tok-nb", "sec-b")

	leaseCoord := NewOutboundLease(testStore)

	// In standard/default configuration without manual overrides, max outbound slots is capped at 1
	// to prevent any single tenant from capturing both River outbound workers (total capacity = 2).
	// Simulate Tenant A currently running 1 active outbound send:
	slotA, err := testAttendanceSvc.ReserveWorker(ctx, tenantA.tenantID, uuid.New(), "outbound", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("falha ao reservar slot para Tenant A: %v", err)
	}
	defer func() {
		_ = testStore.WithTenantTx(ctx, tenantA.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM tenant_job_reservations WHERE id = $1`, slotA.ID)
			return err
		})
	}()

	// Tenant A attempts a second concurrent outbound send:
	// AllowOutbound must return false (snoozed) by default!
	allowedA, err := leaseCoord.AllowOutbound(ctx, tenantA.tenantID, uuid.New())
	if err != nil {
		t.Fatalf("erro em AllowOutbound tenant A: %v", err)
	}
	if allowedA {
		t.Fatal("Tenant A não deveria ser permitido além do teto padrão de 1 slot que previne monopólio dos workers")
	}

	// Tenant B attempts outbound send with real backlog:
	// AllowOutbound must return true, proving Tenant B progresses despite Tenant A's backlog!
	attemptB, _ := createTestAttempt(t, tenantB, "mensagem progresso tenant B", "req-noisy-b-001")
	allowedB, err := leaseCoord.AllowOutbound(ctx, tenantB.tenantID, attemptB)
	if err != nil {
		t.Fatalf("erro em AllowOutbound tenant B: %v", err)
	}
	if !allowedB {
		t.Fatal("Tenant B deveria ser permitido e progredir sem sofrer inanição por Tenant A ruidoso")
	}
}

func TestExpansion_MixedInvalidPhone(t *testing.T) {
	ctx := context.Background()
	appID, phone := "app-mixed-invalid", "5511966660111"
	tenant := seedTenantWithMeta(t, appID, "waba-mixed-invalid", phone, "tok-mixed", "secret-mixed-invalid")
	body := []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"waba-mixed-invalid","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"%s"},"messages":[{"from":"5511911113333","id":"wamid.mixed.valid","timestamp":"1700000040","text":{"body":"ok"},"type":"text"}]}},{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"%s"},"messages":[{"from":"5511911113333","id":"wamid.mixed.invalid","timestamp":"1700000040","text":{"body":"bad"},"type":"text"}]}}]}]}`, phone, strings.Repeat("9", 65)))
	sum := sha256.Sum256(body)
	id := uuid.New()
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ingress_deliveries (id,source,app_id,body_sha256,body,state) VALUES ($1,'meta_cloud',$2,$3,$4,'stored')`, id, appID, sum[:], body)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue='inbound'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, id); err != nil {
		t.Fatal(err)
	}
	var items, quarantine int
	if err := testStore.WithTenantTx(ctx, tenant.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1`, tenant.tenantID).Scan(&items)
	}); err != nil {
		t.Fatal(err)
	}
	if err := testPools.Ingress.QueryRow(ctx, `SELECT count(*) FROM ingress_quarantine WHERE delivery_id=$1`, id).Scan(&quarantine); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := testPools.Owner.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue='inbound'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("jobs antes=%d depois=%d", before, after)
	}
	if items != 1 || quarantine != 1 {
		t.Fatalf("items=%d quarantine=%d", items, quarantine)
	}
}

// TestTenantStatusGateDispatch exercises the real boundary and administrative gate.
func TestTenantStatusGateDispatch(t *testing.T) {
	for _, suspensionFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(suspensionFirst), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			suffix := fmt.Sprint(suspensionFirst)
			st := seedTenantWithMeta(t, "gate-app-"+suffix, "gate-waba-"+suffix, map[bool]string{true: "5511988880888", false: "5511988880889"}[suspensionFirst], "gate-token", "gate-secret-12345")
			attempt, _ := createTestAttempt(t, st, "gate", "gate-"+suffix)
			admin, err := pgx.Connect(ctx, testEnv.AdminURL)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close(context.Background())
			ready := make(chan struct{})
			release := make(chan struct{})
			doneBlock := make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
				go func() {
					if suspensionFirst {
						doneBlock <- testStore.WithPlatform(ctx, func(c context.Context) error {
							if _, e := testStore.SetTenantStatus(c, st.tenantID, "suspended"); e != nil {
								return e
							}
							close(ready)
							select {
							case <-release:
								return nil
							case <-ctx.Done():
								return ctx.Err()
							}
						})
					} else {
						doneBlock <- testStore.WithTenantTx(ctx, st.tenantID, func(c context.Context, tx pgx.Tx) error {
							var id uuid.UUID
							if e := tx.QueryRow(c, `SELECT id FROM memberships WHERE id=$1 FOR UPDATE`, st.memberID).Scan(&id); e != nil {
								return e
							}
							close(ready)
							select {
							case <-release:
								return nil
							case <-ctx.Done():
								return ctx.Err()
							}
						})
					}
				}()
				select {
				case <-ready:
				case <-ctx.Done():
				}
			}}
			driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.gate"}
			factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
			dispatch := make(chan error, 1)
			go func() { dispatch <- handler.DispatchOutbound(ctx, st.tenantID, attempt) }()
			waitLock := func(pattern string) {
				t.Helper()
				for {
					var waiting bool
					e := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting)
					if e != nil {
						t.Fatal(e)
					}
					if waiting {
						return
					}
					if ctx.Err() != nil {
						t.Fatal(ctx.Err())
					}
				}
			}
			var suspension chan error
			if suspensionFirst {
				waitLock("SELECT pg_advisory_xact_lock%")
			} else {
				waitLock("SELECT role, status FROM memberships WHERE tenant_id=%")
				suspension = make(chan error, 1)
				go func() {
					suspension <- testStore.WithPlatform(ctx, func(c context.Context) error {
						_, e := testStore.SetTenantStatus(c, st.tenantID, "suspended")
						return e
					})
				}()
				waitLock("SELECT pg_advisory_xact_lock%")
			}
			unlock()
			if err := <-doneBlock; err != nil {
				t.Fatal(err)
			}
			dispatchErr := <-dispatch
			if suspension != nil {
				if err := <-suspension; err != nil {
					t.Fatal(err)
				}
			}
			if suspensionFirst {
				if dispatchErr != nil || driver.CallCount() != 0 {
					t.Fatalf("dispatch=%v calls=%d", dispatchErr, driver.CallCount())
				}
				var state, code string
				if err := admin.QueryRow(ctx, `SELECT state, COALESCE(error_code,'') FROM outbound_attempts WHERE id=$1`, attempt).Scan(&state, &code); err != nil {
					t.Fatal(err)
				}
				if state != "failed" || code != "despacho_invalidado" {
					t.Fatalf("state=%s code=%s", state, code)
				}
			} else if driver.CallCount() != 1 {
				t.Fatalf("boundary won: dispatch=%v calls=%d", dispatchErr, driver.CallCount())
			}
		})
	}
}

func TestReviewTenantGateExpiredFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-gate-expiry-app", "review-gate-expiry-waba", "5511988880777", "review-token", "review-secret")
	attempt, _ := createTestAttempt(t, st, "gate expiry", "review-gate-expiry")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	blockDone := make(chan error, 1)
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		go func() {
			blockDone <- testStore.WithTenantTx(ctx, st.tenantID, func(c context.Context, _ pgx.Tx) error {
				if err := testStore.LockTenant(c); err != nil {
					return err
				}
				close(ready)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-ready:
		case <-ctx.Done():
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.expiry"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	dispatch := make(chan error, 1)
	go func() { dispatch <- handler.DispatchOutbound(ctx, st.tenantID, attempt) }()
	for {
		var blocked bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT pg_advisory_xact_lock%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
	}
	// Deterministic elapsed-lease fixture: move each deadline to PostgreSQL's
	// current clock after the waiting boundary transaction has already begun.
	// All deadlines are expired before the gate is released; no wall-clock sleep.
	for _, table := range []string{"inbox_send_leases", "send_reservations", "tenant_job_reservations"} {
		if _, err := admin.Exec(ctx, "UPDATE "+table+" SET expires_at=clock_timestamp() WHERE tenant_id=$1", st.tenantID); err != nil {
			t.Fatal(err)
		}
	}
	var expired bool
	if err := admin.QueryRow(ctx, `SELECT expires_at < clock_timestamp() FROM inbox_send_leases WHERE tenant_id=$1`, st.tenantID).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if !expired {
		t.Fatal("fixture lease still live")
	}
	unlock()
	if err := <-blockDone; err != nil {
		t.Fatal(err)
	}
	dispatchErr := <-dispatch
	if driver.CallCount() != 0 {
		t.Fatalf("expired fence dispatched after waiting at tenant gate: calls=%d dispatch=%v", driver.CallCount(), dispatchErr)
	}
}

func TestReviewSuspendedTenantExpiredFenceDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-suspended-expiry-app", "review-suspended-expiry-waba", "5511988880778", "review-token", "review-secret")
	attempt, _ := createTestAttempt(t, st, "suspended expired fence", "review-suspended-expiry")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		// Model expiration during preparation, followed by committed suspension,
		// with no external effect and before the dispatch boundary begins.
		if _, err := admin.Exec(ctx, `UPDATE inbox_send_leases SET expires_at=clock_timestamp() WHERE tenant_id=$1`, st.tenantID); err != nil {
			t.Fatal(err)
		}
		if err := testStore.WithPlatform(ctx, func(c context.Context) error {
			_, err := testStore.SetTenantStatus(c, st.tenantID, "suspended")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.suspended.expiry"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	dispatchErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	retryErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	var state, code string
	if err := admin.QueryRow(ctx, `SELECT state, coalesce(error_code,'') FROM outbound_attempts WHERE id=$1`, attempt).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if driver.CallCount() != 0 {
		t.Fatalf("suspended tenant called driver: %d", driver.CallCount())
	}
	if state == "sending" {
		t.Fatalf("committed suspension left undispatched attempt abandoned: state=%s code=%q dispatch=%v retry=%v calls=%d", state, code, dispatchErr, retryErr, driver.CallCount())
	}
}

func TestDispatchSuspensionWithPreparationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-prep-suspend-app", "review-prep-suspend-waba", "5511988880766", "review-token", "review-secret")
	attempt, _ := createTestAttempt(t, st, "suspended preparation", "review-prep-suspend")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	resolver := &delayedMaterialResolver{underlying: testConfigServer, onResolve: func() {
		// Real configuration changes invalidate the generation captured by the
		// worker. Both changes commit before ResolveMaterial returns its error.
		if err := testConfigServer.ReportInvalidCredential(ctx, st.tenantID, st.connectorID, 1); err != nil {
			t.Fatal(err)
		}
		if err := testStore.WithPlatform(ctx, func(c context.Context) error {
			_, err := testStore.SetTenantStatus(c, st.tenantID, "suspended")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.prep.suspend"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	dispatchErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	retryErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	var state, code, tenantStatus string
	if err := admin.QueryRow(ctx, `SELECT o.state, coalesce(o.error_code,''), t.status FROM outbound_attempts o JOIN tenants t ON t.id=o.tenant_id WHERE o.id=$1`, attempt).Scan(&state, &code, &tenantStatus); err != nil {
		t.Fatal(err)
	}
	if tenantStatus != "suspended" {
		t.Fatalf("suspension did not commit: %s", tenantStatus)
	}
	if driver.CallCount() != 0 {
		t.Fatalf("driver called: %d", driver.CallCount())
	}
	if dispatchErr != nil || state != "failed" || code != "despacho_invalidado" {
		t.Fatalf("suspension committed before preparation returned but attempt abandoned: state=%s code=%q dispatch=%v retry=%v calls=%d", state, code, dispatchErr, retryErr, driver.CallCount())
	}
}

func TestReviewSuspensionWithExpiredWorkerSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-slot-suspend-app", "review-slot-suspend-waba", "5511988880744", "review-token", "review-secret")
	attempt, _ := createTestAttempt(t, st, "suspended expired slot", "review-slot-suspend")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	var leaseOwner uuid.UUID
	var leaseVersion int64
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		// Deterministically model a slot that expired during preparation. Another
		// worker reserves capacity using the real service, which removes the old
		// expired slot; it has not taken over the inbox lease or send reservation.
		for _, table := range []string{"inbox_send_leases", "send_reservations", "tenant_job_reservations"} {
			if _, err := admin.Exec(ctx, "UPDATE "+table+" SET expires_at=clock_timestamp() WHERE tenant_id=$1", st.tenantID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := testAttendanceSvc.ReserveWorker(ctx, st.tenantID, uuid.New(), "outbound", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, `SELECT owner_id, lease_version FROM inbox_send_leases WHERE tenant_id=$1`, st.tenantID).Scan(&leaseOwner, &leaseVersion); err != nil {
			t.Fatal(err)
		}
		if err := testStore.WithPlatform(ctx, func(c context.Context) error {
			_, err := testStore.SetTenantStatus(c, st.tenantID, "suspended")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.slot.suspend"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	dispatchErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	retryErr := handler.DispatchOutbound(ctx, st.tenantID, attempt)
	var state, code, tenantStatus string
	var sameOwner bool
	if err := admin.QueryRow(ctx, `SELECT o.state, coalesce(o.error_code,''), t.status, r.owner_id=$2 AND r.lease_version=$3 FROM outbound_attempts o JOIN tenants t ON t.id=o.tenant_id JOIN send_reservations r ON r.tenant_id=o.tenant_id AND r.attempt_id=o.id WHERE o.id=$1`, attempt, leaseOwner, leaseVersion).Scan(&state, &code, &tenantStatus, &sameOwner); err != nil {
		t.Fatal(err)
	}
	t.Logf("state=%s code=%q tenant=%s sameOwner=%v dispatch=%v retry=%v calls=%d", state, code, tenantStatus, sameOwner, dispatchErr, retryErr, driver.CallCount())
	if tenantStatus != "suspended" || !sameOwner {
		t.Fatal("fixture did not preserve committed suspension and original send owner")
	}
	if driver.CallCount() != 0 {
		t.Fatal("suspended tenant dispatched")
	}
	if state == "sending" {
		t.Fatalf("committed suspension leaves undispatched attempt in sending after worker-slot expiration: dispatch=%v retry=%v", dispatchErr, retryErr)
	}
	if state != "failed" || code != "despacho_invalidado" {
		t.Fatalf("expected failed/despacho_invalidado, got state=%s code=%s", state, code)
	}
}

func TestReviewBoundaryRecoveryLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-deadlock-app", "review-deadlock-waba", "5511988880755", "review-token", "review-secret")
	attempt, _ := createTestAttempt(t, st, "boundary recovery", "review-boundary-recovery")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		close(ready)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.deadlock"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	dispatch := make(chan error, 1)
	go func() { dispatch <- handler.DispatchOutbound(ctx, st.tenantID, attempt) }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var owner uuid.UUID
	var version int64
	if err := admin.QueryRow(ctx, `SELECT owner_id, lease_version FROM send_reservations WHERE tenant_id=$1 AND attempt_id=$2`, st.tenantID, attempt).Scan(&owner, &version); err != nil {
		t.Fatal(err)
	}
	blocker, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var id uuid.UUID
	if err := blocker.QueryRow(ctx, `SELECT id FROM outbound_attempts WHERE id=$1 FOR UPDATE`, attempt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	recovery := make(chan error, 1)
	go func() {
		_, err := testAttendanceSvc.RecoverCrashedDispatch(ctx, st.tenantID, attempt, owner, version)
		recovery <- err
	}()
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	waitFor := func(fragment string) {
		t.Helper()
		for {
			var blocked bool
			if err := observer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE usename='chatisp_app' AND wait_event_type='Lock' AND position($1 in query)>0)`, fragment).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
		}
	}
	waitFor("SELECT lease_version")
	unlock()
	waitFor("SELECT c.id, o.controller_epoch")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	dispatchErr, recoveryErr := <-dispatch, <-recovery
	t.Logf("dispatch=%v recovery=%v calls=%d", dispatchErr, recoveryErr, driver.CallCount())
	if (dispatchErr != nil && strings.Contains(dispatchErr.Error(), "40P01")) || (recoveryErr != nil && strings.Contains(recoveryErr.Error(), "40P01")) {
		t.Fatalf("opposite lock order deadlocked real dispatch and recovery: dispatch=%v recovery=%v", dispatchErr, recoveryErr)
	}
}

func TestReviewBoundaryReserveLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-reserve-order-app", "review-reserve-order-waba", "5511988880733", "review-token", "review-secret")
	attemptA, _ := createTestAttempt(t, st, "boundary A", "review-reserve-order-a")
	attemptB, _ := createTestAttempt(t, st, "reserve B", "review-reserve-order-b")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	blocker, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var member uuid.UUID
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		close(ready)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.reserve-order"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	handlerA := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	handlerB := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() { doneA <- handlerA.DispatchOutbound(ctx, st.tenantID, attemptA) }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := blocker.QueryRow(ctx, `SELECT id FROM memberships WHERE id=$1 FOR UPDATE`, st.memberID).Scan(&member); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"inbox_send_leases", "send_reservations", "tenant_job_reservations"} {
		if _, err := observer.Exec(ctx, "UPDATE "+table+" SET expires_at=clock_timestamp() WHERE tenant_id=$1", st.tenantID); err != nil {
			t.Fatal(err)
		}
	}
	unlock()
	waitMembers := func() {
		t.Helper()
		for {
			var waiting int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename='chatisp_app' AND wait_event_type='Lock' AND query LIKE '%SELECT role, status%' AND query LIKE '%FROM memberships%'`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting >= 1 {
				return
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
		}
	}
	waitMembers()
	go func() { doneB <- handlerB.DispatchOutbound(ctx, st.tenantID, attemptB) }()
	waitTwoWaiting := func() {
		t.Helper()
		for {
			var waiting int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename='chatisp_app' AND wait_event_type='Lock'`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting >= 2 {
				return
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
		}
	}
	waitTwoWaiting()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	errA, errB := <-doneA, <-doneB
	t.Logf("boundary=%v reserve=%v calls=%d", errA, errB, driver.CallCount())
	if (errA != nil && strings.Contains(errA.Error(), "40P01")) || (errB != nil && strings.Contains(errB.Error(), "40P01")) {
		t.Fatalf("boundary and actual reserve worker deadlocked: A=%v B=%v", errA, errB)
	}
}

func TestReviewSuspensionAfterInboxLeaseTakeover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := seedTenantWithMeta(t, "review-takeover-suspend-app", "review-takeover-suspend-waba", "5511988880722", "review-token", "review-secret")
	attemptA, _ := createTestAttempt(t, st, "old attempt", "review-takeover-a")
	attemptB, _ := createTestAttempt(t, st, "new attempt", "review-takeover-b")
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		for _, table := range []string{"inbox_send_leases", "send_reservations", "tenant_job_reservations"} {
			if _, err := admin.Exec(ctx, "UPDATE "+table+" SET expires_at=clock_timestamp() WHERE tenant_id=$1", st.tenantID); err != nil {
				t.Fatal(err)
			}
		}
		_, err := testAttendanceSvc.ReserveDispatch(ctx, service.ReserveInput{TenantID: st.tenantID, InboxID: st.inboxID, AttemptID: attemptB, OwnerID: uuid.New(), ExpiresAt: time.Now().Add(time.Minute), At: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := testStore.WithPlatform(ctx, func(c context.Context) error {
			_, err := testStore.SetTenantStatus(c, st.tenantID, "suspended")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.review.takeover"}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	h := NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory)
	first := h.DispatchOutbound(ctx, st.tenantID, attemptA)
	retry := h.DispatchOutbound(ctx, st.tenantID, attemptA)
	var state, tenantStatus, resState string
	var ownAttemptFence bool
	if err := admin.QueryRow(ctx, `SELECT o.state,t.status,r.state,r.lease_version=o.lease_version FROM outbound_attempts o JOIN tenants t ON t.id=o.tenant_id JOIN send_reservations r ON r.tenant_id=o.tenant_id AND r.attempt_id=o.id WHERE o.id=$1`, attemptA).Scan(&state, &tenantStatus, &resState, &ownAttemptFence); err != nil {
		t.Fatal(err)
	}
	t.Logf("first=%v retry=%v state=%s tenant=%s reservation=%s ownAttemptFence=%v calls=%d", first, retry, state, tenantStatus, resState, ownAttemptFence, driver.CallCount())
	if tenantStatus != "suspended" || !ownAttemptFence {
		t.Fatal("fixture lacks committed suspension/original attempt fence")
	}
	if driver.CallCount() != 0 {
		t.Fatal("suspended dispatch called driver")
	}
	if state == "sending" {
		t.Fatal("committed suspension leaves original undispatched attempt abandoned in sending after a different attempt acquired the inbox lease")
	}
}
