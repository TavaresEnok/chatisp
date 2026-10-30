package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
)

func TestAuthorizationRevalidatesReplay(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-auth", "olá", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "resposta", "req-auth")

	super := Principal{UserID: uuid.New(), TenantID: w.tenant, IsSuperadmin: true}
	if _, err := svc.List(ctx, super, w.inbox); !errors.Is(err, ErrForbidden) {
		t.Fatalf("superadmin sem ingresso: %v", err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w.membership, "disabled")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Send(ctx, p, SendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "req-auth", Body: "resposta", At: at(3),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay sem acesso: %v", err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id = $1 AND id = $2`, w.tenant, sent.MessageID) != 1 {
		t.Fatal("replay negado apagou a mensagem")
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w.membership, "active")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.SetTenantStatus(ctx, w.tenant, "suspended")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, p, w.inbox); !errors.Is(err, ErrTenantInactive) {
		t.Fatalf("tenant suspenso: %v", err)
	}
}

func TestSendIdempotencyRaceConflictAndRollback(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-idem", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)

	var wg sync.WaitGroup
	type outcome struct {
		res SendResult
		err error
	}
	results := make([]outcome, 2)
	ready := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-ready
			res, err := svc.Send(ctx, p, SendInput{
				InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
				ClientRequestID: "req-race", Body: "mesmo texto", At: at(4),
			})
			results[i] = outcome{res, err}
		}(i)
	}
	close(ready)
	wg.Wait()
	for i, item := range results {
		if item.err != nil {
			t.Fatalf("envio %d: %v", i, item.err)
		}
	}
	if results[0].res.MessageID != results[1].res.MessageID || results[0].res.AttemptID != results[1].res.AttemptID {
		t.Fatalf("corrida criou recursos diferentes: %+v %+v", results[0].res, results[1].res)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id = $1 AND conversation_id = $2 AND direction = 'outbound'`, w.tenant, assumed.ID) != 1 {
		t.Fatal("mais de uma mensagem")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND conversation_id = $2`, w.tenant, assumed.ID) != 1 {
		t.Fatal("mais de uma outbox")
	}
	if countJobs(t, ctx, w.tenant) != 1 {
		t.Fatalf("jobs: %d", countJobs(t, ctx, w.tenant))
	}
	replayed := results[0].res.Replayed || results[1].res.Replayed
	if !replayed {
		t.Fatal("a corrida não devolveu o recurso já gravado")
	}

	_, err := svc.Send(ctx, p, SendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "req-race", Body: "outro texto", At: at(4),
	})
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("payload diferente: %v", err)
	}

	failed := live(failQueue{})
	_, err = failed.Send(ctx, p, SendInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "req-rollback", Body: "não pode ficar", At: at(5),
	})
	if err == nil {
		t.Fatal("envio sem job foi aceito")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM messages WHERE tenant_id = $1 AND body = 'não pode ficar'`, w.tenant) != 0 {
		t.Fatal("rollback deixou mensagem")
	}
	if countJobs(t, ctx, w.tenant) != 1 {
		t.Fatal("rollback publicou outro job")
	}
}

func TestConcurrentFirstMessagesAndAssume(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	var wg sync.WaitGroup
	type inbound struct {
		res ReceiveResult
		err error
	}
	got := make([]inbound, 2)
	ready := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-ready
			res, err := svc.ReceiveText(ctx, p, ReceiveInput{
				InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector,
				ExternalMessageID: "ext-" + string(rune('a'+i)), Body: "primeira", Period: "202610",
				TrustedAt: at(1 + i), OpenedAt: at(1),
			})
			got[i] = inbound{res, err}
		}(i)
	}
	close(ready)
	wg.Wait()
	for i, item := range got {
		if item.err != nil {
			t.Fatalf("recebimento %d: %v", i, item.err)
		}
	}
	if got[0].res.Conversation.ID != got[1].res.Conversation.ID || got[0].res.Conversation.Protocol != got[1].res.Conversation.Protocol || got[0].res.Conversation.Protocol == "" {
		t.Fatalf("protocolo divergente: %+v %+v", got[0].res.Conversation, got[1].res.Conversation)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM conversations WHERE tenant_id = $1 AND contact_identity_id = $2 AND status <> 'closed'`, w.tenant, w.identity) != 1 {
		t.Fatal("mais de uma conversa ativa")
	}

	otherUser, otherMember := seedMember(t, w.tenant, "agent")
	other := Principal{UserID: otherUser, MembershipID: otherMember, TenantID: w.tenant, Role: "agent"}
	version := got[0].res.Conversation.Version
	if got[1].res.Conversation.Version > version {
		version = got[1].res.Conversation.Version
	}
	conv, err := svc.List(ctx, p, w.inbox)
	if err != nil || len(conv.Conversations) != 1 {
		t.Fatalf("lista: %v %+v", err, conv.Conversations)
	}
	version = conv.Conversations[0].Version
	var assumeWG sync.WaitGroup
	errs := make([]error, 2)
	winners := make([]store.Conversation, 2)
	gate := make(chan struct{})
	actors := []Principal{p, other}
	for i := 0; i < 2; i++ {
		assumeWG.Add(1)
		go func(i int) {
			defer assumeWG.Done()
			<-gate
			winners[i], errs[i] = svc.Assume(ctx, actors[i], w.inbox, conv.Conversations[0].ID, version, at(6))
		}(i)
	}
	close(gate)
	assumeWG.Wait()
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
			continue
		}
		if !errors.Is(err, store.ErrStaleVersion) {
			t.Fatalf("assumir %d: %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("assumir concorrente: %d vitórias", wins)
	}
	history, err := svc.History(ctx, p, w.inbox, conv.Conversations[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Messages) != 2 {
		t.Fatalf("histórico: %d", len(history.Messages))
	}
	var assumed bool
	for _, event := range history.Timeline {
		if event.Kind == "assumed" && event.ActorID != uuid.Nil {
			assumed = true
		}
	}
	if !assumed {
		t.Fatalf("timeline: %+v", history.Timeline)
	}
}

func TestCloseFarewellReopen(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-close", "preciso de ajuda", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	closed, err := live(failQueue{}).Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-1", Reason: "lentidão", Result: "resolved", Summary: "link restabelecido",
		At: at(8), FarewellBody: "até logo", FarewellRequestID: "bye-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed.FarewellErr == nil || closed.Conversation.Status != store.StatusClosed || closed.Closure.Result != "resolved" {
		t.Fatalf("despedida: %+v", closed)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1`, w.tenant) != 0 {
		t.Fatal("despedida falha deixou outbox")
	}
	again, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-1", Reason: "lentidão", Result: "resolved", Summary: "link restabelecido",
		At: at(8), FarewellBody: "até logo", FarewellRequestID: "bye-1",
	})
	if err != nil || again.Conversation.Protocol != closed.Conversation.Protocol {
		t.Fatalf("replay do encerramento: %v %+v", err, again.Conversation)
	}

	w2 := seedWorld(t)
	p2 := principal(w2)
	svc = live(nil)
	opened := mustReceive(t, svc, w2, "ext-reopen", "oi", at(1))
	owned := mustAssume(t, svc, p2, opened.Conversation)
	done, err := svc.Close(ctx, p2, CloseInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: owned.Version,
		ClientRequestID: "close-2", Reason: "financeiro", Result: "no_response", Summary: "cliente não retornou",
		At: at(9), FarewellBody: "estamos à disposição", FarewellRequestID: "bye-2",
	})
	if err != nil || done.FarewellErr != nil || done.Farewell == nil || done.Farewell.JobID != 0 && done.Conversation.Protocol == "" {
		t.Fatalf("encerramento: %v %+v", err, done)
	}
	if done.Closure.Result != "no_response" {
		t.Fatal(done.Closure.Result)
	}
	if countJobs(t, ctx, w2.tenant) != 1 {
		t.Fatalf("job da despedida: %d", countJobs(t, ctx, w2.tenant))
	}
	reopened, err := svc.Reopen(ctx, p2, ReopenInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: done.Conversation.Version,
		ClientRequestID: "reopen-2", At: at(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Protocol != done.Conversation.Protocol || reopened.CycleNumber != 2 || reopened.Status != store.StatusOpen || reopened.Controller != store.ControllerMenu {
		t.Fatalf("reabertura: %+v", reopened)
	}
	if reopened.ControllerEpoch == done.Conversation.ControllerEpoch {
		t.Fatal("epoch não mudou na reabertura")
	}
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w2.tenant, InboxID: w2.inbox, AttemptID: done.Farewell.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(11),
	})
	if err != nil || reserved.State != "sending" {
		t.Fatalf("despacho da despedida: %v %+v", err, reserved)
	}
	if countQuery(t, ctx, w2.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'sending'`, w2.tenant, done.Farewell.AttemptID) != 1 {
		t.Fatal("despedida confirmada não ficou sending")
	}
	history, err := svc.History(ctx, p2, w2.inbox, reopened.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, event := range history.Timeline {
		kinds[event.Kind] = true
	}
	if !kinds["closed"] || !kinds["reopened"] || !kinds["assumed"] {
		t.Fatalf("timeline: %+v", history.Timeline)
	}
}

func TestLateAcceptanceDoesNotMoveClocks(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-late", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "já respondo", "req-late")
	owner := uuid.New()
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(3),
	})
	if err != nil || reserved.State != "sending" || reserved.LeaseVersion < 1 {
		t.Fatalf("reserva: %v %+v", err, reserved)
	}
	if _, err := svc.ReceiveText(ctx, p, ReceiveInput{
		InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector,
		ExternalMessageID: "ext-late-2", Body: "ainda aqui", Period: "202610", TrustedAt: at(5), OpenedAt: at(5),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "accepted", ExternalMessageID: "wamid-late",
		AcceptedAt: at(3), Documented: true, OwnerID: owner, LeaseVersion: reserved.LeaseVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "accepted" || result.Waiting || !result.IgnoredLate {
		t.Fatalf("aceitação atrasada: %+v", result)
	}
	current := mustHead(t, svc, p, w.inbox, assumed.ID)
	if current.Status != store.StatusOpen || !current.WaitingSince.IsZero() {
		t.Fatalf("relógio moveu: %+v", current)
	}

	w2 := seedWorld(t)
	p2 := principal(w2)
	opened := mustReceive(t, svc, w2, "ext-epoch", "oi", at(1))
	owned := mustAssume(t, svc, p2, opened.Conversation)
	human := mustSend(t, svc, p2, owned, "resposta", "req-epoch")
	epochOwner := uuid.New()
	slot, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w2.tenant, InboxID: w2.inbox, AttemptID: human.AttemptID, OwnerID: epochOwner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := svc.Close(ctx, p2, CloseInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: owned.Version,
		ClientRequestID: "close-epoch", Reason: "financeiro", Result: "referred_external", Summary: "segue no ERP",
		At: at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	late, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w2.tenant, AttemptID: human.AttemptID, To: "accepted", ExternalMessageID: "wamid-epoch",
		AcceptedAt: at(4), Documented: true, OwnerID: epochOwner, LeaseVersion: slot.LeaseVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !late.IgnoredLate || late.Waiting {
		t.Fatalf("epoch antiga: %+v", late)
	}
	reopened, err := svc.Reopen(ctx, p2, ReopenInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: closed.Conversation.Version,
		ClientRequestID: "reopen-epoch", At: at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Protocol != closed.Conversation.Protocol || !reopened.WaitingSince.IsZero() || !reopened.LastEligibleHumanReplyAt.IsZero() {
		t.Fatalf("relógio do ciclo novo: %+v", reopened)
	}
}

func TestReserveFenceAndUncertain(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-fence", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "um", "req-fence-1")
	other := mustSend(t, svc, p, assumed, "dois", "req-fence-2")
	owner := uuid.New()
	first, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: other.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(3),
	})
	if !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("lease: %v", err)
	}
	state, err := svc.RecoverCrashedDispatch(ctx, w.tenant, sent.AttemptID, owner, first.LeaseVersion)
	if err != nil || state != "uncertain" {
		t.Fatalf("queda: %v %s", err, state)
	}
	_, err = svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		LeaseVersion: first.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if !errors.Is(err, store.ErrUncertain) {
		t.Fatalf("reenvio incerto: %v", err)
	}

	bumped := seedWorld(t)
	bp := principal(bumped)
	opened := mustReceive(t, svc, bumped, "ext-stale", "oi", at(1))
	owned := mustAssume(t, svc, bp, opened.Conversation)
	queued := mustSend(t, svc, bp, owned, "auto", "req-stale")
	if err := testStore.WithTenantTx(ctx, bumped.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET controller_epoch = controller_epoch + 1 WHERE tenant_id = $1 AND id = $2`, bumped.tenant, owned.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: bumped.tenant, InboxID: bumped.inbox, AttemptID: queued.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(5),
	})
	if !errors.Is(err, ErrStaleEpoch) || stale.State != "cancelled" {
		t.Fatalf("fence: %v %+v", err, stale)
	}
	if countQuery(t, ctx, bumped.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'cancelled'`, bumped.tenant, queued.AttemptID) != 1 {
		t.Fatal("cancelamento da epoch não ficou gravado")
	}
}

func TestCloseReplayKeepsOriginalCycle(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-cycle", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	firstIn := CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: assumed.Version,
		ClientRequestID: "close-cycle-1", Reason: "lentidão", Result: "resolved", Summary: "ciclo um",
		At: at(8),
	}
	first, err := svc.Close(ctx, p, firstIn)
	if err != nil || first.Closure.CycleNumber != 1 || first.Conversation.Status != store.StatusClosed {
		t.Fatalf("primeiro encerramento: %v %+v", err, first)
	}
	reopenIn := ReopenInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: first.Conversation.Version,
		ClientRequestID: "reopen-cycle-1", At: at(9),
	}
	opened, err := svc.Reopen(ctx, p, reopenIn)
	if err != nil || opened.CycleNumber != 2 {
		t.Fatalf("reabertura: %v %+v", err, opened)
	}
	second, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: opened.Version,
		ClientRequestID: "close-cycle-2", Reason: "financeiro", Result: "spam", Summary: "ciclo dois",
		At: at(10),
	})
	if err != nil || second.Closure.CycleNumber != 2 || second.Closure.Result != "spam" {
		t.Fatalf("segundo encerramento: %v %+v", err, second.Closure)
	}
	againOpened, err := svc.Reopen(ctx, p, ReopenInput{
		InboxID: w.inbox, ConversationID: assumed.ID, ExpectedVersion: second.Conversation.Version,
		ClientRequestID: "reopen-cycle-2", At: at(11),
	})
	if err != nil || againOpened.CycleNumber != 3 {
		t.Fatalf("segunda reabertura: %v %+v", err, againOpened)
	}
	replayed, err := svc.Close(ctx, p, firstIn)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Closure.CycleNumber != 1 || replayed.Closure.Result != "resolved" || replayed.Closure.Summary != "ciclo um" {
		t.Fatalf("replay do fechamento: %+v", replayed.Closure)
	}
	if replayed.Conversation.CycleNumber != 1 || replayed.Conversation.Status != store.StatusClosed || replayed.Conversation.Version != first.Conversation.Version {
		t.Fatalf("conversa do replay: %+v", replayed.Conversation)
	}
	replayedOpen, err := svc.Reopen(ctx, p, reopenIn)
	if err != nil {
		t.Fatal(err)
	}
	if replayedOpen.CycleNumber != 2 || replayedOpen.Status != store.StatusOpen || replayedOpen.Controller != store.ControllerMenu || replayedOpen.Version != opened.Version {
		t.Fatalf("replay da reabertura: %+v", replayedOpen)
	}
}

func TestStaleLeaseDoesNotReplaceFence(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-lease", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "texto", "req-lease")
	owner := uuid.New()
	first, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if err != nil || first.LeaseVersion < 1 {
		t.Fatalf("reserva: %v %+v", err, first)
	}
	retried, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "retrying",
		OwnerID: owner, LeaseVersion: first.LeaseVersion, ErrorCode: "timeout",
	})
	if err != nil || retried.State != "retrying" {
		t.Fatalf("retry: %v %+v", err, retried)
	}
	second, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(5),
	})
	if err != nil || second.State != "sending" || second.LeaseVersion <= first.LeaseVersion {
		t.Fatalf("nova reserva: %v %+v", err, second)
	}
	_, err = svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "failed",
		OwnerID: owner, LeaseVersion: first.LeaseVersion, ErrorCode: "antigo",
	})
	if !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("lease antigo: %v", err)
	}
	_, err = svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "failed", OwnerID: owner,
	})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("lease zero: %v", err)
	}
	_, err = svc.RecoverCrashedDispatch(ctx, w.tenant, sent.AttemptID, owner, first.LeaseVersion)
	if !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("recuperação antiga: %v", err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'sending' AND lease_version = $3`, w.tenant, sent.AttemptID, second.LeaseVersion) != 1 {
		t.Fatal("cerca nova foi substituída")
	}
	observed, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "accepted",
		ExternalMessageID: "wamid-obs", AcceptedAt: at(5), Documented: true, Observation: true,
	})
	if err != nil || observed.State != "accepted" {
		t.Fatalf("observação: %v %+v", err, observed)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'accepted' AND lease_version = $3`, w.tenant, sent.AttemptID, second.LeaseVersion) != 1 {
		t.Fatal("observação trocou a lease")
	}
}

func TestRevokedAuthorIsNotDispatched(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-revoke", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "resposta", "req-revoke")
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w.membership, "disabled")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if !errors.Is(err, ErrForbidden) || reserved.State != "cancelled" {
		t.Fatalf("reserva após revogação: %v %+v", err, reserved)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'cancelled' AND error_code = 'permissao_revogada'`, w.tenant, sent.AttemptID) != 1 {
		t.Fatal("tentativa revogada não ficou cancelada")
	}

	w2 := seedWorld(t)
	p2 := principal(w2)
	opened := mustReceive(t, svc, w2, "ext-revoke-bye", "oi", at(1))
	owned := mustAssume(t, svc, p2, opened.Conversation)
	closed, err := svc.Close(ctx, p2, CloseInput{
		InboxID: w2.inbox, ConversationID: owned.ID, ExpectedVersion: owned.Version,
		ClientRequestID: "close-revoke", Reason: "financeiro", Result: "resolved", Summary: "encerrado",
		At: at(8), FarewellBody: "até logo", FarewellRequestID: "bye-revoke",
	})
	if err != nil || closed.FarewellErr != nil || closed.Farewell == nil {
		t.Fatalf("encerramento: %v %+v", err, closed)
	}
	if err := testStore.WithTenant(ctx, w2.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w2.membership, "disabled")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	bye, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w2.tenant, InboxID: w2.inbox, AttemptID: closed.Farewell.AttemptID, OwnerID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute), At: at(9),
	})
	if !errors.Is(err, ErrForbidden) || bye.State != "cancelled" {
		t.Fatalf("despedida após revogação: %v %+v", err, bye)
	}
	if countQuery(t, ctx, w2.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id = $1 AND id = $2 AND state = 'sending'`, w2.tenant, closed.Farewell.AttemptID) != 0 {
		t.Fatal("despedida revogada foi despachada")
	}

	w3 := seedWorld(t)
	p3 := principal(w3)
	again := mustReceive(t, svc, w3, "ext-revoke-retry", "oi", at(1))
	held := mustAssume(t, svc, p3, again.Conversation)
	retrySend := mustSend(t, svc, p3, held, "de novo", "req-revoke-retry")
	owner := uuid.New()
	first, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w3.tenant, InboxID: w3.inbox, AttemptID: retrySend.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w3.tenant, AttemptID: retrySend.AttemptID, To: "retrying",
		OwnerID: owner, LeaseVersion: first.LeaseVersion, ErrorCode: "timeout",
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, w3.tenant, func(ctx context.Context) error {
		_, err := testStore.UpdateMembershipStatus(ctx, w3.membership, "disabled")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	retried, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w3.tenant, InboxID: w3.inbox, AttemptID: retrySend.AttemptID, OwnerID: owner,
		LeaseVersion: first.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(5),
	})
	if !errors.Is(err, ErrForbidden) || retried.State != "failed" {
		t.Fatalf("retry revogado: %v %+v", err, retried)
	}
}

func TestEarlyReceiptLinksOnResult(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	got := mustReceive(t, svc, w, "ext-receipt", "oi", at(1))
	assumed := mustAssume(t, svc, p, got.Conversation)
	sent := mustSend(t, svc, p, assumed, "já respondo", "req-receipt")
	owner := uuid.New()
	reserved, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: sent.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := store.StatusInput{
		TenantID: w.tenant, ConnectorID: w.connector, ExternalMessageID: "wamid-early",
		ExternalEventID: "evt-early", Kind: "accepted", TrustedAt: at(4),
	}
	early, ignored, err := svc.ReconcileReceipt(ctx, receipt)
	if err != nil || !early.Pending || early.MessageID != uuid.Nil || ignored {
		t.Fatalf("status anterior ao resultado: %v %+v ignored=%v", err, early, ignored)
	}
	result, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: sent.AttemptID, To: "accepted", ExternalMessageID: "wamid-early",
		AcceptedAt: at(4), Documented: true, OwnerID: owner, LeaseVersion: reserved.LeaseVersion,
	})
	if err != nil || result.State != "accepted" || !result.Waiting || result.IgnoredLate {
		t.Fatalf("resultado com recibo pendente: %v %+v", err, result)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM message_status_events WHERE tenant_id = $1 AND external_message_id = 'wamid-early' AND pending = false AND message_id = $2`, w.tenant, sent.MessageID) != 1 {
		t.Fatal("recibo pendente não foi vinculado no resultado")
	}
	current := mustHead(t, svc, p, w.inbox, assumed.ID)
	if current.Status != store.StatusWaitingCustomer {
		t.Fatalf("espera não começou: %+v", current)
	}
	again, ignored, err := svc.ReconcileReceipt(ctx, receipt)
	if err != nil || again.Pending || !again.Duplicate || again.MessageID != sent.MessageID || ignored {
		t.Fatalf("reconciliação repetida: %v %+v ignored=%v", err, again, ignored)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id = $1 AND conversation_id = $2 AND kind = 'wait_started'`, w.tenant, assumed.ID) != 1 {
		t.Fatal("reconciliação repetida abriu outra espera")
	}
}

func TestSuccessiveAcceptedReplies_SendAndReconcile(t *testing.T) {
	ctx := context.Background()
	svc := live(nil)

	// 1. Prova via resultado de envio (RecordAttemptResult)
	w := seedWorld(t)
	p := principal(w)
	opened := mustReceive(t, svc, w, "ext-succ-1", "oi", at(1))
	assumed := mustAssume(t, svc, p, opened.Conversation)

	first := mustSend(t, svc, p, assumed, "primeira resposta", "req-succ-1")
	second := mustSend(t, svc, p, assumed, "segunda resposta", "req-succ-2")

	owner := uuid.New()
	slot1, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: first.AttemptID, OwnerID: owner,
		ExpiresAt: time.Now().Add(time.Minute), At: at(4),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Primeira aceitação humana elegível
	res1, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: first.AttemptID, To: "accepted", ExternalMessageID: "wamid-succ-1",
		AcceptedAt: at(4), Documented: true, OwnerID: owner, LeaseVersion: slot1.LeaseVersion,
	})
	if err != nil || res1.State != "accepted" || !res1.Waiting || res1.IgnoredLate {
		t.Fatalf("primeira aceitação: %v %+v", err, res1)
	}
	head1 := mustHead(t, svc, p, w.inbox, assumed.ID)
	if head1.Status != store.StatusWaitingCustomer || !head1.WaitingSince.Equal(at(4)) {
		t.Fatalf("estado após primeira aceitação: %+v", head1)
	}
	var lastEligible1 time.Time
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_eligible_human_reply_at FROM conversations WHERE tenant_id = $1 AND id = $2`, w.tenant, assumed.ID).Scan(&lastEligible1)
	}); err != nil {
		t.Fatal(err)
	}
	if !lastEligible1.Equal(at(4)) {
		t.Fatalf("last_eligible_human_reply_at após primeira aceitação: %v", lastEligible1)
	}

	slot2, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: second.AttemptID, OwnerID: owner,
		LeaseVersion: slot1.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(6),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Segunda aceitação humana elegível sucessiva: avança relógio e versão, mantém 1 wait_started
	res2, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: second.AttemptID, To: "accepted", ExternalMessageID: "wamid-succ-2",
		AcceptedAt: at(6), Documented: true, OwnerID: owner, LeaseVersion: slot2.LeaseVersion,
	})
	if err != nil || res2.State != "accepted" || !res2.Waiting || res2.IgnoredLate {
		t.Fatalf("segunda aceitação sucessiva: %v %+v", err, res2)
	}
	head2 := mustHead(t, svc, p, w.inbox, assumed.ID)
	if head2.Status != store.StatusWaitingCustomer || !head2.WaitingSince.Equal(at(6)) {
		t.Fatalf("relógio não avançou na segunda aceitação: %+v", head2)
	}
	var lastEligible2 time.Time
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_eligible_human_reply_at FROM conversations WHERE tenant_id = $1 AND id = $2`, w.tenant, assumed.ID).Scan(&lastEligible2)
	}); err != nil {
		t.Fatal(err)
	}
	if !lastEligible2.Equal(at(6)) {
		t.Fatalf("last_eligible_human_reply_at não avançou: %v", lastEligible2)
	}
	if head2.Version <= head1.Version {
		t.Fatalf("versão não avançou: antes=%d, depois=%d", head1.Version, head2.Version)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id = $1 AND conversation_id = $2 AND kind = 'wait_started'`, w.tenant, assumed.ID) != 1 {
		t.Fatal("segunda aceitação duplicou o evento wait_started")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM realtime_events WHERE tenant_id = $1 AND aggregate_id = $2 AND event_type = 'conversation.waiting'`, w.tenant, assumed.ID) < 2 {
		t.Fatal("segunda aceitação não publicou evento realtime conversation.waiting")
	}

	// Replay da segunda aceitação: IgnoredLate verdadeiro, sem retroceder clocks nem avançar versão
	resReplay, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: second.AttemptID, To: "accepted", ExternalMessageID: "wamid-succ-2",
		AcceptedAt: at(6), Documented: true, OwnerID: owner, LeaseVersion: slot2.LeaseVersion,
	})
	if err != nil || resReplay.State != "accepted" || resReplay.Waiting || !resReplay.IgnoredLate {
		t.Fatalf("replay da aceitação: %v %+v", err, resReplay)
	}
	headReplay := mustHead(t, svc, p, w.inbox, assumed.ID)
	if !headReplay.WaitingSince.Equal(at(6)) || headReplay.Version != head2.Version {
		t.Fatalf("replay alterou versão ou relógio: %+v vs %+v", headReplay, head2)
	}

	// Aceitação fora de ordem (at(5) < at(6)): IgnoredLate verdadeiro, não move relógio
	currentConv := assumed
	currentConv.Version = head2.Version
	third := mustSend(t, svc, p, currentConv, "terceira resposta", "req-succ-3")
	slot3, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: w.tenant, InboxID: w.inbox, AttemptID: third.AttemptID, OwnerID: owner,
		LeaseVersion: slot2.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	resOutOfOrder, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: w.tenant, AttemptID: third.AttemptID, To: "accepted", ExternalMessageID: "wamid-succ-3",
		AcceptedAt: at(5), Documented: true, OwnerID: owner, LeaseVersion: slot3.LeaseVersion,
	})
	if err != nil || resOutOfOrder.State != "accepted" || resOutOfOrder.Waiting || !resOutOfOrder.IgnoredLate {
		t.Fatalf("aceitação fora de ordem: %v %+v", err, resOutOfOrder)
	}
	headOutOfOrder := mustHead(t, svc, p, w.inbox, assumed.ID)
	if !headOutOfOrder.WaitingSince.Equal(at(6)) {
		t.Fatalf("aceitação fora de ordem retrocedeu waiting_since: %+v", headOutOfOrder)
	}

	// 2. Prova via reconciliação de recibo (ReconcileReceipt / RecordReceipt)
	wRec := seedWorld(t)
	pRec := principal(wRec)
	openedRec := mustReceive(t, svc, wRec, "ext-rec-base", "olá", at(1))
	assumedRec := mustAssume(t, svc, pRec, openedRec.Conversation)

	sendRec1 := mustSend(t, svc, pRec, assumedRec, "rec resposta 1", "req-rec-1")
	sendRec2 := mustSend(t, svc, pRec, assumedRec, "rec resposta 2", "req-rec-2")

	ownerRec := uuid.New()
	slotRec1, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: wRec.tenant, InboxID: wRec.inbox, AttemptID: sendRec1.AttemptID, OwnerID: ownerRec,
		ExpiresAt: time.Now().Add(time.Minute), At: at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: wRec.tenant, AttemptID: sendRec1.AttemptID, To: "sending", ExternalMessageID: "wamid-rec-1",
		OwnerID: ownerRec, LeaseVersion: slotRec1.LeaseVersion,
	}); err != nil {
		t.Fatal(err)
	}

	slotRec2, err := svc.ReserveDispatch(ctx, ReserveInput{
		TenantID: wRec.tenant, InboxID: wRec.inbox, AttemptID: sendRec2.AttemptID, OwnerID: ownerRec,
		LeaseVersion: slotRec1.LeaseVersion, ExpiresAt: time.Now().Add(time.Minute), At: at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordAttemptResult(ctx, AttemptResultInput{
		TenantID: wRec.tenant, AttemptID: sendRec2.AttemptID, To: "sending", ExternalMessageID: "wamid-rec-2",
		OwnerID: ownerRec, LeaseVersion: slotRec2.LeaseVersion,
	}); err != nil {
		t.Fatal(err)
	}

	// Recibo 1 aceito em at(4)
	receipt1 := store.StatusInput{
		TenantID: wRec.tenant, ConnectorID: wRec.connector, ExternalMessageID: "wamid-rec-1",
		ExternalEventID: "evt-rec-1", Kind: "accepted", TrustedAt: at(4),
	}
	r1, ignored1, err := svc.ReconcileReceipt(ctx, receipt1)
	if err != nil || r1.Pending || ignored1 {
		t.Fatalf("primeiro recibo: %v %+v ignored=%v", err, r1, ignored1)
	}
	headRec1 := mustHead(t, svc, pRec, wRec.inbox, assumedRec.ID)
	if headRec1.Status != store.StatusWaitingCustomer || !headRec1.WaitingSince.Equal(at(4)) {
		t.Fatalf("espera do primeiro recibo: %+v", headRec1)
	}

	// Recibo 2 aceito em at(6) (aceitação sucessiva)
	receipt2 := store.StatusInput{
		TenantID: wRec.tenant, ConnectorID: wRec.connector, ExternalMessageID: "wamid-rec-2",
		ExternalEventID: "evt-rec-2", Kind: "accepted", TrustedAt: at(6),
	}
	r2, ignored2, err := svc.ReconcileReceipt(ctx, receipt2)
	if err != nil || r2.Pending || ignored2 {
		t.Fatalf("segundo recibo: %v %+v ignored=%v", err, r2, ignored2)
	}
	headRec2 := mustHead(t, svc, pRec, wRec.inbox, assumedRec.ID)
	if headRec2.Status != store.StatusWaitingCustomer || !headRec2.WaitingSince.Equal(at(6)) {
		t.Fatalf("relógio do segundo recibo não avançou: %+v", headRec2)
	}
	var lastEligibleRec2 time.Time
	if err := testStore.WithTenantTx(ctx, wRec.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_eligible_human_reply_at FROM conversations WHERE tenant_id = $1 AND id = $2`, wRec.tenant, assumedRec.ID).Scan(&lastEligibleRec2)
	}); err != nil {
		t.Fatal(err)
	}
	if !lastEligibleRec2.Equal(at(6)) {
		t.Fatalf("last_eligible_human_reply_at do segundo recibo não avançou: %v", lastEligibleRec2)
	}
	if headRec2.Version <= headRec1.Version {
		t.Fatalf("versão do segundo recibo não avançou: antes=%d, depois=%d", headRec1.Version, headRec2.Version)
	}
	if countQuery(t, ctx, wRec.tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id = $1 AND conversation_id = $2 AND kind = 'wait_started'`, wRec.tenant, assumedRec.ID) != 1 {
		t.Fatal("segundo recibo duplicou o evento wait_started")
	}

	// Replay do segundo recibo
	againRec, ignoredAgain, err := svc.ReconcileReceipt(ctx, receipt2)
	if err != nil || againRec.Pending || !againRec.Duplicate || ignoredAgain {
		t.Fatalf("replay do recibo: %v %+v ignored=%v", err, againRec, ignoredAgain)
	}
	headRecReplay := mustHead(t, svc, pRec, wRec.inbox, assumedRec.ID)
	if !headRecReplay.WaitingSince.Equal(at(6)) || headRecReplay.Version != headRec2.Version {
		t.Fatalf("replay alterou versão ou relógio do recibo: %+v", headRecReplay)
	}

	// Recibo fora de ordem (at(3) < at(6))
	receiptOutOfOrder := store.StatusInput{
		TenantID: wRec.tenant, ConnectorID: wRec.connector, ExternalMessageID: "wamid-rec-2",
		ExternalEventID: "evt-rec-late", Kind: "accepted", TrustedAt: at(3),
	}
	_, ignoredLateRec, err := svc.ReconcileReceipt(ctx, receiptOutOfOrder)
	if err != nil || !ignoredLateRec {
		t.Fatalf("recibo fora de ordem: %v ignored=%v", err, ignoredLateRec)
	}
	headRecLate := mustHead(t, svc, pRec, wRec.inbox, assumedRec.ID)
	if !headRecLate.WaitingSince.Equal(at(6)) {
		t.Fatalf("recibo fora de ordem retrocedeu waiting_since: %+v", headRecLate)
	}
}

func mustReceive(t *testing.T, svc Service, w world, external, body string, when time.Time) ReceiveResult {
	t.Helper()
	got, err := svc.ReceiveText(context.Background(), principal(w), ReceiveInput{
		InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector,
		ExternalMessageID: external, Body: body, Period: "202610", TrustedAt: when, OpenedAt: when,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustAssume(t *testing.T, svc Service, p Principal, conv store.Conversation) store.Conversation {
	t.Helper()
	got, err := svc.Assume(context.Background(), p, conv.InboxID, conv.ID, conv.Version, at(2))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustSend(t *testing.T, svc Service, p Principal, conv store.Conversation, body, requestID string) SendResult {
	t.Helper()
	got, err := svc.Send(context.Background(), p, SendInput{
		InboxID: conv.InboxID, ConversationID: conv.ID, ExpectedVersion: conv.Version,
		ClientRequestID: requestID, Body: body, At: at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustHead(t *testing.T, svc Service, p Principal, inboxID, conversationID uuid.UUID) store.Head {
	t.Helper()
	snap, err := svc.List(context.Background(), p, inboxID)
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range snap.Conversations {
		if head.ID == conversationID {
			return head
		}
	}
	t.Fatal("conversa ausente da lista")
	return store.Head{}
}
