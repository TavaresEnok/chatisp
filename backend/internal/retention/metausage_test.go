package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMetaUsageCountsEachMessageOnceAndStaysInsideTheTenant(t *testing.T) {
	ctx := context.Background()
	s := seedTenant(t)
	other := seedTenant(t)
	svc := Service{DB: testStore}
	at := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	put := func(tenant, inbox uuid.UUID, ext, kind, key string, category *string, billable string, when time.Time) {
		t.Helper()
		err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `INSERT INTO message_status_events (tenant_id,id,connector_id,external_message_id,dedup_key,kind,ingested_at,billing_category,billable)
				SELECT $1,gen_random_uuid(),connector_id,$3,$4,$5,$7,$6,$8 FROM inboxes WHERE tenant_id=$1 AND id=$2`, tenant, inbox, ext, key, kind, category, when, billable)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	str := func(v string) *string { return &v }
	// A: accepted without pricing, then delivered with pricing -> one service/non-billable.
	put(s.tenant, s.inbox, "wamid.A", "accepted", "a1", nil, "unknown", at)
	put(s.tenant, s.inbox, "wamid.A", "delivered", "a2", str("service"), "false", at.Add(time.Minute))
	put(s.tenant, s.inbox, "wamid.A", "read", "a3", nil, "unknown", at.Add(2*time.Minute))
	// B: repeated pricing events for the same message -> one marketing/billable.
	put(s.tenant, s.inbox, "wamid.B", "accepted", "b1", str("marketing"), "true", at)
	put(s.tenant, s.inbox, "wamid.B", "delivered", "b2", str("marketing"), "true", at.Add(time.Minute))
	// C: no pricing at all -> counted as without pricing, never as zero cost.
	put(s.tenant, s.inbox, "wamid.C", "accepted", "c1", nil, "unknown", at)
	// D: outside the window.
	put(s.tenant, s.inbox, "wamid.D", "delivered", "d1", str("utility"), "true", at.AddDate(0, 0, 30))
	// Another tenant with the same category must not leak in.
	put(other.tenant, other.inbox, "wamid.X", "delivered", "x1", str("service"), "false", at)

	got, err := svc.MetaUsage(ctx, s.admin, "2026-11-01", "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"marketing/true": 1, "service/false": 1}
	if len(got.Rows) != len(want) || got.WithoutPricing != 1 {
		t.Fatalf("uso: %+v", got)
	}
	for _, row := range got.Rows {
		if want[row.Category+"/"+row.Billable] != row.Messages {
			t.Fatalf("linha inesperada %+v em %+v", row, got)
		}
	}
	if got.Note == "" {
		t.Fatal("nota ausente")
	}
	if _, err := svc.MetaUsage(ctx, s.agent, "2026-11-01", "2026-11-05"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("atendente deveria ser negado: %v", err)
	}
	for _, bad := range [][2]string{{"2026-11-05", "2026-11-01"}, {"x", "2026-11-01"}, {"2025-01-01", "2026-11-05"}} {
		if _, err := svc.MetaUsage(ctx, s.admin, bad[0], bad[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("período %v deveria ser inválido: %v", bad, err)
		}
	}
	empty, err := svc.MetaUsage(ctx, other.admin, "2026-12-01", "2026-12-05")
	if err != nil || len(empty.Rows) != 0 || empty.WithoutPricing != 0 {
		t.Fatalf("vazio: %+v %v", empty, err)
	}
}
