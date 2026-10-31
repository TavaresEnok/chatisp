package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestQueueSearchPermissionsAndPersonalRead(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	c := mustReceive(t, svc, w, "queue-search", "Olá pagamentos financeiros", at(1)).Conversation
	u, m := seedMember(t, w.tenant, "agent")
	other := Principal{TenantID: w.tenant, UserID: u, MembershipID: m, Role: "agent"}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO sector_memberships SELECT tenant_id,sector_id,$3 FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2 ON CONFLICT DO NOTHING`, w.tenant, w.membership, m)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	q := store.QueueInput{Search: "pagamento"}
	page, err := svc.Queue(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != c.ID || page.Items[0].UnreadCount != 1 {
		t.Fatalf("search: %+v %v", page, err)
	}
	accent, err := svc.Queue(ctx, p, store.QueueInput{Search: "ola"})
	if err != nil || accent.Counts.All != 1 {
		t.Fatalf("accent: %+v %v", accent, err)
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, page.EventNextCursor, false, 0); err != nil {
		t.Fatal(err)
	}
	a, err := svc.Queue(ctx, p, q)
	if err != nil || len(a.Items) != 1 || a.Items[0].Unread {
		t.Fatalf("read: %+v %v", a, err)
	}
	b, err := svc.Queue(ctx, other, q)
	if err != nil || len(b.Items) != 1 || !b.Items[0].Unread {
		t.Fatalf("other: %+v %v", b, err)
	}
	adminUser, adminMember := seedMember(t, w.tenant, "supervisor")
	supervisor := Principal{TenantID: w.tenant, UserID: adminUser, MembershipID: adminMember, Role: "supervisor"}
	tag, err := svc.CreateAnnotationTag(ctx, supervisor, "Fila")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AnnotationChange(ctx, p, w.inbox, c.ID, "tag_add", tag.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AnnotationChange(ctx, supervisor, w.inbox, c.ID, "priority", uuid.Nil, "urgent", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AnnotationChange(ctx, p, w.inbox, c.ID, "note", uuid.Nil, "segredointerno", "queue-note"); err != nil {
		t.Fatal(err)
	}
	filtered, err := svc.Queue(ctx, p, store.QueueInput{InboxID: w.inbox, TagID: tag.ID, Status: "open", Tab: "unassigned", Order: "lastMessage"})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Priority != "urgent" || filtered.Counts.All != 1 {
		t.Fatalf("filters: %+v %v", filtered, err)
	}
	private, err := svc.Queue(ctx, p, store.QueueInput{Search: "segredointerno"})
	if err != nil || private.Counts.All != 0 {
		t.Fatalf("private search: %+v %v", private, err)
	}
	mine, err := svc.Queue(ctx, p, store.QueueInput{Tab: "mine"})
	if err != nil || len(mine.Items) != 0 || mine.Counts.Unassigned != 1 {
		t.Fatalf("tabs: %+v %v", mine, err)
	}
	if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, w.tenant, m)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	b, err = svc.Queue(ctx, other, q)
	if err != nil || b.Counts.All != 0 || len(b.Items) != 0 {
		t.Fatalf("sector: %+v %v", b, err)
	}
	_, err = svc.Queue(ctx, p, store.QueueInput{Cursor: "invalid"})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("cursor: %v", err)
	}
	_, err = svc.Queue(ctx, p, store.QueueInput{SectorID: uuid.New(), Search: "financeiro"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueBlocksSectorTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	conv := mustReceive(t, svc, w, "queue-lock", "mensagem", at(1)).Conversation
	target := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Destino')`, w.tenant, target); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, w.tenant, target, w.inbox)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	queueDone := make(chan error, 1)
	go func() {
		queueDone <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
				return err
			}
			page, err := store.Queue(ctx, tx, store.QueueInput{TenantID: w.tenant, UserID: w.user, MembershipID: w.membership, Role: "agent"})
			if err != nil {
				return err
			}
			if page.Counts.All != 1 {
				return errors.New("fila não encontrou conversa")
			}
			close(held)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-held:
	case err := <-queueDone:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	transferDone := make(chan error, 1)
	go func() {
		_, err := svc.TransferToSector(ctx, p, AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: target, ExpectedVersion: conv.Version, ClientRequestID: "queue-transfer", At: at(2)})
		transferDone <- err
	}()
	select {
	case err := <-transferDone:
		t.Fatalf("transferência não aguardou: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-queueDone; err != nil {
		t.Fatal(err)
	}
	if err := <-transferDone; err != nil {
		t.Fatal(err)
	}
	page, err := svc.Queue(ctx, p, store.QueueInput{})
	if err != nil || page.Counts.All != 0 || len(page.Items) != 0 {
		t.Fatalf("transferência anterior à fila: %+v %v", page, err)
	}
}

func TestQueueBoundaryEventReplay(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	conv := mustReceive(t, svc, w, "boundary-first", "primeira", at(1)).Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO conversations(tenant_id,id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,sector_id,created_at) SELECT $1,gen_random_uuid(),$2,$3,$4,'closed','BOUNDARY','202610',2,sector_id,now() FROM inbox_sector_defaults WHERE tenant_id=$1 AND inbox_id=$2`, w.tenant, w.inbox, w.contact, w.identity)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	in := store.QueueInput{Limit: 1, Order: "lastMessage"}
	first, err := svc.Queue(ctx, p, in)
	if err != nil || first.PageCursor == "" || first.EventNextCursor <= 0 {
		t.Fatalf("first: %+v %v", first, err)
	}
	mustReceive(t, svc, w, "boundary-second", "segunda", at(5))
	in.Cursor = first.PageCursor
	if _, err = svc.Queue(ctx, p, in); !errors.Is(err, store.ErrResync) {
		t.Fatalf("continuation: %v", err)
	}
	var events store.EventPage
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		events, e = (store.Repo{}).ReadEvents(ctx, tx, w.tenant, first.EventNextCursor)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Events {
		if event.AggregateID == conv.ID && event.Seq > first.EventNextCursor {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing subsequent event: %+v", events)
	}
}
