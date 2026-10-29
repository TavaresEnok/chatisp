package conversation

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	tenantID  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	inboxID   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	contactID = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	agentA    = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	agentB    = uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	actorID   = uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
)

func at(minute int) time.Time {
	return time.Date(2026, 10, 29, 12, minute, 0, 0, time.UTC)
}

func newOpen(t *testing.T) Conversation {
	t.Helper()
	c, err := New(Draft{
		ID:        uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		TenantID:  tenantID,
		InboxID:   inboxID,
		ContactID: contactID,
		OpenedAt:  at(0),
		Status:    StatusOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func assume(t *testing.T, c Conversation, agent uuid.UUID) Conversation {
	t.Helper()
	next, _, _, err := Assume(c, nil, AssumeCommand{
		ExpectedVersion: c.Version,
		AssigneeID:      agent,
		ActorID:         agent,
		At:              at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func humanAccepted(when time.Time) OutboundMessage {
	return OutboundMessage{
		ID:                   uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		Origin:               OriginHuman,
		Visibility:           VisibilityPublic,
		LocalState:           SendAccepted,
		AcceptanceDocumented: true,
		Remote:               RemoteFacts{AcceptedAt: when},
	}
}

func TestAssumeRejectsStaleVersionWithoutMutating(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	backing := make([]Cycle, len(c.Cycles), len(c.Cycles)+1)
	copy(backing, c.Cycles)
	backing = append(backing, Cycle{Number: 99})
	c.Cycles = backing[:1]
	pending := make([]OutboundMessage, 1, 2)
	pending[0] = OutboundMessage{
		Origin:          OriginBot,
		LocalState:      SendQueued,
		ControllerEpoch: c.ControllerEpoch,
	}

	got, gotPending, decision, err := Assume(c, pending, AssumeCommand{
		ExpectedVersion: c.Version - 1,
		AssigneeID:      agentB,
		ActorID:         agentB,
		At:              at(2),
	})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("erro: %v", err)
	}
	if got.Version != c.Version || got.ControllerEpoch != c.ControllerEpoch || got.AssigneeID != agentA {
		t.Fatalf("estado devolvido mudou: %+v", got)
	}
	if len(decision.Events) != 0 || decision.CancelAutomaticQueued {
		t.Fatal("decisão de versão obsoleta não é vazia")
	}
	if c.AssigneeID != agentA || c.Cycles[0].Number != 1 || backing[1].Number != 99 {
		t.Fatal("entrada foi mutada")
	}
	if pending[0].LocalState != SendQueued || gotPending[0].LocalState != SendQueued {
		t.Fatal("pendência foi mutada na versão obsoleta")
	}
}

func TestAssumeChangesEpochOnlyWhenAuthorityChanges(t *testing.T) {
	c, err := New(Draft{
		ID:         uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		TenantID:   tenantID,
		InboxID:    inboxID,
		ContactID:  contactID,
		OpenedAt:   at(0),
		Status:     StatusTriage,
		Controller: ControllerBot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeBotSend(c, c.ControllerEpoch); err != nil {
		t.Fatal(err)
	}
	botEpoch := c.ControllerEpoch
	queued := OutboundMessage{Origin: OriginBot, LocalState: SendQueued, ControllerEpoch: botEpoch}
	inFlight := OutboundMessage{Origin: OriginBot, LocalState: SendSending, ControllerEpoch: botEpoch}
	humanQueued := OutboundMessage{Origin: OriginHuman, LocalState: SendQueued, ControllerEpoch: botEpoch}

	taken, pending, decision, err := Assume(c, []OutboundMessage{queued, inFlight, humanQueued}, AssumeCommand{
		ExpectedVersion: c.Version,
		AssigneeID:      agentA,
		ActorID:         agentA,
		At:              at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if taken.Controller != ControllerHuman || taken.AssigneeID != agentA || taken.Status != StatusOpen {
		t.Fatalf("posse: %+v", taken)
	}
	if taken.ControllerEpoch == botEpoch || taken.Version == c.Version {
		t.Fatal("assumir do bot não avançou versão e epoch")
	}
	if !decision.CancelAutomaticQueued || decision.CancelAutomaticQueuedEpoch != botEpoch {
		t.Fatalf("cancelamento: %+v", decision)
	}
	if decision.Events[0].Kind != EventAssumed || decision.Events[0].ActorID != agentA {
		t.Fatalf("evento: %+v", decision.Events)
	}
	if pending[0].LocalState != SendCancelled || pending[1].LocalState != SendSending || pending[2].LocalState != SendQueued {
		t.Fatalf("pendências: %+v", pending)
	}
	if queued.LocalState != SendQueued {
		t.Fatal("a fatia de entrada foi mutada")
	}
	if err := AuthorizeBotSend(taken, botEpoch); err == nil {
		t.Fatal("epoch antiga ainda autoriza o bot")
	}

	same, _, sameDecision, err := Assume(taken, nil, AssumeCommand{
		ExpectedVersion: taken.Version,
		AssigneeID:      agentA,
		ActorID:         agentA,
		At:              at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if same.Version != taken.Version || same.ControllerEpoch != taken.ControllerEpoch || len(sameDecision.Events) != 0 {
		t.Fatal("assumir de novo pelo mesmo humano alterou autoridade")
	}

	other, _, otherDecision, err := Assume(taken, []OutboundMessage{{
		Origin: OriginBot, LocalState: SendQueued, ControllerEpoch: taken.ControllerEpoch,
	}}, AssumeCommand{
		ExpectedVersion: taken.Version,
		AssigneeID:      agentB,
		ActorID:         agentB,
		At:              at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.AssigneeID != agentB || other.Version != taken.Version+1 {
		t.Fatal("troca de responsável não usou a versão")
	}
	if other.ControllerEpoch != taken.ControllerEpoch || otherDecision.CancelAutomaticQueued {
		t.Fatal("troca entre humanos alterou a autoridade de envio")
	}
	stale, _, _, err := Assume(other, nil, AssumeCommand{
		ExpectedVersion: taken.Version,
		AssigneeID:      agentA,
		ActorID:         agentA,
		At:              at(5),
	})
	if !errors.Is(err, ErrStaleVersion) || stale.AssigneeID != agentB {
		t.Fatalf("versão obsoleta: %v %+v", err, stale)
	}

	back, _, _, err := GrantController(other, nil, ControllerCommand{
		ExpectedVersion: other.Version,
		Controller:      ControllerBot,
		ActorID:         actorID,
		At:              at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if back.ControllerEpoch == other.ControllerEpoch || back.Controller != ControllerBot {
		t.Fatal("retorno ao bot não gerou epoch nova")
	}
	if err := AuthorizeBotSend(back, botEpoch); err == nil {
		t.Fatal("epoch anterior ao humano voltou a valer")
	}
	if err := AuthorizeBotSend(back, back.ControllerEpoch); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRequiresOutcomeAndDoesNotPromiseExternalResolution(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	withRef, err := AttachExternalProtocol(c, c.Version, "ERP-9")
	if err != nil {
		t.Fatal(err)
	}
	if withRef.ControllerEpoch != c.ControllerEpoch {
		t.Fatal("referência externa alterou a autoridade de envio")
	}
	base := CloseCommand{
		ExpectedVersion: withRef.Version,
		ActorID:         agentA,
		ActorKind:       ControllerHuman,
		Reason:          "financeiro",
		Result:          CloseReferredExternal,
		Summary:         "cliente seguiu no ERP",
		At:              at(10),
	}
	cases := []CloseCommand{
		{ExpectedVersion: base.ExpectedVersion, ActorID: base.ActorID, ActorKind: ControllerHuman, Result: CloseResolved, Summary: "s", At: at(10)},
		{ExpectedVersion: base.ExpectedVersion, ActorID: base.ActorID, ActorKind: ControllerHuman, Reason: "m", Summary: "s", At: at(10)},
		{ExpectedVersion: base.ExpectedVersion, ActorID: base.ActorID, ActorKind: ControllerHuman, Reason: "m", Result: CloseResolved, At: at(10)},
		{ExpectedVersion: base.ExpectedVersion, ActorKind: ControllerHuman, Reason: "m", Result: CloseResolved, Summary: "s", At: at(10)},
	}
	for i, cmd := range cases {
		got, _, _, err := Close(withRef, nil, cmd)
		if !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("caso %d: %v", i, err)
		}
		if got.Status != withRef.Status || got.Version != withRef.Version {
			t.Fatalf("caso %d mutou o estado", i)
		}
	}
	unknown := base
	unknown.Result = "solved"
	if _, _, _, err := Close(withRef, nil, unknown); !errors.Is(err, ErrInvalidCloseResult) {
		t.Fatal(err)
	}
	stale := base
	stale.ExpectedVersion = 0
	if _, _, _, err := Close(withRef, nil, stale); !errors.Is(err, ErrStaleVersion) {
		t.Fatal(err)
	}

	pending := []OutboundMessage{{Origin: OriginBot, LocalState: SendQueued, ControllerEpoch: withRef.ControllerEpoch}}
	closed, out, decision, err := Close(withRef, pending, base)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != StatusClosed || closed.Version != withRef.Version+1 || closed.ControllerEpoch == withRef.ControllerEpoch {
		t.Fatalf("encerramento: %+v", closed)
	}
	closure := closed.Cycles[0].Closure
	if closure.Reason != "financeiro" || closure.Summary != "cliente seguiu no ERP" || closure.AuthorID != agentA {
		t.Fatalf("autoria: %+v", closure)
	}
	if closure.Result != CloseReferredExternal || closure.ExternalReference != "ERP-9" {
		t.Fatalf("referência: %+v", closure)
	}
	if closure.PromisesExternalResolution() || closed.PromisesExternalResolution() || closed.ConfirmedSolution() {
		t.Fatal("referred_external prometeu solução no ERP")
	}
	if closed.ExternalProtocol != "ERP-9" {
		t.Fatal("protocolo externo foi substituído")
	}
	if decision.Events[0].Kind != EventClosed || decision.Events[0].ActorID != agentA {
		t.Fatal("evento de encerramento ausente")
	}
	if pending[0].LocalState != SendQueued || out[0].LocalState != SendCancelled {
		t.Fatal("cancelamento na entrada ou ausência na saída")
	}
	if _, err := AttachExternalProtocol(closed, closed.Version, "ERP-10"); !errors.Is(err, ErrProtocolImmutable) {
		t.Fatal(err)
	}

	noReply := base
	noReply.ExpectedVersion = withRef.Version
	noReply.Result = CloseNoResponse
	noReply.Summary = "encerrado sem retorno"
	silent, _, _, err := Close(withRef, nil, noReply)
	if err != nil {
		t.Fatal(err)
	}
	if silent.ConfirmedSolution() || silent.Cycles[0].Closure.Result.ConfirmedSolution() {
		t.Fatal("no_response contou como solução confirmada")
	}
	resolvedCmd := noReply
	resolvedCmd.Result = CloseResolved
	resolved, _, _, err := Close(withRef, nil, resolvedCmd)
	if err != nil || !resolved.ConfirmedSolution() {
		t.Fatal(err)
	}
}

func TestBotCloseRequiresScopeAndCurrentEpoch(t *testing.T) {
	c, err := New(Draft{
		ID: uuid.MustParse("44444444-4444-4444-8444-444444444444"), TenantID: tenantID, InboxID: inboxID, ContactID: contactID,
		OpenedAt: at(0), Status: StatusTriage, Controller: ControllerBot,
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := CloseCommand{
		ExpectedVersion: c.Version,
		ExpectedEpoch:   c.ControllerEpoch,
		ActorID:         actorID,
		ActorKind:       ControllerBot,
		Reason:          "spam",
		Result:          CloseSpam,
		Summary:         "abuso",
		At:              at(3),
	}
	if _, _, _, err := Close(c, nil, cmd); !errors.Is(err, ErrBotScope) {
		t.Fatal(err)
	}
	cmd.BotScope = true
	cmd.ExpectedEpoch = c.ControllerEpoch + 8
	if _, _, _, err := Close(c, nil, cmd); !errors.Is(err, ErrStaleEpoch) {
		t.Fatal(err)
	}
	cmd.ExpectedEpoch = c.ControllerEpoch
	closed, _, _, err := Close(c, nil, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Cycles[0].Closure.AuthorKind != ControllerBot || closed.ConfirmedSolution() {
		t.Fatal("o bot foi reescrito como solução humana")
	}
}

func TestReopenKeepsProtocolAndOpensCycle(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	numbered, err := AssignProtocol(c, c.Version, "PRT-100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AssignProtocol(numbered, numbered.Version, "PRT-200"); !errors.Is(err, ErrProtocolImmutable) {
		t.Fatal(err)
	}
	closed, _, _, err := Close(numbered, nil, CloseCommand{
		ExpectedVersion: numbered.Version,
		ActorID:         agentA,
		ActorKind:       ControllerHuman,
		Reason:          "lentidão",
		Result:          CloseResolved,
		Summary:         "link restabelecido",
		At:              at(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	conflict, decision, err := Reopen(closed, ReopenCommand{
		ExpectedVersion:   closed.Version,
		ActorID:           agentA,
		At:                at(20),
		OtherActiveExists: true,
	})
	if !errors.Is(err, ErrActiveConversation) || conflict.Protocol != "PRT-100" || conflict.Status != StatusClosed {
		t.Fatalf("conflito: %v %+v", err, conflict)
	}
	if len(decision.Events) != 0 || len(conflict.Cycles) != 1 {
		t.Fatal("conflito abriu ciclo")
	}

	reopened, decision, err := Reopen(closed, ReopenCommand{
		ExpectedVersion: closed.Version,
		ActorID:         agentA,
		At:              at(21),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Protocol != "PRT-100" || len(reopened.Cycles) != 2 || reopened.Cycles[1].Number != 2 {
		t.Fatalf("ciclo: %+v", reopened)
	}
	if reopened.Status != StatusOpen || reopened.AssigneeID != uuid.Nil || reopened.Controller != ControllerMenu {
		t.Fatalf("fila: %+v", reopened)
	}
	if reopened.ControllerEpoch == closed.ControllerEpoch || !decision.Reopened {
		t.Fatal("reabertura não invalidou a autoridade anterior")
	}
	if reopened.Cycles[0].Closure.Summary != "link restabelecido" || reopened.Cycles[0].ClosedAt.IsZero() {
		t.Fatal("o ciclo encerrado foi reescrito")
	}
	if decision.Events[0].CycleNumber != 2 || decision.Events[0].ActorID != agentA {
		t.Fatalf("evento: %+v", decision.Events[0])
	}
	again, _, err := Reopen(reopened, ReopenCommand{ExpectedVersion: reopened.Version, ActorID: agentA, At: at(22)})
	if !errors.Is(err, ErrInvalidTransition) || again.Protocol != "PRT-100" || len(again.Cycles) != 2 {
		t.Fatal("reabertura de conversa aberta")
	}
}

func TestProtocolIsImmutableAndNotCountedInMemory(t *testing.T) {
	first := newOpen(t)
	second := newOpen(t)
	second.ID = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	if first.Protocol != "" || second.Protocol != "" {
		t.Fatal("a conversa nasceu com contador próprio")
	}
	a, err := AssignProtocol(first, first.Version, "  PRT-7  ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := AssignProtocol(second, second.Version, "PRT-7")
	if err != nil {
		t.Fatal(err)
	}
	if a.Protocol != "PRT-7" || b.Protocol != "PRT-7" || a.ControllerEpoch != first.ControllerEpoch {
		t.Fatalf("protocolo: %q %q epoch %d", a.Protocol, b.Protocol, a.ControllerEpoch)
	}
	same, err := AssignProtocol(a, a.Version-1, "PRT-7")
	if err != nil || same.Version != a.Version {
		t.Fatal("repetir o mesmo protocolo não é idempotente")
	}
	notice, err := RecordProtocolNotice(a, ProtocolNotice{ExpectedVersion: a.Version, At: at(2), Delivered: false})
	if err != nil {
		t.Fatal(err)
	}
	if notice.ProtocolCommunicated || notice.Protocol != "PRT-7" || notice.ControllerEpoch != a.ControllerEpoch {
		t.Fatal("falha ao informar o protocolo virou sucesso ou mudou autoridade")
	}
	told, err := RecordProtocolNotice(notice, ProtocolNotice{
		ExpectedVersion: notice.Version, At: at(3), Delivered: true, EvidenceRef: "msg-1",
	})
	if err != nil || !told.ProtocolCommunicated || told.Protocol != "PRT-7" || told.ControllerEpoch != a.ControllerEpoch {
		t.Fatal(err)
	}
	if _, err := RecordProtocolNotice(a, ProtocolNotice{ExpectedVersion: a.Version}); !errors.Is(err, ErrInvalidState) {
		t.Fatal("aviso sem hora foi aceito")
	}
}

func TestCustomerMessageEndsWaitAndLateEventDoesNotRewind(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	withCustomer, decision, err := ReceiveCustomerMessage(c, InboundMessage{
		ExpectedVersion: c.Version,
		TrustedAt:       at(4),
		IngestedAt:      at(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !withCustomer.LastCustomerMessageAt.Equal(at(4)) || decision.WaitEnded {
		t.Fatalf("relógio: %s", withCustomer.LastCustomerMessageAt)
	}

	waiting, decision, err := ApplySendResult(withCustomer, withCustomer.Version, humanAccepted(at(5)))
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != StatusWaitingCustomer || !waiting.WaitingSince.Equal(at(5)) || !decision.WaitStarted {
		t.Fatalf("espera: %+v", waiting)
	}
	if len(waiting.Cycles[0].Waits) != 1 || len(withCustomer.Cycles[0].Waits) != 0 {
		t.Fatal("a conversa de entrada ganhou intervalo de espera")
	}

	late, lateDecision, err := ReceiveCustomerMessage(waiting, InboundMessage{
		ExpectedVersion: waiting.Version,
		TrustedAt:       at(4),
		IngestedAt:      at(40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lateDecision.IgnoredLate || late.Status != StatusWaitingCustomer || !late.LastCustomerMessageAt.Equal(at(4)) {
		t.Fatal("evento atrasado retrocedeu relógio ou espera")
	}
	historical, historicalDecision, err := ReceiveCustomerMessage(waiting, InboundMessage{
		ExpectedVersion: waiting.Version - 1,
		TrustedAt:       at(50),
		Historical:      true,
	})
	if err != nil || !historicalDecision.IgnoredHistorical || historical.Status != StatusWaitingCustomer || historical.Version != waiting.Version {
		t.Fatal("histórico reiniciou a espera")
	}

	current, currentDecision, err := ReceiveCustomerMessage(waiting, InboundMessage{
		ExpectedVersion: waiting.Version,
		TrustedAt:       at(6),
		IngestedAt:      at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusOpen || !currentDecision.WaitEnded || !current.LastCustomerMessageAt.Equal(at(6)) {
		t.Fatalf("mensagem atual: %+v", current)
	}
	if current.Cycles[0].Waits[0].EndedAt.IsZero() || current.Cycles[0].Waits[0].StartedAt.After(current.Cycles[0].Waits[0].EndedAt) {
		t.Fatal("intervalo de espera incoerente")
	}
	if !waiting.Cycles[0].Waits[0].EndedAt.IsZero() {
		t.Fatal("encerrou a espera na entrada")
	}

	stale, _, err := ApplySendResult(current, current.Version-1, humanAccepted(at(7)))
	if !errors.Is(err, ErrStaleVersion) || stale.Status != StatusOpen {
		t.Fatalf("versão obsoleta iniciou espera: %v %s", err, stale.Status)
	}
}

func TestWaitingRequiresAcceptedHumanReplyWithoutLaterCustomer(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	blocked := []OutboundMessage{
		{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendQueued},
		{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendUncertain},
		{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendFailed},
		{Origin: OriginNote, Visibility: VisibilityInternal, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: at(5)}},
		{Origin: OriginBot, Visibility: VisibilityPublic, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: at(5)}},
		{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendSending, Remote: RemoteFacts{DeliveredAt: at(5)}},
	}
	for i, message := range blocked {
		got, decision, err := ApplySendResult(c, c.Version, message)
		if err != nil || got.Status != StatusOpen || got.Version != c.Version || decision.WaitStarted {
			t.Fatalf("caso %d passou a aguardando: %v %s", i, err, got.Status)
		}
	}
	later := c
	later.LastCustomerMessageAt = at(9)
	got, _, err := ApplySendResult(later, later.Version, humanAccepted(at(8)))
	if err != nil || got.Status != StatusOpen {
		t.Fatal("resposta anterior à mensagem do cliente iniciou espera")
	}
	ok, decision, err := ApplySendResult(c, c.Version, humanAccepted(at(8)))
	if err != nil || ok.Status != StatusWaitingCustomer || !decision.WaitStarted {
		t.Fatal(err)
	}
	replay, replayDecision, err := ApplySendResult(ok, ok.Version, humanAccepted(at(8)))
	if err != nil || !replayDecision.IgnoredLate || !replay.WaitingSince.Equal(at(8)) || replay.Version != ok.Version {
		t.Fatal("aceite atrasado moveu a âncora para trás")
	}
}

func TestInboundReopenKeepsProtocolUntilWindowEnds(t *testing.T) {
	c := assume(t, newOpen(t), agentA)
	numbered, err := AssignProtocol(c, c.Version, "PRT-100")
	if err != nil {
		t.Fatal(err)
	}
	closed, _, _, err := Close(numbered, nil, CloseCommand{
		ExpectedVersion: numbered.Version,
		ActorID:         agentA,
		ActorKind:       ControllerHuman,
		Reason:          "financeiro",
		Result:          CloseReferredExternal,
		Summary:         "segue no ERP",
		At:              at(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	outside, decision, err := ReceiveCustomerMessage(closed, InboundMessage{
		ExpectedVersion:    closed.Version,
		TrustedAt:          at(30),
		WithinReopenWindow: false,
	})
	if err != nil || !decision.NeedsNewConversation || outside.Protocol != "PRT-100" || outside.Status != StatusClosed || len(outside.Cycles) != 1 {
		t.Fatalf("fora da janela: %v %+v", err, outside)
	}
	inside, decision, err := ReceiveCustomerMessage(closed, InboundMessage{
		ExpectedVersion:    closed.Version,
		TrustedAt:          at(12),
		WithinReopenWindow: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Reopened || inside.Protocol != "PRT-100" || len(inside.Cycles) != 2 || inside.Cycles[0].Closure.Result != CloseReferredExternal {
		t.Fatalf("reabertura: %+v", inside)
	}
	if inside.PromisesExternalResolution() {
		t.Fatal("reabrir referred_external prometeu o ERP")
	}
	dup, dupDecision, err := ReceiveCustomerMessage(inside, InboundMessage{Duplicate: true, TrustedAt: at(13)})
	if err != nil || !dupDecision.Duplicate || dup.Version != inside.Version {
		t.Fatal("duplicata reaplicou efeito")
	}
	if _, _, err := ReceiveCustomerMessage(closed, InboundMessage{
		ExpectedVersion: closed.Version - 1, TrustedAt: at(12), WithinReopenWindow: true,
	}); !errors.Is(err, ErrStaleVersion) {
		t.Fatal("reabertura com versão obsoleta")
	}
}
