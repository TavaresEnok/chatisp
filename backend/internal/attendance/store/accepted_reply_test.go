package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestNoteAcceptedReply_SuccessiveReplies(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	trusted := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	var opened OpenResult
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		opened, err = (Repo{}).Open(ctx, tx, OpenInput{
			TenantID:   w.tenant,
			InboxID:    w.inbox,
			ContactID:  w.contact,
			IdentityID: w.identity,
			Period:     "202610",
			OpenedAt:   trusted,
			Status:     StatusOpen,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var assumed Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		assumed, err = (Repo{}).Assume(ctx, tx, AssumeInput{
			TenantID:        w.tenant,
			ConversationID:  opened.Conversation.ID,
			ExpectedVersion: opened.Conversation.Version,
			MembershipID:    w.membership,
			At:              trusted.Add(time.Minute),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// 1st eligible acceptance: transitions from open to waiting_customer.
	t1 := trusted.Add(2 * time.Minute)
	var firstWait Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		firstWait, err = (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, assumed.ID, assumed.Version, t1)
		return err
	}); err != nil {
		t.Fatalf("primeira aceitação: %v", err)
	}
	if firstWait.Status != StatusWaitingCustomer {
		t.Fatalf("status esperado waiting_customer, obteve %s", firstWait.Status)
	}
	if !firstWait.WaitingSince.Equal(t1) {
		t.Fatalf("waiting_since esperado %v, obteve %v", t1, firstWait.WaitingSince)
	}
	if !firstWait.LastEligibleHumanReplyAt.Equal(t1) {
		t.Fatalf("last_eligible_human_reply_at esperado %v, obteve %v", t1, firstWait.LastEligibleHumanReplyAt)
	}
	if firstWait.Version <= assumed.Version {
		t.Fatalf("versão deveria ter avançado: antes=%d, depois=%d", assumed.Version, firstWait.Version)
	}

	var waitStartedEvents int
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM conversation_events
			WHERE tenant_id = $1 AND conversation_id = $2 AND kind = 'wait_started'
		`, w.tenant, assumed.ID).Scan(&waitStartedEvents)
	}); err != nil {
		t.Fatal(err)
	}
	if waitStartedEvents != 1 {
		t.Fatalf("esperava 1 evento wait_started, obteve %d", waitStartedEvents)
	}

	// 2nd eligible acceptance in same cycle/epoch: advances waiting_since and version, maintains 1 wait_started event.
	t2 := trusted.Add(5 * time.Minute)
	var secondWait Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		secondWait, err = (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, firstWait.ID, firstWait.Version, t2)
		return err
	}); err != nil {
		t.Fatalf("segunda aceitação sucessiva: %v", err)
	}
	if secondWait.Status != StatusWaitingCustomer {
		t.Fatalf("status esperado waiting_customer, obteve %s", secondWait.Status)
	}
	if !secondWait.WaitingSince.Equal(t2) {
		t.Fatalf("waiting_since deveria avançar para %v, obteve %v", t2, secondWait.WaitingSince)
	}
	if !secondWait.LastEligibleHumanReplyAt.Equal(t2) {
		t.Fatalf("last_eligible_human_reply_at deveria avançar para %v, obteve %v", t2, secondWait.LastEligibleHumanReplyAt)
	}
	if secondWait.Version <= firstWait.Version {
		t.Fatalf("versão deveria ter avançado: antes=%d, depois=%d", firstWait.Version, secondWait.Version)
	}

	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM conversation_events
			WHERE tenant_id = $1 AND conversation_id = $2 AND kind = 'wait_started'
		`, w.tenant, assumed.ID).Scan(&waitStartedEvents)
	}); err != nil {
		t.Fatal(err)
	}
	if waitStartedEvents != 1 {
		t.Fatalf("segunda aceitação não pode duplicar wait_started: count=%d", waitStartedEvents)
	}

	// Realtime event published for both transitions.
	var waitingRtEvents int
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM realtime_events
			WHERE tenant_id = $1 AND aggregate_id = $2 AND event_type = 'conversation.waiting'
		`, w.tenant, assumed.ID).Scan(&waitingRtEvents)
	}); err != nil {
		t.Fatal(err)
	}
	if waitingRtEvents < 2 {
		t.Fatalf("esperava pelo menos 2 eventos conversation.waiting em realtime_events, obteve %d", waitingRtEvents)
	}

	// Replay of t2: does not advance version or clocks, does not duplicate wait_started.
	var replayWait Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		replayWait, err = (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, secondWait.ID, secondWait.Version, t2)
		return err
	}); err != nil {
		t.Fatalf("replay aceitação: %v", err)
	}
	if replayWait.Version != secondWait.Version || !replayWait.WaitingSince.Equal(t2) {
		t.Fatalf("replay alterou versão ou relógio: %+v vs %+v", replayWait, secondWait)
	}

	// Out-of-order acceptance (t1 < t2): does not retrocede clocks or advance version.
	var outOfOrderWait Conversation
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		outOfOrderWait, err = (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, secondWait.ID, secondWait.Version, t1)
		return err
	}); err != nil {
		t.Fatalf("aceitação fora de ordem: %v", err)
	}
	if outOfOrderWait.Version != secondWait.Version || !outOfOrderWait.WaitingSince.Equal(t2) {
		t.Fatalf("aceitação fora de ordem retrocedeu relógio: %+v vs %+v", outOfOrderWait, secondWait)
	}

	// Customer message arrives later at t3:
	t3 := trusted.Add(10 * time.Minute)
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		inbound, err := (Repo{}).AcceptInbound(ctx, tx, InboundInput{
			TenantID:          w.tenant,
			InboxID:           w.inbox,
			ContactID:         w.contact,
			IdentityID:        w.identity,
			ConnectorID:       w.connector,
			ExternalMessageID: "ext-cust-1",
			Body:              "mensagem posterior",
			Period:            "202610",
			TrustedAt:         t3,
			OpenedAt:          trusted,
		})
		if err != nil {
			return err
		}
		if inbound.Conversation.Status != StatusOpen {
			t.Fatalf("cliente posterior deveria reabrir para open, status=%s", inbound.Conversation.Status)
		}
		// A late acceptance timestamped before customer message must not start waiting.
		late, err := (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, inbound.Conversation.ID, inbound.Conversation.Version, t2)
		if err != nil {
			return err
		}
		if late.Status != StatusOpen {
			t.Fatalf("aceitação anterior à mensagem do cliente moveu status para %s", late.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Stale version error check.
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := (Repo{}).NoteAcceptedReply(ctx, tx, w.tenant, assumed.ID, 999999, t2)
		if !errors.Is(err, ErrStaleVersion) {
			t.Fatalf("esperava ErrStaleVersion, obteve %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
