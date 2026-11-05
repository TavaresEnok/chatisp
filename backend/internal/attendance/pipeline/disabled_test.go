package pipeline

import (
	"context"
	"testing"
	"time"

	"chatisp/internal/attendance/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReleaseDisabledReturnsConversationsToTheQueue(t *testing.T) {
	ctx := context.Background()
	const phone = "5511977730002"
	st := seedTenantWithMeta(t, "app-disabled-"+phone, "waba-disabled-"+phone, phone, "tok-disabled", "secret-disabled")
	if err := NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, automationItem(t, st, "preciso de ajuda", phone)); err != nil {
		t.Fatal(err)
	}
	principal := service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}
	var conv uuid.UUID
	var version int64
	query := func(sql string, dest ...any) {
		t.Helper()
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, sql, st.tenantID, st.inboxID).Scan(dest...)
		}); err != nil {
			t.Fatal(err)
		}
	}
	query(`SELECT id,version FROM conversations WHERE tenant_id=$1 AND inbox_id=$2`, &conv, &version)
	if _, err := testAttendanceSvc.Assume(ctx, principal, st.inboxID, conv, version, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// An active attendant keeps the conversation.
	if n, err := ReleaseDisabled(ctx, testStore, st.tenantID, now); err != nil || n != 0 {
		t.Fatalf("matrícula ativa: %d %v", n, err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE memberships SET status='disabled' WHERE tenant_id=$1 AND id=$2`, st.tenantID, st.memberID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := ReleaseDisabled(ctx, testStore, st.tenantID, now); err != nil || n != 1 {
		t.Fatalf("matrícula desativada: %d %v", n, err)
	}
	var assignee *uuid.UUID
	var events, queued int
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT assignee_id FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&assignee); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind LIKE 'offline_unassigned:%'`, st.tenantID, conv).Scan(&events); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM assignment_queue_intervals WHERE tenant_id=$1 AND conversation_id=$2 AND ended_at IS NULL`, st.tenantID, conv).Scan(&queued)
	}); err != nil {
		t.Fatal(err)
	}
	if assignee != nil || events != 1 || queued != 1 {
		t.Fatalf("liberação: responsável=%v eventos=%d fila=%d", assignee, events, queued)
	}
	if n, err := ReleaseDisabled(ctx, testStore, st.tenantID, now); err != nil || n != 0 {
		t.Fatalf("repetição: %d %v", n, err)
	}
}
