package pipeline

import (
	"context"
	"testing"
	"time"

	"chatisp/internal/attendance/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// closeFirstConversation receives one message through the real handler, then
// closes that attendance through the service and returns its id and protocol.
func closeFirstConversation(t *testing.T, st seededTenant, phone string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	handler := NewInboundHandler(testStore, testAttendanceSvc)
	if err := handler.ApplyInbound(ctx, st.tenantID, automationItem(t, st, "primeiro contato", phone)); err != nil {
		t.Fatal(err)
	}
	principal := service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}
	var conv uuid.UUID
	var version int64
	var protocol string
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,version,protocol FROM conversations WHERE tenant_id=$1 AND inbox_id=$2`, st.tenantID, st.inboxID).Scan(&conv, &version, &protocol)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := testAttendanceSvc.Close(ctx, principal, service.CloseInput{
		InboxID: st.inboxID, ConversationID: conv, ExpectedVersion: version, ClientRequestID: "close-" + phone,
		Reason: "atendido", Result: "resolved", Summary: "resolvido", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return conv, protocol
}

func setReopenWindow(t *testing.T, st seededTenant, hours int) {
	t.Helper()
	if err := testStore.WithTenantTx(context.Background(), st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE inboxes SET reopen_window_hours=$3 WHERE tenant_id=$1 AND id=$2`, st.tenantID, st.inboxID, hours)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type reopenSnapshot struct {
	conversations int
	status        string
	cycle         int32
	messages      int
	reopenedEvent int
}

func readReopen(t *testing.T, st seededTenant, conv uuid.UUID) reopenSnapshot {
	t.Helper()
	var s reopenSnapshot
	if err := testStore.WithTenantTx(context.Background(), st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE tenant_id=$1 AND inbox_id=$2`, st.tenantID, st.inboxID).Scan(&s.conversations); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT status,cycle_number FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&s.status, &s.cycle); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='inbound'`, st.tenantID, conv).Scan(&s.messages); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='reopened'`, st.tenantID, conv).Scan(&s.reopenedEvent)
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInboundReopensWithinInboxWindow(t *testing.T) {
	const phone = "5511977720001"
	st := seedTenantWithMeta(t, "app-reopen-"+phone, "waba-reopen-"+phone, phone, "tok-reopen", "secret-reopen")
	setReopenWindow(t, st, 24)
	conv, protocol := closeFirstConversation(t, st, phone)
	item := automationItem(t, st, "voltei", phone)
	handler := NewInboundHandler(testStore, testAttendanceSvc)
	if err := handler.ApplyInbound(context.Background(), st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	got := readReopen(t, st, conv)
	if got.conversations != 1 || got.status != "open" || got.cycle != 2 || got.messages != 2 || got.reopenedEvent != 1 {
		t.Fatalf("reopen %+v", got)
	}
	var after string
	if err := testStore.WithTenantTx(context.Background(), st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT protocol FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&after)
	}); err != nil || after != protocol {
		t.Fatalf("protocol %q -> %q %v", protocol, after, err)
	}
	// Replaying the same webhook item does not reopen or duplicate anything.
	if err := handler.ApplyInbound(context.Background(), st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	if again := readReopen(t, st, conv); again != got {
		t.Fatalf("replay changed state %+v -> %+v", got, again)
	}
}

func TestInboundOutsideWindowOpensNewProtocol(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		phone string
		hours int
		age   time.Duration
	}{
		{"disabled", "5511977720002", 0, 0},
		{"expired", "5511977720003", 1, 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := seedTenantWithMeta(t, "app-reopen-"+tc.phone, "waba-reopen-"+tc.phone, tc.phone, "tok-reopen", "secret-reopen")
			setReopenWindow(t, st, tc.hours)
			conv, protocol := closeFirstConversation(t, st, tc.phone)
			if tc.age > 0 {
				if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `UPDATE conversation_cycles SET closed_at=closed_at-$3::interval WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, conv, tc.age.String())
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, automationItem(t, st, "nova demanda", tc.phone)); err != nil {
				t.Fatal(err)
			}
			got := readReopen(t, st, conv)
			if got.conversations != 2 || got.status != "closed" || got.cycle != 1 || got.reopenedEvent != 0 {
				t.Fatalf("closed attendance changed %+v", got)
			}
			var other string
			if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT protocol FROM conversations WHERE tenant_id=$1 AND inbox_id=$2 AND id<>$3`, st.tenantID, st.inboxID, conv).Scan(&other)
			}); err != nil || other == protocol {
				t.Fatalf("new protocol %q (old %q) %v", other, protocol, err)
			}
		})
	}
}
