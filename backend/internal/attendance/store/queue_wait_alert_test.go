package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestQueueWaitAlertFollowsStoredLimit(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	late, fresh, urgent, assigned := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO distribution_settings(tenant_id,wait_alert_seconds,version) VALUES($1,120,1)`, w.tenant); e != nil {
			return e
		}
		for _, row := range []struct {
			id       uuid.UUID
			protocol string
		}{{late, "W-LATE"}, {fresh, "W-FRESH"}, {urgent, "W-URGENT"}, {assigned, "W-MINE"}} {
			if e := insertOpenConversation(ctx, tx, w, row.id, row.protocol); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at=now()-make_interval(secs => 120) WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, late); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at=now()-make_interval(secs => 119) WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, fresh); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at=now()-interval '1 day' WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, urgent); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO conversation_annotations(tenant_id,conversation_id,priority) VALUES($1,$2,'urgent')`, w.tenant, urgent); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3, controller='human', created_at=now()-interval '2 days' WHERE tenant_id=$1 AND id=$2`, w.tenant, assigned, w.membership); e != nil {
			return e
		}
		page, e := Queue(ctx, tx, QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent"})
		if e != nil {
			return e
		}
		got := map[uuid.UUID]QueueItem{}
		for _, item := range page.Items {
			got[item.ID] = item
		}
		if len(got) != 4 || !got[late].WaitAlert || got[fresh].WaitAlert || got[urgent].WaitAlert || got[assigned].WaitAlert {
			t.Fatalf("alerts: late=%v fresh=%v urgent=%v assigned=%v", got[late].WaitAlert, got[fresh].WaitAlert, got[urgent].WaitAlert, got[assigned].WaitAlert)
		}
		if got[late].Protocol != "W-LATE" || got[late].Priority != "normal" || got[late].AssigneeID != nil || got[urgent].Priority != "urgent" || got[assigned].AssigneeID == nil || *got[assigned].AssigneeID != w.membership {
			t.Fatalf("other fields changed: %+v %+v %+v", got[late], got[urgent], got[assigned])
		}
		var assignee uuid.UUID
		if e = tx.QueryRow(ctx, `SELECT assignee_id FROM conversations WHERE tenant_id=$1 AND id=$2`, w.tenant, assigned).Scan(&assignee); e != nil {
			return e
		}
		if assignee != w.membership {
			t.Fatalf("queue reassigned %s", assignee)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueWaitAlertUsesDistributionDefaultWhenUnset(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	due, early := uuid.New(), uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if e := insertOpenConversation(ctx, tx, w, due, "W-900"); e != nil {
			return e
		}
		if e := insertOpenConversation(ctx, tx, w, early, "W-899"); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at=now()-make_interval(secs => 900) WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, due); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at=now()-make_interval(secs => 899) WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, early); e != nil {
			return e
		}
		var settings int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM distribution_settings WHERE tenant_id=$1`, w.tenant).Scan(&settings); e != nil {
			return e
		}
		page, e := Queue(ctx, tx, QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent"})
		if e != nil {
			return e
		}
		got := map[uuid.UUID]bool{}
		for _, item := range page.Items {
			got[item.ID] = item.WaitAlert
		}
		if settings != 0 || !got[due] || got[early] {
			t.Fatalf("settings=%d due=%v early=%v", settings, got[due], got[early])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertOpenConversation(ctx context.Context, tx pgx.Tx, w world, id uuid.UUID, protocol string) error {
	identity := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity, w.tenant, w.contact, w.connector, "wa-"+identity.String()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id) SELECT $1,$2,$3,$4,$5,'open',$6,'202611',1,sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$3`, w.tenant, id, w.inbox, w.contact, identity, protocol)
	return err
}
