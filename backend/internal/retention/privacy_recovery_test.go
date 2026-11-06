package retention

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/crypto"
)

func TestPrivacyGuardAndPendingItemSanitization(t *testing.T) {
	ctx := context.Background()
	a, b := seedTenant(t), seedTenant(t)
	keys := privacyTestKeys(t)
	var connector uuid.UUID
	var identity string
	if err := testStore.WithTenantTx(ctx, a.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT connector_id,external_id FROM contact_channel_identities WHERE tenant_id=$1 AND contact_id=$2 ORDER BY id LIMIT 1`, a.tenant, a.contact).Scan(&connector, &identity)
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	delivery, matching, other := uuid.New(), uuid.New(), uuid.New()
	hash := sha256.Sum256([]byte(delivery.String()))
	if _, err := admin.Exec(ctx, `INSERT INTO ingress_deliveries(id,source,app_id,body_sha256,body,state) VALUES($1,'meta_cloud',$2,$3,$4,'routed')`, delivery, delivery.String(), hash[:], []byte("{}")); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   uuid.UUID
		from string
	}{{matching, identity}, {other, "other-identity"}} {
		payload, _ := json.Marshal(map[string]string{"from": item.from, "text": "private text", "profile_name": "private name"})
		if _, err := admin.Exec(ctx, `INSERT INTO webhook_items(tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload) VALUES($1,$2,$3,$4,$5,'phone',$6,'message','pending',$7)`, a.tenant, item.id, delivery, a.inbox, connector, item.id.String(), payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (Service{DB: testStore, Keys: keys}).DeletePrivacy(ctx, a.admin, a.contact); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, a.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var guarded bool
		if err := tx.QueryRow(ctx, `SELECT ingress_guarded FROM retention_deletion_records WHERE tenant_id=$1 AND class='content' AND target_type='contact' AND target_id=$2`, a.tenant, a.contact).Scan(&guarded); err != nil {
			return err
		}
		if !guarded {
			t.Fatal("exclusão nova sem proveniência da proteção")
		}
		var legacyCutoff *time.Time
		if err := tx.QueryRow(ctx, `SELECT privacy_legacy_cutoff FROM contacts WHERE tenant_id=$1 AND id=$2`, a.tenant, a.contact).Scan(&legacyCutoff); err != nil {
			return err
		}
		if legacyCutoff != nil {
			t.Fatalf("exclusão nova recebeu corte legado: %v", legacyCutoff)
		}
		var state string
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT state,payload FROM webhook_items WHERE tenant_id=$1 AND id=$2`, a.tenant, matching).Scan(&state, &payload); err != nil {
			return err
		}
		if state != "quarantined" || string(payload) != "{}" {
			t.Fatalf("item permaneceu: %s %s", state, payload)
		}
		if err := tx.QueryRow(ctx, `SELECT state,payload FROM webhook_items WHERE tenant_id=$1 AND id=$2`, a.tenant, other).Scan(&state, &payload); err != nil {
			return err
		}
		if state != "pending" || !strings.Contains(string(payload), "other-identity") {
			t.Fatalf("outro item alterado: %s %s", state, payload)
		}
		mac, err := keys.Fingerprint(keys.Current(), []byte(identity), crypto.Context{TenantID: a.tenant.String(), Entity: "privacy_ingress_guard", EntityID: connector.String(), Field: "external_id"})
		if err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM privacy_ingress_guards WHERE tenant_id=$1 AND connector_id=$2 AND key_version=$3 AND identity_mac=$4`, a.tenant, connector, keys.Current(), mac).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("marcador esperado: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, b.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM privacy_ingress_guards`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("marcador visível para outro tenant")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyDeletionPersistsObjectMappingAndRedactsReplay(t *testing.T) {
	ctx := context.Background()
	a, b := seedTenant(t), seedTenant(t)
	seedPreview(t, a, "secret original", 40)
	seedPreview(t, b, "other tenant body", 40)
	key := uuid.NewString() + "/" + uuid.NewString()
	var attachment, conv uuid.UUID
	if err := testStore.WithTenantTx(ctx, a.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id,conversation_id FROM attachments WHERE tenant_id=$1`, a.tenant).Scan(&attachment, &conv); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE attachments SET object_key=$2,object_version='etag:synthetic' WHERE tenant_id=$1`, a.tenant, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE messages SET content_context='{"location":{"name":"private address","latitude":1},"contact":{"phone":"private number"}}'::jsonb WHERE tenant_id=$1`, a.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO realtime_tenant_cursors(tenant_id,seq) VALUES($1,7)`, a.tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO realtime_events(tenant_id,seq,id,event_type,aggregate_type,aggregate_id,version,payload) VALUES($1,7,$2,'conversation.message','conversation',$3,1,'{"content":{"name":"private address"},"reply_to_text":"private quotation"}')`, a.tenant, uuid.New(), conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// No periodic map was exported between creation and erasure.
	svc := Service{DB: testStore, Objects: &memObjects{}, Keys: privacyTestKeys(t)}
	if _, err := svc.DeletePrivacy(ctx, a.admin, a.contact); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, a.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var storedKey, version string
		var bytes int64
		if err := tx.QueryRow(ctx, `SELECT object_key,object_version,object_bytes FROM deletion_records WHERE tenant_id=$1 AND subject='attachment' AND subject_id=$2`, a.tenant, attachment).Scan(&storedKey, &version, &bytes); err != nil {
			return err
		}
		if storedKey != key || version != "etag:synthetic" || bytes != 1 {
			t.Fatalf("lost object association %s %s %d", storedKey, version, bytes)
		}
		var body, contextJSON string
		if err := tx.QueryRow(ctx, `SELECT body,content_context::text FROM messages WHERE tenant_id=$1`, a.tenant).Scan(&body, &contextJSON); err != nil {
			return err
		}
		if body != "" || contextJSON != "{}" {
			t.Fatalf("message content survived %s %s", body, contextJSON)
		}
		var payload []byte
		var seq, cursor int64
		if err := tx.QueryRow(ctx, `SELECT r.seq,r.payload,c.seq FROM realtime_events r JOIN realtime_tenant_cursors c USING(tenant_id) WHERE r.tenant_id=$1 AND r.seq=7`, a.tenant).Scan(&seq, &payload, &cursor); err != nil {
			return err
		}
		var fields map[string]any
		if json.Unmarshal(payload, &fields) != nil || strings.Contains(string(payload), "private") || fields["privacy_erased"] != true || seq != 7 || cursor != 7 {
			t.Fatalf("unsafe replay seq=%d cursor=%d payload=%s", seq, cursor, payload)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, b.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		var body string
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM deletion_records WHERE subject_id=$1`, attachment).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("object mapping leaked across tenant")
		}
		if err := tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1`, b.tenant).Scan(&body); err != nil {
			return err
		}
		if body != "other tenant body" {
			t.Fatal("other tenant changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
