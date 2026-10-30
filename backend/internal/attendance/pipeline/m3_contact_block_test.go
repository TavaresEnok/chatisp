package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestBlockedContactQuarantinesInboundAndStopsPendingDispatch(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-block-m3", "waba-block-m3", "5511977771021", "token-block", "secret-block")
	attempt, _ := createTestAttempt(t, st, "pendente", "block-m3-request")
	var conversation uuid.UUID
	var contact uuid.UUID
	var sender string
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT c.id, c.contact_id, ci.external_id FROM outbound_attempts o JOIN conversations c ON c.tenant_id=o.tenant_id AND c.id=o.conversation_id JOIN contact_channel_identities ci ON ci.tenant_id=c.tenant_id AND ci.id=c.contact_identity_id WHERE o.tenant_id=$1 AND o.id=$2`, st.tenantID, attempt).Scan(&conversation, &contact, &sender); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE contacts SET blocked=true, block_reason='abuso' WHERE tenant_id=$1 AND id=$2`, st.tenantID, contact)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	driver := &recordingDriver{}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attempt); err != nil {
		t.Fatal(err)
	}
	if driver.CallCount() != 0 {
		t.Fatal("contato bloqueado recebeu despacho")
	}
	var delivery, item uuid.UUID
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		d, err := (store.Repo{}).StoreDelivery(ctx, tx, st.appID, []byte(`{"m3":true}`), "", "")
		if err != nil {
			return err
		}
		delivery = d.ID
		payload, err := json.Marshal(MessageItemPayload{From: sender, Text: "bloqueado", ExternalID: "m3-block-inbound", DedupKey: "message:m3-block-inbound", OriginAt: time.Now()})
		if err != nil {
			return err
		}
		v, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{TenantID: st.tenantID, DeliveryID: delivery, InboxID: st.inboxID, ConnectorID: st.connectorID, PhoneNumberID: st.phoneID, ExternalKey: "message:m3-block-inbound", Kind: "message", Payload: payload})
		item = v.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewInboundHandler(testStore, testAttendanceSvc).ApplyInbound(ctx, st.tenantID, item); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var state, attemptState string
		var count int
		if err := tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE id=$1`, item).Scan(&state); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT state FROM outbound_attempts WHERE id=$1`, attempt).Scan(&attemptState); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND external_message_id='m3-block-inbound'`, conversation).Scan(&count); err != nil {
			return err
		}
		if state != "quarantined" || attemptState != "failed" || count != 0 {
			t.Fatalf("item=%s tentativa=%s mensagens=%d", state, attemptState, count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
