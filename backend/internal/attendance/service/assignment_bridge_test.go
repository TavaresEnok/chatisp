package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReopenThenAssumeTransferCompeteForLastSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w := seedWorld(t)
	second := w
	second.contact, second.identity = uuid.New(), uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'second')`, second.contact, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, second.identity, w.tenant, second.contact, w.connector, second.identity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, w.tenant, w.membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc, p := live(nil), principal(w)
	a := mustAssume(t, svc, p, mustReceive(t, svc, w, "bridge-a", "a", at(0)).Conversation)
	closed, err := svc.Close(ctx, p, CloseInput{InboxID: w.inbox, ConversationID: a.ID, ExpectedVersion: a.Version, ClientRequestID: "race-close", Reason: "done", Result: "resolved", Summary: "done", At: at(2)})
	if err != nil {
		t.Fatal(err)
	}
	a, err = svc.Reopen(ctx, p, ReopenInput{InboxID: w.inbox, ConversationID: a.ID, ExpectedVersion: closed.Conversation.Version, ClientRequestID: "race-reopen", At: at(3)})
	if err != nil || a.AssigneeID != uuid.Nil {
		t.Fatalf("reopen: %+v %v", a, err)
	}
	b := mustReceive(t, svc, second, "bridge-b", "b", at(0)).Conversation
	held, release, lockDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
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
	errs := make(chan error, 2)
	go func() { _, err := svc.Assume(ctx, p, w.inbox, a.ID, a.Version, at(4)); errs <- err }()
	go func() {
		_, err := svc.TransferToMember(ctx, p, AssignmentInput{InboxID: w.inbox, ConversationID: b.ID, TargetID: w.membership, ExpectedVersion: b.Version, ClientRequestID: "bridge-transfer", At: at(4)})
		errs <- err
	}()
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	for {
		var n int
		if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock(hashtextextended(''chatisp-assignment-capacity:%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
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
	if n := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM conversations WHERE tenant_id=$1 AND assignee_id=$2 AND status IN ('open','waiting_customer')`, w.tenant, w.membership); n != 1 {
		t.Fatalf("occupancy=%d", n)
	}
}

func TestReopenReturnsToQueueEvenWhenCapacityFull(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	second := w
	second.contact, second.identity = uuid.New(), uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'second')`, second.contact, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, second.identity, w.tenant, second.contact, w.connector, second.identity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, w.tenant, w.membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc, p := live(nil), principal(w)
	a := mustAssume(t, svc, p, mustReceive(t, svc, w, "bridge-close", "a", at(0)).Conversation)
	closed, err := svc.Close(ctx, p, CloseInput{InboxID: w.inbox, ConversationID: a.ID, ExpectedVersion: a.Version, ClientRequestID: "bridge-close", Reason: "done", Result: "resolved", Summary: "done", At: at(2)})
	if err != nil {
		t.Fatal(err)
	}
	_ = mustAssume(t, svc, p, mustReceive(t, svc, second, "bridge-fill", "b", at(3)).Conversation)
	in := ReopenInput{InboxID: w.inbox, ConversationID: a.ID, ExpectedVersion: closed.Conversation.Version, ClientRequestID: "bridge-reopen", At: at(4)}
	reopened, err := svc.Reopen(ctx, p, in)
	if err != nil || reopened.AssigneeID != uuid.Nil || reopened.Controller != store.ControllerMenu || reopened.Protocol != a.Protocol {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
	if n := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM conversations WHERE tenant_id=$1 AND assignee_id=$2 AND status IN ('open','waiting_customer')`, w.tenant, w.membership); n != 1 {
		t.Fatalf("occupancy after reopen=%d", n)
	}
	replay, err := svc.Reopen(ctx, p, in)
	if err != nil || replay.Version != reopened.Version || replay.CycleNumber != reopened.CycleNumber {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = svc.Assume(ctx, p, w.inbox, a.ID, reopened.Version, at(5)); !errors.Is(err, store.ErrCapacityFull) {
		t.Fatalf("assume full: %v", err)
	}
}

// A shared row lock holds both real Assume paths at the conversation boundary.
// If authorization took SHARE before UPDATE, both transactions could promote
// simultaneously after release and PostgreSQL would abort one as a deadlock.
func TestConcurrentAssumeDoesNotPromoteConversationShareLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w := seedWorld(t)
	svc, p := live(nil), principal(w)
	c := mustReceive(t, svc, w, "bridge-lock-order", "a", at(0)).Conversation
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	blocker, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR SHARE`, w.tenant, c.ID); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := svc.Assume(ctx, p, w.inbox, c.ID, c.Version, at(1)); errs <- err }()
	}
	for {
		// PostgreSQL caches statistics snapshots inside the blocker's transaction.
		if _, err := admin.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%-- name: LockConversation :one%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wins, stale := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrStaleVersion) {
			stale++
		} else {
			t.Fatalf("concurrent assume: %v", err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("wins=%d stale=%d", wins, stale)
	}
}

func TestAssumeRollbackReleasesCapacity(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc, p := live(nil), principal(w)
	a := mustReceive(t, svc, w, "bridge-rollback", "a", at(0)).Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, w.tenant, w.membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := (store.Repo{}).Assume(ctx, tx, store.AssumeInput{TenantID: w.tenant, ConversationID: a.ID, MembershipID: w.membership, ExpectedVersion: a.Version, At: at(1)}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	got, err := svc.Assume(ctx, p, w.inbox, a.ID, a.Version, at(1))
	if err != nil || got.AssigneeID != w.membership {
		t.Fatalf("assume after rollback: %+v %v", got, err)
	}
	replay, err := svc.Assume(ctx, p, w.inbox, a.ID, got.Version, at(2))
	if err != nil || replay.Version != got.Version {
		t.Fatalf("idempotent assume: %+v %v", replay, err)
	}
}
