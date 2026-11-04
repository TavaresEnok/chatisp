package pipeline

import (
	"context"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/lifecycle"
	"chatisp/internal/platform/db"
	"chatisp/internal/triage"
	"crypto/sha256"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAutomationWarningUsesOutboxAndConfirmedClock(t *testing.T) {
	ctx := context.Background()
	st, _, _, humanAttempt := seedPolicyAttempt(t, "5511977714401", "window")
	now := time.Now().UTC()
	var conv uuid.UUID
	err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT conversation_id FROM outbound_attempts WHERE tenant_id=$1 AND id=$2`, st.tenantID, humanAttempt).Scan(&conv); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET status='waiting_customer',last_eligible_human_reply_at=$3,waiting_since=$3,last_customer_message_at=$4 WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv, now.Add(-16*time.Minute), now.Add(-17*time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	a := Automation{DB: testStore, Jobs: testJobs, Now: func() time.Time { return now }}
	if err = a.RunTenant(ctx, st.tenantID, now); err != nil {
		t.Fatal(err)
	}
	var warningAttempt uuid.UUID
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT attempt_id FROM automation_dispatches WHERE tenant_id=$1 AND conversation_id=$2 AND kind='inactivity'`, st.tenantID, conv).Scan(&warningAttempt)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Replay tick neither duplicates nor closes a pending warning.
	if err = a.RunTenant(ctx, st.tenantID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	var status string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM automation_dispatches WHERE tenant_id=$1 AND kind='inactivity'`, st.tenantID).Scan(&count); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&status)
	})
	if err != nil || count != 1 || status != "waiting_customer" {
		t.Fatalf("pending: %d %s %v", count, status, err)
	}
	fake := whatsapp.NewFake()
	factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: fake})
	if err = NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, warningAttempt); err != nil {
		t.Fatal(err)
	}
	state, _ := readAttempt(t, ctx, st.tenantID, warningAttempt)
	if state != "accepted" {
		t.Fatalf("warning %s", state)
	}
	if err = a.RunTenant(ctx, st.tenantID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = a.RunTenant(ctx, st.tenantID, now.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&status)
	})
	if err != nil || status != "closed" {
		t.Fatalf("confirmed: %s %v", status, err)
	}
}

func TestAutomationWarningCustomerResponseCancelsDispatch(t *testing.T) {
	ctx := context.Background()
	st, _, _, human := seedPolicyAttempt(t, "5511977714402", "window")
	now := time.Now().UTC()
	var conv uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT conversation_id FROM outbound_attempts WHERE tenant_id=$1 AND id=$2`, st.tenantID, human).Scan(&conv); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET status='waiting_customer',last_eligible_human_reply_at=$3,last_customer_message_at=$4 WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv, now.Add(-16*time.Minute), now.Add(-17*time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a := Automation{DB: testStore, Jobs: testJobs}
	if err := a.RunTenant(ctx, st.tenantID, now); err != nil {
		t.Fatal(err)
	}
	var attempt uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT attempt_id FROM automation_dispatches WHERE tenant_id=$1 AND kind='inactivity'`, st.tenantID).Scan(&attempt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET last_customer_message_at=$3,status='open' WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	driver := &recordingDriver{}
	factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attempt); err != nil {
		t.Fatal(err)
	}
	if driver.CallCount() != 0 {
		t.Fatal("obsolete warning reached network")
	}
	if err := a.RunTenant(ctx, st.tenantID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// The pre-existing domain fixtures exercise every triage branch. This proof
// composes a saved decision, human routing, outbox and replay over real PG.
func TestAutomationTriageComposesAndReplays(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-triage-compose", "waba-triage-compose", "5511977714403", "token-triage-compose", "secret-triage-compose")
	var sector uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2`, st.tenantID, st.inboxID).Scan(&sector)
	}); err != nil {
		t.Fatal(err)
	}
	p := triage.Principal{TenantID: st.tenantID, MembershipID: st.memberID, UserID: st.userID, Role: "admin"}
	_, err := triage.New(testStore).Put(ctx, p, st.inboxID, triage.Command{RequestID: uuid.New(), MaxAttempts: 2, Options: []triage.Option{{Key: "1", Label: "Suporte", Destination: "sector", SectorID: &sector}, {Key: "0", Label: "Humano", Destination: "human"}}})
	if err != nil {
		t.Fatal(err)
	}
	a := &Automation{DB: testStore, Jobs: testJobs, TriageInboxes: map[uuid.UUID]bool{st.inboxID: true}}
	item := automationItem(t, st, "oi")
	if err = NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM triage_decisions WHERE tenant_id=$1`, st.tenantID).Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("flag off: %d %v", count, err)
	}
	composed := NewInboundHandler(testStore, testAttendanceSvc, a)
	if err = composed.ApplyInbound(ctx, st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	if err = composed.ApplyInbound(ctx, st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	var conv, attempt uuid.UUID
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM triage_decisions WHERE tenant_id=$1`, st.tenantID).Scan(&count); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT conversation_id,attempt_id FROM automation_dispatches WHERE tenant_id=$1 AND attempt_id IS NOT NULL`, st.tenantID).Scan(&conv, &attempt)
	}); err != nil || count != 1 {
		t.Fatalf("replay: %d %v", count, err)
	}
	fake := whatsapp.NewFake()
	factory, _ := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: fake})
	sender := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).(*outboundHandler)
	sender.triageInboxes = a.TriageInboxes
	if err = sender.DispatchOutbound(ctx, st.tenantID, attempt); err != nil {
		t.Fatal(err)
	}
	if state, _ := readAttempt(t, ctx, st.tenantID, attempt); state != "accepted" {
		t.Fatalf("menu %s", state)
	}
	item2 := automationItem(t, st, "1")
	if err = composed.ApplyInbound(ctx, st.tenantID, item2); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&status)
	}); err != nil || status != "open" {
		t.Fatalf("human queue: %s %v", status, err)
	}
}
func automationItem(t *testing.T, st seededTenant, text string, from ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	delivery := uuid.New()
	id := uuid.NewString()
	sender := "5511912341111"
	if len(from) > 0 {
		sender = from[0]
	}
	raw, _ := json.Marshal(MessageItemPayload{From: sender, Text: text, ExternalID: id, OriginAt: time.Now().UTC()})
	sum := sha256.Sum256(raw)
	var item uuid.UUID
	err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO ingress_deliveries(id,source,app_id,body_sha256,body,state) VALUES($1,'meta_cloud',$2,$3,$4,'stored')`, delivery, st.appID, sum[:], raw); err != nil {
			return err
		}
		result, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{TenantID: st.tenantID, DeliveryID: delivery, InboxID: st.inboxID, ConnectorID: st.connectorID, PhoneNumberID: st.phoneID, ExternalKey: id, Kind: "message", Payload: raw})
		item = result.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestAutomationInboundClearsSnoozeInMessageTransaction(t *testing.T) {
	ctx := context.Background()
	const phone = "5511977714404"
	st, p, _, attempt := seedPolicyAttempt(t, phone, "window")
	var conv uuid.UUID
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT c.id,c.version FROM outbound_attempts o JOIN conversations c ON c.tenant_id=o.tenant_id AND c.id=o.conversation_id WHERE o.tenant_id=$1 AND o.id=$2`, st.tenantID, attempt).Scan(&conv, &version)
	}); err != nil {
		t.Fatal(err)
	}
	principal := lifecycle.Principal{TenantID: p.TenantID, MembershipID: p.MembershipID, UserID: p.UserID, Role: p.Role}
	if _, err := lifecycle.New(testStore).Snooze(ctx, principal, conv, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: st.inboxID, ExpectedVersion: version, Until: time.Now().Add(time.Hour), Reason: "aguardar documento"}); err != nil {
		t.Fatal(err)
	}
	item := automationItem(t, st, "voltei", phone)
	if err := NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	var active int
	var state string
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active'`, st.tenantID, conv).Scan(&active); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&state)
	}); err != nil || active != 0 || state != "open" {
		t.Fatalf("snooze %d %s %v", active, state, err)
	}
}
