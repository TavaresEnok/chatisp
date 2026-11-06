package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"chatisp/internal/opsalert"
	"chatisp/internal/platform/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSuspendedIngressConfigRequiresExplicitLimits(t *testing.T) {
	if cfg, err := SuspendedIngressFromEnv(nil); err != nil || cfg.Enabled {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	for _, env := range [][]string{
		{"CHATISP_SUSPENDED_INGRESS_ENABLED=invalid"},
		{"CHATISP_SUSPENDED_INGRESS_ENABLED=true"},
		{"CHATISP_SUSPENDED_INGRESS_ENABLED=true", "CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES=2", "CHATISP_SUSPENDED_INGRESS_MAX_BYTES=0"},
	} {
		if _, err := SuspendedIngressFromEnv(env); err == nil {
			t.Fatalf("config sem limites aceita: %v", env)
		}
	}
	cfg, err := SuspendedIngressFromEnv([]string{"CHATISP_SUSPENDED_INGRESS_ENABLED=true", "CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES=2", "CHATISP_SUSPENDED_INGRESS_MAX_BYTES=4096"})
	if err != nil || cfg.MaxDeliveries != 2 || cfg.MaxBytes != 4096 {
		t.Fatalf("config: %+v %v", cfg, err)
	}
}

func suspensionBody(st seededTenant, id string) []byte {
	return []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":%q,"changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":%q},"messages":[{"from":"5511988881111","id":%q,"timestamp":"1700000000","type":"text","text":{"body":"mensagem sintética retida"}}]}}]}]}`, st.wabaID, st.phoneID, id))
}

func suspensionHandler(t *testing.T, st seededTenant, cfg SuspendedIngressConfig) *WebhookHandler {
	t.Helper()
	h, err := NewWebhookHandler(WebhookConfig{AppID: st.appID, AppResolver: testConfigServer, VerifyTokens: StaticVerifyTokens{st.appID: "verify-suspension-distinct"}, IngressPool: testPools.Ingress, JobsClient: testJobs, SuspendedIngress: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func deliverSuspension(h *WebhookHandler, st seededTenant, body []byte) int {
	req := httptest.NewRequest(http.MethodPost, "/meta/webhook?app_id="+st.appID, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signPayload(st.appSecret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func setSuspension(t *testing.T, st seededTenant, status string) {
	t.Helper()
	if err := testStore.WithPlatform(context.Background(), func(c context.Context) error { _, err := testStore.SetTenantStatus(c, st.tenantID, status); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestSuspendedIngressBoundedReplayAndManualReconciliation(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "suspend-bounded", "suspend-waba", "5511999900881", "test-token", "test-suspension-secret")
	setSuspension(t, st, "suspended")
	body := suspensionBody(st, "wamid.suspended.001")
	h := suspensionHandler(t, st, SuspendedIngressConfig{Enabled: true, MaxDeliveries: 1, MaxBytes: int64(len(body))})
	if status := deliverSuspension(h, st, body); status != 200 {
		t.Fatalf("primeira entrega: %d", status)
	}
	if status := deliverSuspension(h, st, body); status != 200 {
		t.Fatalf("replay no limite deve confirmar retenção já persistida: %d", status)
	}
	if status := deliverSuspension(h, st, suspensionBody(st, "wamid.suspended.002")); status == 200 {
		t.Fatal("limite de entregas ignorado")
	}
	var count int
	if err := testPools.Ingress.QueryRow(ctx, `SELECT count(*) FROM ingress_deliveries WHERE app_id=$1`, st.appID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("entrega recusada persistiu conteúdo: %d %v", count, err)
	}
	var delivery uuid.UUID
	if err := testPools.Ingress.QueryRow(ctx, `SELECT id FROM ingress_deliveries WHERE app_id=$1`, st.appID).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	// Reactivation deliberately precedes expansion: held data stays inert.
	setSuspension(t, st, "active")
	if err := NewIngressHandler(testPools.Ingress, testJobs).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(c context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(c, `SELECT state FROM webhook_items WHERE tenant_id=$1 AND delivery_id=$2`, st.tenantID, delivery).Scan(&state); err != nil {
			return err
		}
		if state != "quarantined" {
			t.Fatalf("retido executável após reativação: %s", state)
		}
		view, err := opsalert.Refresh(c, tx, st.tenantID, time.Now())
		if err != nil {
			return err
		}
		found := false
		for _, a := range view.Alerts {
			if a.Signal == opsalert.SignalSuspendedIngress {
				found = true
			}
		}
		if !found {
			t.Fatal("alerta persistido não apareceu no painel")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testJobsPool.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue='inbound' AND args->>'tenant_id'=$1`, st.tenantID.String()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retenção disparou inbound: %d %v", count, err)
	}
}

func TestSuspendedIngressDisabledAndByteLimitRefuseWithoutPersistence(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  SuspendedIngressConfig
	}{
		{"disabled", SuspendedIngressConfig{}},
		{"bytes", SuspendedIngressConfig{Enabled: true, MaxDeliveries: 10, MaxBytes: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := seedTenantWithMeta(t, "suspend-"+tc.name, "waba-"+tc.name, "551199990088"+map[bool]string{true: "2", false: "3"}[tc.name == "disabled"], "token-test", "test-suspension-secret")
			setSuspension(t, st, "suspended")
			if status := deliverSuspension(suspensionHandler(t, st, tc.cfg), st, suspensionBody(st, "wamid.refused")); status == 200 {
				t.Fatal("recusa confirmou 200")
			}
			var count int
			if err := testPools.Ingress.QueryRow(context.Background(), `SELECT count(*) FROM ingress_deliveries WHERE app_id=$1`, st.appID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("conteúdo recusado persistido: %d %v", count, err)
			}
			if err := testStore.WithPlatform(context.Background(), func(c context.Context) error {
				// A platform transaction can inspect the signal, but cannot operate attendance.
				tx, err := testPools.Platform.Begin(c)
				if err != nil {
					return err
				}
				defer tx.Rollback(c)
				if _, err := tx.Exec(c, `SELECT set_config('app.access_scope','platform',true)`); err != nil {
					return err
				}
				var rejected int64
				if err := tx.QueryRow(c, `SELECT rejected_count FROM suspended_ingress_alerts WHERE tenant_id=$1`, st.tenantID).Scan(&rejected); err != nil {
					return err
				}
				if rejected != 1 {
					t.Fatalf("falha não alertou: %d", rejected)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSuspendedIngressConcurrentAdmissionCannotExceedLimit(t *testing.T) {
	st := seedTenantWithMeta(t, "suspend-race", "suspend-race-waba", "5511999900884", "token-test", "test-suspension-secret")
	setSuspension(t, st, "suspended")
	h := suspensionHandler(t, st, SuspendedIngressConfig{Enabled: true, MaxDeliveries: 1, MaxBytes: 10000})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- deliverSuspension(h, st, suspensionBody(st, fmt.Sprint("wamid.concurrent.", i)))
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for status := range results {
		if status == 200 {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("admissão concorrente aceitou %d entregas com limite 1", accepted)
	}
	active := seedTenantWithMeta(t, "suspend-isolation", "suspend-active-waba", "5511999900885", "token-test", "test-suspension-secret")
	if status := deliverSuspension(suspensionHandler(t, active, SuspendedIngressConfig{}), active, suspensionBody(active, "wamid.active")); status != 200 {
		t.Fatalf("outro tenant ativo bloqueado: %d", status)
	}
	if err := db.WithIngressTx(context.Background(), testPools.Ingress, func(c context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(c, `SELECT count(*) FROM suspended_ingress_holds WHERE tenant_id=$1`, active.tenantID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("tenant ativo recebeu retenção de outro")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSuspendedIngressLabelsContainNoPayload(t *testing.T) {
	alerts := opsalert.Derive(opsalert.Limits{}, opsalert.Facts{SuspendedIngress: true, SuspendedAdmissionBlocked: true})
	if len(alerts) != 1 || !strings.Contains(alerts[0].Label, "recusada") {
		t.Fatalf("alerta de recusa: %+v", alerts)
	}
}

func TestSuspendedIngressMixedEnvelopeKeepsTenantPartsSeparate(t *testing.T) {
	ctx := context.Background()
	a := seedTenantWithMeta(t, "mixed-suspend-a", "mixed-waba-a", "5511999900886", "test-token", "shared-suspension-secret")
	b := seedTenantWithMeta(t, "mixed-suspend-b", "mixed-waba-b", "5511999900887", "test-token", "shared-suspension-secret")
	// Synthetic fixture: register the same trusted Meta app for both tenants.
	// Production uses the canonical registry capability flow; no external call.
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err := admin.Exec(ctx, `UPDATE meta_apps SET app_id=$1,registry_id=(SELECT registry_id FROM meta_apps WHERE tenant_id=$2) WHERE tenant_id=$3`, a.appID, a.tenantID, b.tenantID); err != nil {
		t.Fatal(err)
	}
	b.appID = a.appID
	setSuspension(t, b, "suspended")
	mixed := func(suffix string) []byte {
		var ea, eb struct {
			Object string            `json:"object"`
			Entry  []json.RawMessage `json:"entry"`
		}
		if err := json.Unmarshal(suspensionBody(a, "wamid.mixed.a."+suffix), &ea); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(suspensionBody(b, "wamid.mixed.b."+suffix), &eb); err != nil {
			t.Fatal(err)
		}
		ea.Entry = append(ea.Entry, eb.Entry...)
		body, err := json.Marshal(ea)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	h := suspensionHandler(t, a, SuspendedIngressConfig{Enabled: true, MaxDeliveries: 1, MaxBytes: 1 << 20})
	if status := deliverSuspension(h, a, mixed("1")); status != 200 {
		t.Fatalf("envelope misto recusado sem esgotamento: %d", status)
	}
	if status := deliverSuspension(h, a, mixed("2")); status == 200 {
		t.Fatal("ACK misto confirmou perda da parte suspensa")
	}
	setSuspension(t, b, "active")
	var delivery uuid.UUID
	if err := testPools.Ingress.QueryRow(ctx, `SELECT id FROM ingress_deliveries WHERE app_id=$1`, a.appID).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	if err := NewIngressHandler(testPools.Ingress, testJobs).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		st            seededTenant
		expectedState string
		expectedJobs  int
	}{{a, "pending", 1}, {b, "quarantined", 0}} {
		if err := testStore.WithTenantTx(ctx, tc.st.tenantID, func(c context.Context, tx pgx.Tx) error {
			var state string
			if err := tx.QueryRow(c, `SELECT state FROM webhook_items WHERE tenant_id=$1 AND delivery_id=$2`, tc.st.tenantID, delivery).Scan(&state); err != nil {
				return err
			}
			if state != tc.expectedState {
				t.Fatalf("estado tenant %s: %s esperado %s", tc.st.tenantID, state, tc.expectedState)
			}
			var foreign int
			other := a.tenantID
			if tc.st.tenantID == a.tenantID {
				other = b.tenantID
			}
			if err := tx.QueryRow(c, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1`, other).Scan(&foreign); err != nil {
				return err
			}
			if foreign != 0 {
				t.Fatal("RLS expôs parte de outro tenant")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := testJobsPool.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE queue='inbound' AND args->>'tenant_id'=$1`, tc.st.tenantID.String()).Scan(&count); err != nil || count != tc.expectedJobs {
			t.Fatalf("jobs tenant %s: %d esperado %d; %v", tc.st.tenantID, count, tc.expectedJobs, err)
		}
	}
}
