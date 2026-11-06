package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/privacyguard"
	"chatisp/internal/retention"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyBlocksOldExpandedAndLateItemsButAllowsNewIngress(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-privacy-replay", "waba-privacy-replay", "5511977714931", "token-privacy-replay", "secret-privacy-replay")
	const sender = "5511987654321"
	h := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	h.keys = testRing
	first := automationItem(t, st, "old", sender)
	if err := h.ApplyInbound(ctx, st.tenantID, first); err != nil {
		t.Fatal(err)
	}
	pending := automationItem(t, st, "pending", sender)
	var contact uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT contact_id FROM contact_channel_identities WHERE tenant_id=$1 AND connector_id=$2 AND external_id=$3`, st.tenantID, st.connectorID, sender).Scan(&contact)
	}); err != nil {
		t.Fatal(err)
	}
	delivery := uuid.New()
	raw, _ := json.Marshal(MessageItemPayload{From: sender, Text: "late", ExternalID: uuid.NewString(), OriginAt: time.Now()})
	sum := sha256.Sum256(raw)
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ingress_deliveries(id,source,app_id,body_sha256,body,state) VALUES($1,'meta_cloud',$2,$3,$4,'stored')`, delivery, st.appID, sum[:], raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc := retention.Service{DB: testStore, Keys: testRing}
	if _, err := svc.DeletePrivacy(ctx, retention.Principal{TenantID: st.tenantID, UserID: st.userID, MembershipID: st.memberID, Role: "admin"}, contact); err != nil {
		t.Fatal(err)
	}
	var late uuid.UUID
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		r, err := (store.Repo{}).PutWebhookItem(ctx, tx, store.WebhookInput{TenantID: st.tenantID, DeliveryID: delivery, InboxID: st.inboxID, ConnectorID: st.connectorID, PhoneNumberID: st.phoneID, ExternalKey: uuid.NewString(), Kind: "message", Payload: raw})
		late = r.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{pending, late} {
		if err := h.ApplyInbound(ctx, st.tenantID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1 AND id=ANY($2) AND state='quarantined' AND payload='{}'::jsonb`, st.tenantID, []uuid.UUID{pending, late}).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("guarded items=%d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A duplicate delivery received after erasure is still the old message.
	// It must not recreate the contact before message deduplication runs.
	duplicate := automationItem(t, st, "old redelivery", sender)
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE webhook_items SET payload=jsonb_set(payload,'{external_id}',to_jsonb((SELECT external_message_id FROM messages WHERE tenant_id=$1 AND connector_id=$2 AND body='' LIMIT 1))) WHERE tenant_id=$1 AND id=$3`, st.tenantID, st.connectorID, duplicate)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyInbound(ctx, st.tenantID, duplicate); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contact_channel_identities WHERE tenant_id=$1 AND connector_id=$2 AND external_id=$3`, st.tenantID, st.connectorID, sender).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("duplicate recreated identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	newer := automationItem(t, st, "new", sender)
	if err := h.ApplyInbound(ctx, st.tenantID, newer); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		return tx.QueryRow(ctx, `SELECT contact_id FROM contact_channel_identities WHERE tenant_id=$1 AND connector_id=$2 AND external_id=$3`, st.tenantID, st.connectorID, sender).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyLegacyQuarantinesOldBacklogOnlyInAffectedTenant(t *testing.T) {
	ctx := context.Background()
	a := seedTenantWithMeta(t, "app-legacy-a", "waba-legacy-a", "5511977714941", "token-legacy-a", "secret-legacy-a")
	b := seedTenantWithMeta(t, "app-legacy-b", "waba-legacy-b", "5511977714942", "token-legacy-b", "secret-legacy-b")
	oldA := automationItem(t, a, "mensagem anterior", "5511911112222")
	oldB := automationItem(t, b, "outro tenant", "5511911112222")
	reaction := automationItem(t, a, "", "5511911113333")
	if err := testStore.WithTenantTx(ctx, a.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE webhook_items SET payload=jsonb_set(payload,'{content}', '{"type":"reaction","reaction_target":"m1","emoji":"x"}'::jsonb) WHERE tenant_id=$1 AND id=$2`, a.tenantID, reaction); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contacts (id,tenant_id,name,privacy_erased_at,privacy_legacy_cutoff) VALUES ($1,$2,'titular excluído',clock_timestamp()-interval '1 hour',clock_timestamp())`, uuid.New(), a.tenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	newA := automationItem(t, a, "mensagem nova", "5511911114444")
	h := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	h.keys = testRing
	for _, item := range []struct{ tenant, id uuid.UUID }{{a.tenantID, oldA}, {a.tenantID, reaction}, {b.tenantID, oldB}, {a.tenantID, newA}} {
		if err := h.ApplyInbound(ctx, item.tenant, item.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := testStore.WithTenantTx(ctx, a.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1 AND id=ANY($2) AND state='quarantined' AND payload <> '{}'::jsonb`, a.tenantID, []uuid.UUID{oldA, reaction}).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("legacy quarantine with payload: %d", n)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1 AND id=$2 AND state='processed'`, a.tenantID, newA).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatal("new ingress was not processed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, b.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE tenant_id=$1 AND id=$2`, b.tenantID, oldB).Scan(&state); err != nil {
			return err
		}
		if state != "processed" {
			t.Fatalf("other tenant: %s", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyLegacyContradictoryRecordsFailClosed(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-legacy-mixed", "waba-legacy-mixed", "5511977714943", "token-legacy-mixed", "secret-legacy-mixed")
	contact := uuid.New()
	err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name,privacy_erased_at,privacy_legacy_cutoff) VALUES($1,$2,'excluído',clock_timestamp()-interval '1 hour',clock_timestamp())`, contact, st.tenantID); err != nil {
			return err
		}
		for _, guarded := range []bool{true, false} {
			if _, err := tx.Exec(ctx, `INSERT INTO retention_deletion_records(tenant_id,id,class,target_type,target_id,note,ingress_guarded) VALUES($1,$2,'content','contact',$3,'teste',$4)`, st.tenantID, uuid.New(), contact, guarded); err != nil {
				return err
			}
		}
		blocked, err := privacyguard.LegacyBlocked(ctx, tx, st.tenantID, time.Now().Add(-time.Minute))
		if err != nil {
			return err
		}
		if !blocked {
			t.Fatal("mixed history must fail closed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
