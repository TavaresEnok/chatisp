package service

import (
	"chatisp/internal/attendance/store"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestAnnotationsPrivateAndPersonal(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "annotation-inbound", "oi", at(1))
	c := got.Conversation
	u, m := seedMember(t, w.tenant, "supervisor")
	supervisor := Principal{TenantID: w.tenant, UserID: u, MembershipID: m, Role: "supervisor"}
	tag, err := svc.CreateAnnotationTag(ctx, supervisor, "Financeiro")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateAnnotationTag(ctx, p, "denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent catalog: %v", err)
	}
	if _, err = svc.AnnotationChange(ctx, p, w.inbox, c.ID, "priority", uuid.Nil, "urgent", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent priority: %v", err)
	}
	if _, err = svc.AnnotationChange(ctx, p, w.inbox, c.ID, "tag_add", tag.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AnnotationChange(ctx, supervisor, w.inbox, c.ID, "priority", uuid.Nil, "urgent", ""); err != nil {
		t.Fatal(err)
	}
	before, err := svc.History(ctx, p, w.inbox, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobs := countJobs(t, ctx, w.tenant)
	v, err := svc.AnnotationChange(ctx, p, w.inbox, c.ID, "note", uuid.Nil, "segredo", "note-1")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := svc.AnnotationChange(ctx, p, w.inbox, c.ID, "note", uuid.Nil, "segredo", "note-1")
	if err != nil || v != v2 {
		t.Fatalf("replay: %d %d %v", v, v2, err)
	}
	if _, err = svc.AnnotationChange(ctx, p, w.inbox, c.ID, "note", uuid.Nil, "outro", "note-1"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("conflict: %v", err)
	}
	after, err := svc.History(ctx, p, w.inbox, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Head.Version != after.Head.Version || !before.Head.WaitingSince.Equal(after.Head.WaitingSince) || len(before.Messages) != len(after.Messages) || countJobs(t, ctx, w.tenant) != jobs {
		t.Fatal("note changed attendance or sent a message")
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, after.NextCursor, false, 0); err != nil {
		t.Fatal(err)
	}
	if err = svc.AnnotationMarkRead(ctx, supervisor, w.inbox, c.ID, 0, true, 0); err != nil {
		t.Fatal(err)
	}
	a, err := svc.AnnotationRead(ctx, p, w.inbox, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.AnnotationRead(ctx, supervisor, w.inbox, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Unread || !b.Unread || a.Watermark != after.NextCursor || b.Watermark != 0 || a.Priority != "urgent" || len(a.Tags) != 1 || len(a.Notes) != 1 {
		t.Fatalf("states: %+v %+v", a, b)
	}
	mustReceive(t, svc, w, "annotation-second", "nova", at(2))
	if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM realtime_events WHERE tenant_id=$1 AND seq<=$2`, w.tenant, after.NextCursor)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, after.NextCursor, false, 1); err != nil {
		t.Fatalf("compacted watermark: %v", err)
	}
	a, err = svc.AnnotationRead(ctx, p, w.inbox, c.ID)
	if err != nil || !a.Unread || a.UnreadCount != 1 {
		t.Fatalf("new message: %+v %v", a, err)
	}
	other := seedWorld(t)
	if err = testStore.WithTenantTx(ctx, other.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_notes WHERE tenant_id=$1`, w.tenant).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatalf("RLS exposed %d notes", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ManageAnnotationTag(ctx, p, tag.ID, "renamed", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent rename: %v", err)
	}
	if _, err = svc.ManageAnnotationTag(ctx, supervisor, tag.ID, "renamed", false); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ManageAnnotationTag(ctx, supervisor, tag.ID, "", true); err != nil {
		t.Fatal(err)
	}
	a, err = svc.AnnotationRead(ctx, p, w.inbox, c.ID)
	if err != nil || len(a.Tags) != 0 || len(a.Events) != 5 {
		t.Fatalf("timeline after catalog changes: %+v %v", a, err)
	}
}

func TestAnnotationReadIntentVersion(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	c := mustReceive(t, svc, w, "intent", "oi", at(1)).Conversation
	h, err := svc.History(ctx, p, w.inbox, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, h.NextCursor, false, 0); err != nil {
		t.Fatal(err)
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, h.NextCursor, true, 1); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []int64{h.NextCursor, 0} {
		if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, cursor, false, 0); !errors.Is(err, store.ErrIdempotencyConflict) {
			t.Fatalf("stale replay: %v", err)
		}
	}
	s, err := svc.AnnotationRead(ctx, p, w.inbox, c.ID)
	if err != nil || !s.Unread || s.ReadVersion != 2 || s.Watermark != h.NextCursor {
		t.Fatalf("state: %+v %v", s, err)
	}
	if err = svc.AnnotationMarkRead(ctx, p, w.inbox, c.ID, 0, false, 2); err != nil {
		t.Fatal(err)
	}
	s, err = svc.AnnotationRead(ctx, p, w.inbox, c.ID)
	if err != nil || s.Unread || s.Watermark != h.NextCursor {
		t.Fatalf("state: %+v %v", s, err)
	}
}

func TestCatalogAuthorizationLocks(t *testing.T) {
	for _, action := range []string{"membership", "tenant"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			w := seedWorld(t)
			u, m := seedMember(t, w.tenant, "supervisor")
			p := Principal{TenantID: w.tenant, UserID: u, MembershipID: m, Role: "supervisor"}
			svc := live(nil)
			locked := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
					if err := svc.authorizeAnnotationCatalog(ctx, tx, p); err != nil {
						return err
					}
					close(locked)
					<-release
					return nil
				})
			}()
			<-locked
			changed := make(chan error, 1)
			go func() {
				if action == "membership" {
					changed <- testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
						_, err := tx.Exec(ctx, `UPDATE memberships SET role='agent' WHERE tenant_id=$1 AND id=$2`, w.tenant, m)
						return err
					})
				} else {
					changed <- testStore.WithPlatform(ctx, func(ctx context.Context) error {
						_, err := testStore.SetTenantStatus(ctx, w.tenant, "suspended")
						return err
					})
				}
			}()
			select {
			case err := <-changed:
				t.Fatalf("authorization not pinned: %v", err)
			case <-time.After(200 * time.Millisecond):
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatal(err)
			}
			if _, err := svc.CreateAnnotationTag(ctx, p, "blocked"); err == nil {
				t.Fatal("stale principal accepted")
			}
			if _, err := svc.AnnotationCatalog(ctx, p); err == nil {
				t.Fatal("stale catalog accepted")
			}
		})
	}
}
