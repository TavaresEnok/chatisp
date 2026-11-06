package retention

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func seedPreview(t *testing.T, s seeded, body string, days int) {
	t.Helper()
	ctx := context.Background()
	conv := uuid.New()
	opened := time.Now().AddDate(0, 0, -days)
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,sector_id,created_at,updated_at) SELECT $1,$2,$3,c.id,i.id,'closed','human',$6,($1::uuid)::text,'202611',1,d.sector_id,$4,$4 FROM contacts c JOIN contact_channel_identities i ON i.tenant_id=c.tenant_id AND i.contact_id=c.id JOIN inbox_sector_defaults d ON d.tenant_id=c.tenant_id AND d.inbox_id=$3 WHERE c.tenant_id=$2 AND c.id=$5 ORDER BY i.id LIMIT 1`, conv, s.tenant, s.inbox, opened, s.contact, s.agent.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,ingested_at) SELECT $1,$2,$3,connector_id,'inbound','customer','public',$4,'received',$5,$5 FROM inboxes WHERE tenant_id=$2 AND id=$6`, uuid.New(), s.tenant, conv, body, opened, s.inbox); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO attachments(tenant_id,id,conversation_id,request_id,direction,name,expected_bytes,object_key,state,created_at,expires_at) VALUES($1,$2,$3,$4,'incoming','test.txt',1,($2::uuid)::text,'validated',$5,$5)`, s.tenant, uuid.New(), conv, uuid.New(), opened)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func TestPurgePreviewIsBoundedReadOnlyAndTenantScoped(t *testing.T) {
	ctx := context.Background()
	a, b := seedTenant(t), seedTenant(t)
	svc := Service{DB: testStore}
	seedPreview(t, a, "old", 40)
	seedPreview(t, a, "old2", 40)
	seedPreview(t, a, "recent", 1)
	seedPreview(t, b, "another tenant", 40)
	days := 30
	if _, err := svc.SavePolicy(ctx, a.admin, Policy{ContentDays: &days, MediaDays: &days, LogDays: &days}); err != nil {
		t.Fatal(err)
	}
	before := counts(t, a.tenant)
	result, err := svc.PreviewPurge(ctx, a.admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.AutomaticPurge || result.ExecutionAvailable || len(result.Classes) != 5 {
		t.Fatalf("unsafe preview %+v", result)
	}
	for _, item := range result.Classes {
		if item.Class == "content" || item.Class == "media" {
			if item.Candidates == nil || *item.Candidates != 1 || !item.Capped || item.Cutoff == nil {
				t.Fatalf("bounded class %+v", item)
			}
		} else if item.Candidates != nil {
			t.Fatalf("invented census %+v", item)
		}
		if item.Blocker == "" {
			t.Fatalf("missing implementation blocker %+v", item)
		}
	}
	if after := counts(t, a.tenant); before != after {
		t.Fatalf("preview changed records %v -> %v", before, after)
	}
	full, err := svc.PreviewPurge(ctx, a.admin, 100)
	if err != nil || full.Classes[0].Candidates == nil || *full.Classes[0].Candidates != 2 || full.Classes[0].Capped {
		t.Fatalf("tenant census %+v err=%v", full, err)
	}
	if _, err := svc.PreviewPurge(ctx, a.agent, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent allowed: %v", err)
	}
	crossed := a.admin
	crossed.TenantID = b.tenant
	if _, err := svc.PreviewPurge(ctx, crossed, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross tenant allowed: %v", err)
	}
	if _, err := svc.PreviewPurge(ctx, a.admin, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded preview: %v", err)
	}
}
