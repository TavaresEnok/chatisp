package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestQueueCutBeforeLimitAndContinuation(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	hidden := uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'privado')`, w.tenant, hidden); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, w.tenant, hidden, w.inbox); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id,created_at) SELECT $1,gen_random_uuid(),$2,$3,$4,'closed','P-'||n,'202610',1,CASE WHEN n<=501 THEN $5 ELSE (SELECT sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2) END,now()+n*interval '1 second' FROM generate_series(1,503) n`, w.tenant, w.inbox, w.contact, w.identity, hidden); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body) SELECT tenant_id,gen_random_uuid(),id,$2,'inbound','customer','public','palavra pesquisavel' FROM conversations WHERE tenant_id=$1 AND protocol IN ('P-502','P-503')`, w.tenant, w.connector)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	// EXPLAIN the actual query with synthetic data and no message bodies in logs.
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var ids []uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT array_agg(id) FROM conversations WHERE tenant_id=$1 AND protocol IN ('P-502','P-503')`, w.tenant).Scan(&ids); e != nil {
			return e
		}
		var plan []byte
		if e := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+queueSQL, w.tenant, w.user, w.membership, "agent", "all", "wait", "", nil, nil, nil, nil, nil, nil, "pesquisavel", 1, nil, nil, ids).Scan(&plan); e != nil {
			return e
		}
		var tree []struct {
			Plan map[string]any `json:"Plan"`
		}
		if e := json.Unmarshal(plan, &tree); e != nil {
			return e
		}
		nodes := map[string]bool{}
		indexes := map[string]bool{}
		var walk func(map[string]any)
		walk = func(p map[string]any) {
			if n, ok := p["Node Type"].(string); ok {
				nodes[n] = true
			}
			if n, ok := p["Index Name"].(string); ok {
				indexes[n] = true
			}
			if children, ok := p["Plans"].([]any); ok {
				for _, child := range children {
					if c, ok := child.(map[string]any); ok {
						walk(c)
					}
				}
			}
		}
		walk(tree[0].Plan)
		t.Logf("queue EXPLAIN ANALYZE (503 conversations, 2 authorized, public fulltext): nodes=%v indexes=%v", nodes, indexes)
		p, e := Queue(ctx, tx, QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent", Search: "pesquisavel", Limit: 1})
		if e != nil {
			return e
		}
		if p.Counts.All != 2 || len(p.Items) != 1 {
			t.Fatalf("fulltext explain fixture: %+v", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	in := QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent", Limit: 1}
	run := func(q QueueInput) (QueuePage, error) {
		var p QueuePage
		e := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error { var x error; p, x = Queue(ctx, tx, q); return x })
		return p, e
	}
	first, err := run(in)
	if err != nil || first.Counts.All != 2 || len(first.Items) != 1 || first.PageCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	in.Cursor = first.PageCursor
	second, err := run(in)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.EventNextCursor != first.EventNextCursor {
		t.Fatalf("second: %+v %v", second, err)
	}
	in.Search = "P-502"
	if _, err = run(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scope: %v", err)
	}
	in.Search = ""
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE contacts SET name='alterado' WHERE tenant_id=$1 AND id=$2`, w.tenant, w.contact)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run(in); !errors.Is(err, ErrResync) {
		t.Fatalf("changed result: %v", err)
	}
}

func TestQueueHistoricalImportDoesNotMoveLastMessagePage(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	firstID, secondID := uuid.New(), uuid.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id,created_at) SELECT $1,x.id,$2,$3,$4,'closed',x.protocol,'202601',1,d.sector_id,$7 FROM (VALUES ($5::uuid,'H-1'),($6::uuid,'H-2')) x(id,protocol) CROSS JOIN inbox_sector_defaults d WHERE d.tenant_id=$1 AND d.inbox_id=$2`, w.tenant, w.inbox, w.contact, w.identity, firstID, secondID, base); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,ingested_at) VALUES ($1,gen_random_uuid(),$2,$4,'inbound','customer','public',$5),($1,gen_random_uuid(),$3,$4,'inbound','customer','public',$6)`, w.tenant, firstID, secondID, w.connector, base.Add(2*time.Hour), base.Add(time.Hour))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	in := QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent", Order: "lastMessage", Limit: 1}
	run := func() (p QueuePage, e error) {
		e = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error { var x error; p, x = Queue(ctx, tx, in); return x })
		return
	}
	p, err := run()
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != firstID || !p.Items[0].LastMessageAt.Equal(base.Add(2*time.Hour)) || p.PageCursor == "" {
		t.Fatalf("first page: %+v %v", p, err)
	}
	in.Cursor = p.PageCursor
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,ingested_at,historical) VALUES($1,gen_random_uuid(),$2,$3,'inbound','customer','public',$4,true)`, w.tenant, secondID, w.connector, base.Add(3*time.Hour))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err = run()
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != secondID || !p.Items[0].LastMessageAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("continuation after import: %+v %v", p, err)
	}
}

func TestQueuePinsAuthorizationRows(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	conv, target := uuid.New(), uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'privado');`, w.tenant, target)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, w.tenant, target, w.inbox)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id) SELECT $1,$2,$3,$4,$5,'closed','LOCK','202610',1,sector_id FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$3`, w.tenant, conv, w.inbox, w.contact, w.identity)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	in := QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent"}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		p, e := Queue(ctx, tx, in)
		if e != nil || p.Counts.All != 1 {
			t.Fatalf("queue: %+v %v", p, e)
		}
		for _, stmt := range []string{
			`UPDATE conversations SET sector_id=$3 WHERE tenant_id=$1 AND id=$2`,
			`DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=(SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2) AND $3::uuid IS NOT NULL`,
			`UPDATE sectors SET active=false WHERE tenant_id=$1 AND id=(SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2) AND $3::uuid IS NOT NULL`,
			`DELETE FROM sector_memberships WHERE tenant_id=$1 AND sector_id=(SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2) AND $3::uuid IS NOT NULL`,
		} {
			e = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, other pgx.Tx) error {
				if _, x := other.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); x != nil {
					return x
				}
				_, x := other.Exec(ctx, stmt, w.tenant, conv, target)
				return x
			})
			if pgCode(e) != "55P03" {
				t.Fatalf("expected lock timeout for %s: %v", stmt, e)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE conversations SET sector_id=$3 WHERE tenant_id=$1 AND id=$2`, w.tenant, conv, target)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		p, e := Queue(ctx, tx, in)
		if e == nil && (p.Counts.All != 0 || len(p.Items) != 0) {
			t.Fatalf("moved conversation visible: %+v", p)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}
