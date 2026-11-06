package retention

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

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
	svc := Service{DB: testStore, Objects: &memObjects{}}
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
