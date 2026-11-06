package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/lifecycle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestManualAssumeClearsSnooze(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	conv := mustAssume(t, svc, p, mustReceive(t, svc, w, "snooze-manual", "oi", at(0)).Conversation)
	life := lifecycle.New(testStore)
	life.Now = func() time.Time { return at(3) }
	paused, err := life.Snooze(ctx, lifecycle.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, conv.ID, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: w.inbox, ExpectedVersion: conv.Version, Until: at(60), Reason: "retorno"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Assume(ctx, p, w.inbox, conv.ID, paused.Version-1, at(4)); !errors.Is(err, store.ErrStaleVersion) {
		t.Fatalf("stale assume: %v", err)
	}
	if _, err = life.Get(ctx, lifecycle.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, w.inbox, conv.ID); err != nil {
		t.Fatalf("stale command cleared snooze: %v", err)
	}
	_, err = svc.Assume(ctx, p, w.inbox, conv.ID, paused.Version, at(4))
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state FROM conversation_snoozes WHERE tenant_id=$1 AND id=$2`, w.tenant, paused.ID).Scan(&state)
	}); err != nil {
		t.Fatal(err)
	}
	if state == "active" {
		t.Fatal("manual takeover left snooze active")
	}
}

func TestManualTransitionsClearSnoozeAndStayCleared(t *testing.T) {
	for _, kind := range []string{"member", "sector", "unassign", "close"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			w := seedWorld(t)
			svc := live(nil)
			p := principal(w)
			conv := mustAssume(t, svc, p, mustReceive(t, svc, w, "snooze-"+kind, "oi", at(0)).Conversation)
			life := lifecycle.New(testStore)
			life.Now = func() time.Time { return at(3) }
			paused, err := life.Snooze(ctx, lifecycle.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, conv.ID, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: w.inbox, ExpectedVersion: conv.Version, Until: at(60), Reason: "retorno"})
			if err != nil {
				t.Fatal(err)
			}
			in := AssignmentInput{InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: paused.Version, ClientRequestID: "manual-" + kind, At: at(4)}
			switch kind {
			case "member":
				_, in.TargetID = seedMember(t, w.tenant, "agent")
				_, err = svc.TransferToMember(ctx, p, in)
			case "sector":
				if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
					return tx.QueryRow(ctx, `SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2`, w.tenant, conv.ID).Scan(&in.TargetID)
				}); err != nil {
					t.Fatal(err)
				}
				_, err = svc.TransferToSector(ctx, p, in)
			case "unassign":
				_, err = svc.Unassign(ctx, p, in)
			case "close":
				_, err = svc.Close(ctx, p, CloseInput{InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: paused.Version, ClientRequestID: "close-snoozed", Reason: "resolvido", Result: "resolved", Summary: "ok", At: at(4)})
			}
			if err != nil {
				t.Fatal(err)
			}
			var state, resolution string
			var events int
			if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
				if err := tx.QueryRow(ctx, `SELECT state,resolution FROM conversation_snoozes WHERE tenant_id=$1 AND id=$2`, w.tenant, paused.ID).Scan(&state, &resolution); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='snooze_cleared'`, w.tenant, conv.ID).Scan(&events); err != nil {
					return err
				}
				releases, err := lifecycle.ReturnDue(ctx, tx, w.tenant, at(61))
				if err != nil {
					return err
				}
				if len(releases) != 0 {
					t.Fatalf("manual transition left due snooze: %+v", releases)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if state == "active" || events != 1 {
				t.Fatalf("state=%s resolution=%s events=%d", state, resolution, events)
			}
		})
	}
}
