package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAssignmentReplayAndReturn(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	_, member := seedMember(t, w.tenant, "agent")
	conv := mustReceive(t, svc, w, "assignment-1", "olá", at(0)).Conversation
	in := AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: member, ExpectedVersion: conv.Version, ClientRequestID: "transfer-1", At: at(1)}
	got, err := svc.TransferToMember(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssigneeID != member || got.Version != conv.Version+1 {
		t.Fatalf("unexpected assignment: %+v", got)
	}
	replay, err := svc.TransferToMember(ctx, p, in)
	if err != nil || replay.Version != got.Version || replay.AssigneeID != member {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	changed := in
	changed.ExpectedVersion++
	if _, err = svc.TransferToMember(ctx, p, changed); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("conflict: %v", err)
	}
	returned, err := svc.Unassign(ctx, p, AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: got.Version, ClientRequestID: "return-1", At: at(2)})
	if err != nil {
		t.Fatal(err)
	}
	if returned.AssigneeID == member || returned.Controller != store.ControllerMenu {
		t.Fatalf("return: %+v", returned)
	}
	n := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM assignment_queue_intervals WHERE tenant_id=$1 AND conversation_id=$2 AND ended_at IS NULL`, w.tenant, conv.ID)
	if n != 1 {
		t.Fatalf("queue intervals: %d", n)
	}
}

func TestAssignmentHistoryIncludesActionAndDestinations(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	_, member := seedMember(t, w.tenant, "agent")
	conv := mustReceive(t, svc, w, "assignment-history", "hello", at(0)).Conversation
	var source uuid.UUID
	target := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2`, w.tenant, conv.ID).Scan(&source); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Destino')`, w.tenant, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, w.tenant, target, w.inbox); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3)`, w.tenant, target, w.membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	in := AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: member, ExpectedVersion: conv.Version, ClientRequestID: "member", At: at(1)}
	a, err := svc.TransferToMember(ctx, principal(w), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Unassign(ctx, principal(w), AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: a.Version, ClientRequestID: "return", At: at(2)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.TransferToSector(ctx, principal(w), AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: target, ExpectedVersion: b.Version, ClientRequestID: "sector", At: at(3)})
	if err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, principal(w), w.inbox, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		fmt.Sprintf("transferred_member:%s:%s:%s:%s", source, source, uuid.Nil, member):   false,
		fmt.Sprintf("unassigned:%s:%s:%s:%s", source, source, member, uuid.Nil):           false,
		fmt.Sprintf("transferred_sector:%s:%s:%s:%s", source, target, uuid.Nil, uuid.Nil): false,
	}
	for _, event := range history.Timeline {
		if _, ok := want[event.Kind]; ok {
			if event.ActorID != w.user {
				t.Fatalf("actor: %+v", event)
			}
			want[event.Kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("missing timeline event %s", kind)
		}
	}
}

func TestAssignmentCapacityHoldsTenantGateWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w := seedWorld(t)
	user, admin := seedMember(t, w.tenant, "admin")
	p := Principal{TenantID: w.tenant, UserID: user, MembershipID: admin, Role: "admin"}
	held, release := make(chan struct{}), make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:' || $1::text || ':' || $2::text, 0))`, w.tenant, w.membership); err != nil {
				return err
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
	<-held
	capacityDone := make(chan error, 1)
	go func() { capacityDone <- live(nil).SetAssignmentCapacity(ctx, p, w.membership, 3) }()
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	waitForLock := func(pattern string) {
		t.Helper()
		for {
			var found bool
			err := observer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&found)
			if err != nil {
				t.Fatal(err)
			}
			if found {
				return
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForLock("%pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:%")
	suspendDone := make(chan error, 1)
	go func() {
		suspendDone <- testStore.WithPlatform(ctx, func(ctx context.Context) error {
			_, err := testStore.SetTenantStatus(ctx, w.tenant, "suspended")
			return err
		})
	}()
	waitForLock("%pg_advisory_xact_lock(hashtext('chatisp-tenant')%")
	select {
	case err := <-suspendDone:
		t.Fatalf("suspension passed active capacity update: %v", err)
	default:
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if err := <-capacityDone; err != nil {
		t.Fatal(err)
	}
	if err := <-suspendDone; err != nil {
		t.Fatal(err)
	}
	if err := live(nil).SetAssignmentCapacity(ctx, p, w.membership, 4); !errors.Is(err, ErrTenantInactive) {
		t.Fatalf("suspended tenant: %v", err)
	}
}

func TestAssignmentLastSlot(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	second := w
	second.contact, second.identity = uuid.New(), uuid.New()
	_, member := seedMember(t, w.tenant, "agent")
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'second')`, second.contact, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, second.identity, w.tenant, second.contact, w.connector, second.identity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, w.tenant, member)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := live(nil)
	a := mustReceive(t, svc, w, "slot-a", "a", at(0)).Conversation
	b := mustReceive(t, svc, second, "slot-b", "b", at(0)).Conversation
	errs := make(chan error, 2)
	for _, conv := range []store.Conversation{a, b} {
		go func(conv store.Conversation) {
			_, err := svc.TransferToMember(ctx, principal(w), AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: member, ExpectedVersion: conv.Version, ClientRequestID: conv.ID.String(), At: at(1)})
			errs <- err
		}(conv)
	}
	wins, full := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrCapacityFull) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || full != 1 {
		t.Fatalf("wins=%d full=%d", wins, full)
	}
}

func TestTransferSectorRevokesSourceAccess(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	conv := mustReceive(t, svc, w, "sector-transfer", "hello", at(0)).Conversation
	target := uuid.New()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Destino')`, w.tenant, target); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, w.tenant, target, w.inbox)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.TransferToSector(ctx, principal(w), AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, TargetID: target, ExpectedVersion: conv.Version, ClientRequestID: "sector", At: at(1)})
	if err != nil || got.AssigneeID != uuid.Nil || got.Protocol != conv.Protocol {
		t.Fatalf("transfer: %+v %v", got, err)
	}
	if _, err := svc.History(ctx, principal(w), w.inbox, conv.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("history after revocation: %v", err)
	}
	n := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM assignment_queue_intervals WHERE tenant_id=$1 AND conversation_id=$2 AND sector_id=$3 AND ended_at IS NULL`, w.tenant, conv.ID, target)
	if n != 1 {
		t.Fatalf("destination intervals: %d", n)
	}
}
