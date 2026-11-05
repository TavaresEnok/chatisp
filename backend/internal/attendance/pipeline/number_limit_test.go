package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
)

func limitBody(waba, display, current string) []byte {
	return []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":%q,"changes":[{"field":"phone_number_quality_update","value":{"display_phone_number":%q,"event":"THROUGHPUT_UPGRADE","current_limit":%q,"old_limit":"TIER_250","max_daily_conversations_per_business":2000}}]}]}`, waba, display, current))
}

func expandLimit(t *testing.T, appID string, body []byte) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	sum := sha256.Sum256(append(body, id[:]...))
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ingress_deliveries (id,source,app_id,body_sha256,body,state) VALUES ($1,'meta_cloud',$2,$3,$4,'stored')`, id, appID, sum[:], body)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, id); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := testPools.Ingress.QueryRow(ctx, `SELECT state FROM ingress_deliveries WHERE id=$1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return id, state
}

func limitRows(t *testing.T, tenant uuid.UUID) []string {
	t.Helper()
	var out []string
	if err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT phone_id::text || ':' || current_limit || ':' || coalesce(old_limit,'') || ':' || max_daily FROM meta_number_health WHERE kind='limit' ORDER BY received_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func quarantineReason(t *testing.T, delivery uuid.UUID) string {
	t.Helper()
	var reason string
	if err := testPools.Ingress.QueryRow(context.Background(), `SELECT reason FROM ingress_quarantine WHERE delivery_id=$1`, delivery).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	return reason
}

func TestNumberLimitRoutesByAppWABAAndDisplayDigits(t *testing.T) {
	appID, waba, phone := "app-limit-a", "waba-limit-a", "5511955550101"
	a := seedTenantWithMeta(t, appID, waba, phone, "tok-limit-a", "secret-limit-a")
	other := seedTenantWithMeta(t, "app-limit-b", "waba-limit-b", "5511955550202", "tok-limit-b", "secret-limit-b")

	// The fixture stores "+<phone>"; the webhook sends bare digits.
	delivery, state := expandLimit(t, appID, limitBody(waba, phone, "TIER_2K"))
	if state != "routed" {
		t.Fatalf("estado: %s", state)
	}
	got := limitRows(t, a.tenantID)
	if len(got) != 1 || got[0] != a.phoneRowID.String()+":TIER_2K:TIER_250:2000" {
		t.Fatalf("linhas: %v", got)
	}
	if rows := limitRows(t, other.tenantID); len(rows) != 0 {
		t.Fatalf("vazou para outro tenant: %v", rows)
	}
	var quarantined int
	_ = testPools.Ingress.QueryRow(context.Background(), `SELECT count(*) FROM ingress_quarantine WHERE delivery_id=$1`, delivery).Scan(&quarantined)
	if quarantined != 0 {
		t.Fatalf("quarentena indevida: %d", quarantined)
	}

	// Meta retries the same change: same key, no second row.
	if _, state := expandLimit(t, appID, limitBody(waba, phone, "TIER_2K")); state != "routed" {
		t.Fatalf("repetição: %s", state)
	}
	if got := limitRows(t, a.tenantID); len(got) != 1 {
		t.Fatalf("repetição duplicou: %v", got)
	}

	// A different app cannot write into this WABA, even with the right number.
	d, state := expandLimit(t, "app-limit-b", limitBody(waba, phone, "TIER_10K"))
	if state != "quarantined" || quarantineReason(t, d) != reasonUnknownDisplay {
		t.Fatalf("app trocado: %s", state)
	}
	// Unknown display number in the right WABA.
	d, state = expandLimit(t, appID, limitBody(waba, "5511900000000", "TIER_10K"))
	if state != "quarantined" || quarantineReason(t, d) != reasonUnknownDisplay {
		t.Fatalf("número desconhecido: %s", state)
	}
	if got := limitRows(t, a.tenantID); len(got) != 1 {
		t.Fatalf("quarentena gravou limite: %v", got)
	}
}

func TestNumberLimitAmbiguousDisplayIsQuarantined(t *testing.T) {
	appID, waba, phone := "app-limit-amb", "waba-limit-amb", "5511955550303"
	st := seedTenantWithMeta(t, appID, waba, phone, "tok-limit-amb", "secret-limit-amb")
	_, _ = seedSecondBoxForTenant(t, st, "5511955550304")
	// Make the second number show the same display digits, formatted differently.
	if err := testStore.WithTenantTx(context.Background(), st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE meta_phone_numbers SET display_number=$2 WHERE tenant_id=$1 AND phone_number_id='5511955550304'`, st.tenantID, "+55 (11) 95555-0303")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d, state := expandLimit(t, appID, limitBody(waba, phone, "TIER_2K"))
	if state != "quarantined" || quarantineReason(t, d) != reasonAmbiguousDisplay {
		t.Fatalf("ambíguo: %s", state)
	}
	if got := limitRows(t, st.tenantID); len(got) != 0 {
		t.Fatalf("ambíguo gravou: %v", got)
	}
}

func TestNumberLimitIngressCannotWriteQualityOrReadValues(t *testing.T) {
	st := seedTenantWithMeta(t, "app-limit-rls", "waba-limit-rls", "5511955550404", "tok-limit-rls", "secret-limit-rls")
	ctx := context.Background()
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO meta_number_health (tenant_id,id,phone_id,kind,dedup_key,quality_rating) VALUES ($1,$2,$3,'quality','x','GREEN')`, st.tenantID, uuid.New(), st.phoneRowID)
		return err
	})
	if err == nil {
		t.Fatal("ingresso gravou leitura de qualidade")
	}
	err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var s *string
		return tx.QueryRow(ctx, `SELECT current_limit FROM meta_number_health LIMIT 1`).Scan(&s)
	})
	if err == nil {
		t.Fatal("ingresso leu valores de limite")
	}
}
