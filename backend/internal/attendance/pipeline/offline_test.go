package pipeline

import (
	"context"
	"testing"
	"time"

	"chatisp/internal/attendance/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestUnassignOfflineOnlyAfterTolerance(t *testing.T) {
	ctx := context.Background()
	const phone = "5511977730001"
	st := seedTenantWithMeta(t, "app-offline-"+phone, "waba-offline-"+phone, phone, "tok-offline", "secret-offline")
	if err := NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, automationItem(t, st, "preciso de ajuda", phone)); err != nil {
		t.Fatal(err)
	}
	principal := service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}
	var conv uuid.UUID
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,version FROM conversations WHERE tenant_id=$1 AND inbox_id=$2`, st.tenantID, st.inboxID).Scan(&conv, &version)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := testAttendanceSvc.Assume(ctx, principal, st.inboxID, conv, version, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := func() (assignee *uuid.UUID, events int) {
		t.Helper()
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT assignee_id FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&assignee); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind LIKE 'offline_unassigned:%' AND actor_id IS NULL`, st.tenantID, conv).Scan(&events)
		}); err != nil {
			t.Fatal(err)
		}
		return assignee, events
	}
	offlineSince := func(d time.Duration) {
		t.Helper()
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO presence_operator(tenant_id,user_id,membership_id,explicit_offline_at) VALUES($1,$2,$3,$4) ON CONFLICT (tenant_id,user_id) DO UPDATE SET explicit_offline_at=EXCLUDED.explicit_offline_at`, st.tenantID, st.userID, st.memberID, now.Add(-d))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Without any presence record nothing is released.
	if n, err := UnassignOffline(ctx, testStore, st.tenantID, now); err != nil || n != 0 {
		t.Fatalf("sem presença: %d %v", n, err)
	}
	// A quick drop (inside the default 5 min tolerance) keeps the owner.
	offlineSince(time.Minute)
	if n, err := UnassignOffline(ctx, testStore, st.tenantID, now); err != nil || n != 0 {
		t.Fatalf("dentro da tolerância: %d %v", n, err)
	}
	if a, _ := state(); a == nil || *a != st.memberID {
		t.Fatalf("responsável perdido dentro da tolerância: %v", a)
	}
	offlineSince(10 * time.Minute)
	if n, err := UnassignOffline(ctx, testStore, st.tenantID, now); err != nil || n != 1 {
		t.Fatalf("após tolerância: %d %v", n, err)
	}
	if a, events := state(); a != nil || events != 1 {
		t.Fatalf("desatribuição: %v %d", a, events)
	}
	// Running again does not repeat the event.
	if n, err := UnassignOffline(ctx, testStore, st.tenantID, now); err != nil || n != 0 {
		t.Fatalf("repetição: %d %v", n, err)
	}
	var queued int
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM assignment_queue_intervals WHERE tenant_id=$1 AND conversation_id=$2 AND ended_at IS NULL`, st.tenantID, conv).Scan(&queued)
	}); err != nil || queued != 1 {
		t.Fatalf("fila: %d %v", queued, err)
	}
}
