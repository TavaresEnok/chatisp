// Package conversation is the pure attendance model used before persistence.
// Commands return a new value and a decision. They do not read the clock,
// allocate protocol numbers, or call connectors, the ERP, or the database.
// On error the returned conversation is the value that was passed in.
package conversation

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StatusTriage          Status = "triage"
	StatusOpen            Status = "open"
	StatusWaitingCustomer Status = "waiting_customer"
	StatusClosed          Status = "closed"
)

const (
	ControllerMenu  Controller = "menu"
	ControllerBot   Controller = "bot"
	ControllerHuman Controller = "human"
)

const (
	CloseResolved         CloseResult = "resolved"
	CloseNoResponse       CloseResult = "no_response"
	CloseReferredExternal CloseResult = "referred_external"
	CloseSpam             CloseResult = "spam"
)

const (
	EventAssumed           = "assumed"
	EventControllerChanged = "controller_changed"
	EventClosed            = "closed"
	EventReopened          = "reopened"
	EventWaitStarted       = "wait_started"
	EventWaitEnded         = "wait_ended"
	EventCustomerMessage   = "customer_message"
)

var (
	ErrStaleVersion       = errors.New("versão obsoleta da conversa")
	ErrStaleEpoch         = errors.New("controller_epoch obsoleto")
	ErrClosureIncomplete  = errors.New("encerramento exige motivo, resultado, síntese e autoria")
	ErrInvalidCloseResult = errors.New("resultado de encerramento inválido")
	ErrProtocolImmutable  = errors.New("protocolo imutável")
	ErrActorRequired      = errors.New("autoria obrigatória")
	ErrActiveConversation = errors.New("já existe conversa ativa para o contato nesta caixa")
	ErrInvalidTransition  = errors.New("transição de conversa inválida")
	ErrBotScope           = errors.New("encerramento do bot exige escopo explícito e posse vigente")
	ErrInvalidState       = errors.New("estado de conversa inválido")
)

// Status is the attendance state from A.6. Waiting and closed are reached
// only by the commands in this package, never by a remote receipt alone.
type Status string

// Controller is who may send. It is not the assignee.
type Controller string

// CloseResult is required when a cycle ends. no_response is not a confirmed
// solution. referred_external does not settle an ERP demand.
type CloseResult string

// Conversation is one attendance thread. Protocol is assigned by persistence
// and then immutable. Version gates edits. ControllerEpoch gates send authority
// and changes only when that authority changes or is invalidated.
type Conversation struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	InboxID   uuid.UUID
	ContactID uuid.UUID

	Status          Status
	Version         int64
	Controller      Controller
	ControllerEpoch int64
	AssigneeID      uuid.UUID

	Protocol              string
	ProtocolCommunicated  bool
	ProtocolAttempts      []ProtocolAttempt
	ExternalProtocol      string
	LastCustomerMessageAt time.Time
	// LastEligibleHumanReplyAt is the inactivity anchor. It never moves backward.
	LastEligibleHumanReplyAt time.Time
	WaitingSince             time.Time

	Cycles []Cycle
}

// Cycle is the span from open or reopen until close. Reopen appends a cycle
// and keeps the protocol.
type Cycle struct {
	Number   int
	OpenedAt time.Time
	ClosedAt time.Time
	Closure  Closure
	Waits    []WaitInterval
}

// WaitInterval is a continuous stretch in waiting_customer. The anchor that
// restarts inactivity is Conversation.WaitingSince; the interval itself is
// not split when a newer human reply is accepted while the wait continues.
type WaitInterval struct {
	StartedAt time.Time
	EndedAt   time.Time
}

// Closure is the authored end of a cycle.
type Closure struct {
	Reason            string
	Result            CloseResult
	Summary           string
	AuthorID          uuid.UUID
	AuthorKind        Controller
	At                time.Time
	ExternalReference string
}

// ProtocolAttempt records whether the customer was told the protocol.
// Generating the number is not evidence that it was delivered.
type ProtocolAttempt struct {
	At          time.Time
	Delivered   bool
	EvidenceRef string
}

// Draft is the caller-supplied identity of a new attendance. The protocol
// string, when present, comes from persistence. This package has no counter.
type Draft struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	InboxID               uuid.UUID
	ContactID             uuid.UUID
	OpenedAt              time.Time
	Status                Status
	Controller            Controller
	Protocol              string
	LastCustomerMessageAt time.Time
}

// ControllerCommand transfers send authority.
type ControllerCommand struct {
	ExpectedVersion int64
	Controller      Controller
	AssigneeID      uuid.UUID
	ActorID         uuid.UUID
	At              time.Time
}

// AssumeCommand is a human taking the current version.
type AssumeCommand struct {
	ExpectedVersion int64
	AssigneeID      uuid.UUID
	ActorID         uuid.UUID
	At              time.Time
}

// CloseCommand ends the current cycle. A bot actor must present the epoch it
// was granted and an explicit scope. Farewell delivery is not part of this
// result: a later outbox failure must not undo the local close.
type CloseCommand struct {
	ExpectedVersion int64
	ExpectedEpoch   int64
	ActorID         uuid.UUID
	ActorKind       Controller
	BotScope        bool
	Reason          string
	Result          CloseResult
	Summary         string
	At              time.Time
}

// ReopenCommand opens a new cycle on a closed attendance when no other active
// attendance exists for the same tenant, inbox and contact.
type ReopenCommand struct {
	ExpectedVersion   int64
	ActorID           uuid.UUID
	At                time.Time
	OtherActiveExists bool
}

// InboundMessage is a customer message already authenticated by ingress.
// TrustedAt is the channel time. IngestedAt is not a clock for local rules.
type InboundMessage struct {
	ExpectedVersion    int64
	TrustedAt          time.Time
	IngestedAt         time.Time
	Historical         bool
	Duplicate          bool
	WithinReopenWindow bool
	OtherActiveExists  bool
}

// ProtocolNotice is one attempt to tell the customer the protocol.
type ProtocolNotice struct {
	ExpectedVersion int64
	At              time.Time
	Delivered       bool
	EvidenceRef     string
}

// Decision describes effects the future transaction must persist. It does not
// perform them.
type Decision struct {
	Events                     []TimelineEvent
	CancelAutomaticQueued      bool
	CancelAutomaticQueuedEpoch int64
	WaitStarted                bool
	WaitEnded                  bool
	Reopened                   bool
	IgnoredLate                bool
	IgnoredHistorical          bool
	Duplicate                  bool
	NeedsNewConversation       bool
}

// TimelineEvent is the authored fact shown on the attendance timeline.
type TimelineEvent struct {
	Kind            string
	ActorID         uuid.UUID
	At              time.Time
	Version         int64
	ControllerEpoch int64
	CycleNumber     int
	Controller      Controller
	AssigneeID      uuid.UUID
}

// PromisesExternalResolution reports whether this closure settled an ERP
// demand. Closing the chat never does. referred_external only keeps the
// external reference.
func (Closure) PromisesExternalResolution() bool { return false }

// ConfirmedSolution is true only for resolved. no_response, spam and
// referred_external are not confirmed solutions.
func (r CloseResult) ConfirmedSolution() bool { return r == CloseResolved }

// ConfirmedSolution reads the last cycle. An open attendance is not a solution.
func (c Conversation) ConfirmedSolution() bool {
	if c.Status != StatusClosed || len(c.Cycles) == 0 {
		return false
	}
	last := c.Cycles[len(c.Cycles)-1]
	return !last.ClosedAt.IsZero() && last.Closure.Result.ConfirmedSolution()
}

// PromisesExternalResolution is always false for the attendance itself.
func (Conversation) PromisesExternalResolution() bool { return false }

// New opens cycle 1. Status defaults to the human queue (open) and the
// controller defaults to menu. Waiting and closed are not initial states.
func New(d Draft) (Conversation, error) {
	if d.ID == uuid.Nil || d.TenantID == uuid.Nil || d.InboxID == uuid.Nil || d.ContactID == uuid.Nil {
		return Conversation{}, ErrInvalidState
	}
	if d.OpenedAt.IsZero() {
		return Conversation{}, ErrInvalidState
	}
	status := d.Status
	if status == "" {
		status = StatusOpen
	}
	if status != StatusOpen && status != StatusTriage {
		return Conversation{}, ErrInvalidState
	}
	controller := d.Controller
	if controller == "" {
		controller = ControllerMenu
	}
	if !controller.valid() {
		return Conversation{}, ErrInvalidState
	}
	protocol := strings.TrimSpace(d.Protocol)
	c := Conversation{
		ID:                    d.ID,
		TenantID:              d.TenantID,
		InboxID:               d.InboxID,
		ContactID:             d.ContactID,
		Status:                status,
		Version:               1,
		Controller:            controller,
		ControllerEpoch:       1,
		Protocol:              protocol,
		LastCustomerMessageAt: d.LastCustomerMessageAt,
		Cycles: []Cycle{{
			Number:   1,
			OpenedAt: d.OpenedAt,
		}},
	}
	return c, nil
}

// AssignProtocol stores the number issued by persistence. It does not compute
// the next transactional counter. A protocol already stored cannot change.
func AssignProtocol(c Conversation, expectedVersion int64, protocol string) (Conversation, error) {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return c, ErrInvalidState
	}
	if c.Protocol == protocol {
		return c, nil
	}
	if c.Protocol != "" {
		return c, ErrProtocolImmutable
	}
	if err := checkVersion(c, expectedVersion); err != nil {
		return c, err
	}
	next := c.clone()
	next.Protocol = protocol
	next.Version++
	return next, nil
}

// AttachExternalProtocol stores an additional ERP reference. Empty input does
// not block the attendance. A reference already stored is not replaced.
func AttachExternalProtocol(c Conversation, expectedVersion int64, ref string) (Conversation, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return c, ErrInvalidState
	}
	if c.ExternalProtocol == ref {
		return c, nil
	}
	if c.ExternalProtocol != "" {
		return c, ErrProtocolImmutable
	}
	if err := checkVersion(c, expectedVersion); err != nil {
		return c, err
	}
	next := c.clone()
	next.ExternalProtocol = ref
	next.Version++
	return next, nil
}

// RecordProtocolNotice appends one communication attempt. Failure leaves the
// protocol pending and does not change send authority.
func RecordProtocolNotice(c Conversation, n ProtocolNotice) (Conversation, error) {
	if c.Protocol == "" || n.At.IsZero() {
		return c, ErrInvalidState
	}
	if err := checkVersion(c, n.ExpectedVersion); err != nil {
		return c, err
	}
	next := c.clone()
	next.ProtocolAttempts = append(next.ProtocolAttempts, ProtocolAttempt{
		At:          n.At,
		Delivered:   n.Delivered,
		EvidenceRef: n.EvidenceRef,
	})
	if n.Delivered {
		next.ProtocolCommunicated = true
	}
	next.Version++
	return next, nil
}

// Assume hands the current version to a human. Epoch changes only when the
// controller kind changes. Passing the attendance from one human to another
// updates the assignee and the version, and leaves the epoch in place.
// Queued bot/menu sends of the previous epoch are cancelled in the returned
// slice; messages already dispatched are left in flight.
func Assume(c Conversation, pending []OutboundMessage, cmd AssumeCommand) (Conversation, []OutboundMessage, Decision, error) {
	return GrantController(c, pending, ControllerCommand{
		ExpectedVersion: cmd.ExpectedVersion,
		Controller:      ControllerHuman,
		AssigneeID:      cmd.AssigneeID,
		ActorID:         cmd.ActorID,
		At:              cmd.At,
	})
}

// GrantController moves menu, bot or human authority. A bot that returns
// after a human takeover receives a new epoch, so the epoch it held before
// the takeover no longer authorizes a send.
func GrantController(c Conversation, pending []OutboundMessage, cmd ControllerCommand) (Conversation, []OutboundMessage, Decision, error) {
	if cmd.At.IsZero() || cmd.ActorID == uuid.Nil {
		return c, pending, Decision{}, ErrActorRequired
	}
	if !cmd.Controller.valid() {
		return c, pending, Decision{}, ErrInvalidTransition
	}
	if cmd.Controller == ControllerHuman && cmd.AssigneeID == uuid.Nil {
		return c, pending, Decision{}, ErrActorRequired
	}
	if cmd.Controller != ControllerHuman && cmd.AssigneeID != uuid.Nil {
		return c, pending, Decision{}, ErrInvalidTransition
	}
	if err := checkVersion(c, cmd.ExpectedVersion); err != nil {
		return c, pending, Decision{}, err
	}
	if c.Status == StatusClosed || len(c.Cycles) == 0 {
		return c, pending, Decision{}, ErrInvalidTransition
	}
	kindChange := c.Controller != cmd.Controller
	assigneeChange := cmd.Controller == ControllerHuman && c.AssigneeID != cmd.AssigneeID
	leavesTriage := c.Status == StatusTriage && cmd.Controller == ControllerHuman
	if !kindChange && !assigneeChange && !leavesTriage {
		return c, pending, Decision{}, nil
	}

	next := c.clone()
	prevEpoch := next.ControllerEpoch
	if kindChange {
		next.ControllerEpoch++
	}
	next.Controller = cmd.Controller
	if cmd.Controller == ControllerHuman {
		next.AssigneeID = cmd.AssigneeID
		if next.Status == StatusTriage {
			next.Status = StatusOpen
		}
	} else {
		next.AssigneeID = uuid.Nil
	}
	next.Version++

	out := pending
	decision := Decision{}
	if kindChange {
		out, _ = cancelAutomaticQueued(pending, prevEpoch)
		decision.CancelAutomaticQueued = true
		decision.CancelAutomaticQueuedEpoch = prevEpoch
	}
	kind := EventControllerChanged
	if cmd.Controller == ControllerHuman {
		kind = EventAssumed
	}
	decision.Events = []TimelineEvent{next.event(kind, cmd.ActorID, cmd.At)}
	return next, out, decision, nil
}

// AuthorizeBotSend reports whether a bot reply still holds send authority.
// The check is pure; the caller runs it inside the same exclusion that takes
// the human assumption. An old epoch fails after bot → human → bot.
func AuthorizeBotSend(c Conversation, epoch int64) error {
	if c.Status == StatusClosed || c.Controller != ControllerBot || c.ControllerEpoch != epoch {
		return ErrStaleEpoch
	}
	return nil
}

// Close ends the cycle when reason, result, summary and authorship are all
// present. referred_external copies the external reference and still does not
// promise that the ERP demand was solved. Epoch is invalidated so sends of
// the previous authority do not stay current.
func Close(c Conversation, pending []OutboundMessage, cmd CloseCommand) (Conversation, []OutboundMessage, Decision, error) {
	reason := strings.TrimSpace(cmd.Reason)
	summary := strings.TrimSpace(cmd.Summary)
	if reason == "" || summary == "" || cmd.ActorID == uuid.Nil || cmd.At.IsZero() || !cmd.ActorKind.valid() {
		return c, pending, Decision{}, ErrClosureIncomplete
	}
	if cmd.Result == "" {
		return c, pending, Decision{}, ErrClosureIncomplete
	}
	if !cmd.Result.valid() {
		return c, pending, Decision{}, ErrInvalidCloseResult
	}
	if err := checkVersion(c, cmd.ExpectedVersion); err != nil {
		return c, pending, Decision{}, err
	}
	if (c.Status != StatusOpen && c.Status != StatusWaitingCustomer && c.Status != StatusTriage) || len(c.Cycles) == 0 {
		return c, pending, Decision{}, ErrInvalidTransition
	}
	if cmd.ActorKind == ControllerBot {
		if !cmd.BotScope {
			return c, pending, Decision{}, ErrBotScope
		}
		if c.Controller != ControllerBot || cmd.ExpectedEpoch != c.ControllerEpoch {
			return c, pending, Decision{}, ErrStaleEpoch
		}
	} else if cmd.ActorKind != ControllerHuman {
		return c, pending, Decision{}, ErrInvalidTransition
	}

	next := c.clone()
	prevEpoch := next.ControllerEpoch
	next.ControllerEpoch++
	next.Version++
	next.Status = StatusClosed
	next.WaitingSince = time.Time{}
	idx := len(next.Cycles) - 1
	next.Cycles[idx].ClosedAt = cmd.At
	next.Cycles[idx].Closure = Closure{
		Reason:            reason,
		Result:            cmd.Result,
		Summary:           summary,
		AuthorID:          cmd.ActorID,
		AuthorKind:        cmd.ActorKind,
		At:                cmd.At,
		ExternalReference: c.ExternalProtocol,
	}
	endOpenWait(&next.Cycles[idx], cmd.At)
	out, _ := cancelAutomaticQueued(pending, prevEpoch)
	decision := Decision{
		Events:                     []TimelineEvent{next.event(EventClosed, cmd.ActorID, cmd.At)},
		CancelAutomaticQueued:      true,
		CancelAutomaticQueuedEpoch: prevEpoch,
	}
	return next, out, decision, nil
}

// Reopen keeps the protocol, appends a cycle, and returns the attendance to
// the queue. It does not allocate a new protocol number.
func Reopen(c Conversation, cmd ReopenCommand) (Conversation, Decision, error) {
	if cmd.ActorID == uuid.Nil || cmd.At.IsZero() {
		return c, Decision{}, ErrActorRequired
	}
	if err := checkVersion(c, cmd.ExpectedVersion); err != nil {
		return c, Decision{}, err
	}
	if c.Status != StatusClosed || len(c.Cycles) == 0 || c.Cycles[len(c.Cycles)-1].ClosedAt.IsZero() {
		return c, Decision{}, ErrInvalidTransition
	}
	if cmd.OtherActiveExists {
		return c, Decision{}, ErrActiveConversation
	}
	next, prevEpoch := beginCycle(c, cmd.At)
	decision := Decision{
		Events:                     []TimelineEvent{next.event(EventReopened, cmd.ActorID, cmd.At)},
		Reopened:                   true,
		CancelAutomaticQueued:      true,
		CancelAutomaticQueuedEpoch: prevEpoch,
	}
	return next, decision, nil
}

// ReceiveCustomerMessage applies one current customer message.
// A message while waiting returns the attendance to open and ends the wait.
// A message inside the reopen window opens a cycle and keeps the protocol.
// Outside that window the caller must open another attendance; this value
// keeps its protocol. Historical, duplicate and late events do not move clocks.
func ReceiveCustomerMessage(c Conversation, msg InboundMessage) (Conversation, Decision, error) {
	if msg.Duplicate {
		return c, Decision{Duplicate: true}, nil
	}
	if msg.Historical {
		return c, Decision{IgnoredHistorical: true}, nil
	}
	if err := checkVersion(c, msg.ExpectedVersion); err != nil {
		return c, Decision{}, err
	}
	if msg.TrustedAt.IsZero() || len(c.Cycles) == 0 {
		return c, Decision{}, ErrInvalidState
	}
	if !c.LastCustomerMessageAt.IsZero() && !msg.TrustedAt.After(c.LastCustomerMessageAt) {
		return c, Decision{IgnoredLate: true}, nil
	}
	if c.Status == StatusClosed {
		if msg.OtherActiveExists {
			return c, Decision{}, ErrActiveConversation
		}
		if !msg.WithinReopenWindow {
			return c, Decision{NeedsNewConversation: true}, nil
		}
		next, prevEpoch := beginCycle(c, msg.TrustedAt)
		next.LastCustomerMessageAt = msg.TrustedAt
		decision := Decision{
			Events: []TimelineEvent{
				next.event(EventReopened, uuid.Nil, msg.TrustedAt),
				next.event(EventCustomerMessage, uuid.Nil, msg.TrustedAt),
			},
			Reopened:                   true,
			CancelAutomaticQueued:      true,
			CancelAutomaticQueuedEpoch: prevEpoch,
		}
		return next, decision, nil
	}

	next := c.clone()
	next.LastCustomerMessageAt = msg.TrustedAt
	next.Version++
	decision := Decision{
		Events: []TimelineEvent{next.event(EventCustomerMessage, uuid.Nil, msg.TrustedAt)},
	}
	if c.Status == StatusWaitingCustomer {
		next.Status = StatusOpen
		next.WaitingSince = time.Time{}
		endOpenWait(&next.Cycles[len(next.Cycles)-1], msg.TrustedAt)
		decision.WaitEnded = true
		decision.Events = append(decision.Events, next.event(EventWaitEnded, uuid.Nil, msg.TrustedAt))
	}
	return next, decision, nil
}

// ApplySendResult starts waiting_customer only when m is an accepted public
// human reply with documented acceptance evidence and the customer has not
// written after that acceptance. Queued, uncertain, failed, note and bot
// results leave the attendance where it is. A late acceptance does not move
// the wait anchor backward.
func ApplySendResult(c Conversation, expectedVersion int64, m OutboundMessage) (Conversation, Decision, error) {
	if !StartsCustomerWait(m, c.LastCustomerMessageAt) {
		return c, Decision{}, nil
	}
	if c.Status != StatusOpen && c.Status != StatusWaitingCustomer {
		return c, Decision{}, nil
	}
	acceptedAt := m.Remote.AcceptedAt
	if c.Status == StatusWaitingCustomer && !c.WaitingSince.IsZero() && !acceptedAt.After(c.WaitingSince) {
		return c, Decision{IgnoredLate: true}, nil
	}
	if err := checkVersion(c, expectedVersion); err != nil {
		return c, Decision{}, err
	}
	if len(c.Cycles) == 0 {
		return c, Decision{}, ErrInvalidState
	}

	next := c.clone()
	started := c.Status != StatusWaitingCustomer
	if c.WaitingSince.IsZero() || acceptedAt.After(c.WaitingSince) {
		next.WaitingSince = acceptedAt
	}
	if c.LastEligibleHumanReplyAt.IsZero() || acceptedAt.After(c.LastEligibleHumanReplyAt) {
		next.LastEligibleHumanReplyAt = acceptedAt
	}
	next.Status = StatusWaitingCustomer
	next.Version++
	if started {
		idx := len(next.Cycles) - 1
		next.Cycles[idx].Waits = append(next.Cycles[idx].Waits, WaitInterval{StartedAt: acceptedAt})
	}
	decision := Decision{WaitStarted: started}
	if started {
		decision.Events = []TimelineEvent{next.event(EventWaitStarted, uuid.Nil, acceptedAt)}
	}
	return next, decision, nil
}

func beginCycle(c Conversation, at time.Time) (Conversation, int64) {
	next := c.clone()
	prevEpoch := next.ControllerEpoch
	next.ControllerEpoch++
	next.Version++
	next.Status = StatusOpen
	next.Controller = ControllerMenu
	next.AssigneeID = uuid.Nil
	next.WaitingSince = time.Time{}
	last := next.Cycles[len(next.Cycles)-1]
	next.Cycles = append(next.Cycles, Cycle{
		Number:   last.Number + 1,
		OpenedAt: at,
	})
	return next, prevEpoch
}

func endOpenWait(cycle *Cycle, at time.Time) {
	n := len(cycle.Waits)
	if n == 0 || !cycle.Waits[n-1].EndedAt.IsZero() {
		return
	}
	ended := at
	if ended.Before(cycle.Waits[n-1].StartedAt) {
		ended = cycle.Waits[n-1].StartedAt
	}
	cycle.Waits[n-1].EndedAt = ended
}

func (c Conversation) event(kind string, actor uuid.UUID, at time.Time) TimelineEvent {
	number := 0
	if n := len(c.Cycles); n > 0 {
		number = c.Cycles[n-1].Number
	}
	return TimelineEvent{
		Kind:            kind,
		ActorID:         actor,
		At:              at,
		Version:         c.Version,
		ControllerEpoch: c.ControllerEpoch,
		CycleNumber:     number,
		Controller:      c.Controller,
		AssigneeID:      c.AssigneeID,
	}
}

func (c Conversation) clone() Conversation {
	next := c
	if len(c.Cycles) > 0 {
		next.Cycles = make([]Cycle, len(c.Cycles))
		for i := range c.Cycles {
			next.Cycles[i] = c.Cycles[i]
			if len(c.Cycles[i].Waits) > 0 {
				next.Cycles[i].Waits = append([]WaitInterval(nil), c.Cycles[i].Waits...)
			}
		}
	}
	if len(c.ProtocolAttempts) > 0 {
		next.ProtocolAttempts = append([]ProtocolAttempt(nil), c.ProtocolAttempts...)
	}
	return next
}

func checkVersion(c Conversation, expected int64) error {
	if expected != c.Version {
		return ErrStaleVersion
	}
	return nil
}

func (k Controller) valid() bool {
	switch k {
	case ControllerMenu, ControllerBot, ControllerHuman:
		return true
	default:
		return false
	}
}

func (r CloseResult) valid() bool {
	switch r {
	case CloseResolved, CloseNoResponse, CloseReferredExternal, CloseSpam:
		return true
	default:
		return false
	}
}

func cancelAutomaticQueued(pending []OutboundMessage, epoch int64) ([]OutboundMessage, bool) {
	changed := false
	for i := range pending {
		if automaticCancellable(pending[i], epoch) {
			changed = true
			break
		}
	}
	if !changed {
		return pending, false
	}
	out := make([]OutboundMessage, len(pending))
	copy(out, pending)
	for i := range out {
		if automaticCancellable(out[i], epoch) {
			out[i].LocalState = SendCancelled
		}
	}
	return out, true
}

func automaticCancellable(m OutboundMessage, epoch int64) bool {
	return m.ControllerEpoch == epoch && m.LocalState == SendQueued && (m.Origin == OriginBot || m.Origin == OriginMenu)
}
