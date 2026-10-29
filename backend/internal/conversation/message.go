package conversation

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	OriginHuman     Origin = "human"
	OriginBot       Origin = "bot"
	OriginMenu      Origin = "menu"
	OriginNote      Origin = "note"
	OriginReaction  Origin = "reaction"
	OriginTechnical Origin = "technical"
)

const (
	VisibilityPublic   Visibility = "public"
	VisibilityInternal Visibility = "internal"
)

const (
	SendQueued    LocalSendState = "queued"
	SendSending   LocalSendState = "sending"
	SendAccepted  LocalSendState = "accepted"
	SendFailed    LocalSendState = "failed"
	SendRetrying  LocalSendState = "retrying"
	SendUncertain LocalSendState = "uncertain"
	SendCancelled LocalSendState = "cancelled"
)

const (
	EvidenceAccepted  EvidenceKind = "accepted"
	EvidenceDelivered EvidenceKind = "delivered"
	EvidenceRead      EvidenceKind = "read"
	EvidenceFailed    EvidenceKind = "failed"
)

var (
	ErrInvalidSendTransition = errors.New("transição de envio inválida")
	ErrInvalidEvidence       = errors.New("evidência remota inválida")
)

// Origin classifies who produced a timeline item. Notes, reactions, menu and
// bot output are not human public replies.
type Origin string

// Visibility separates a public reply from an internal note.
type Visibility string

// LocalSendState is the local outbox lifecycle. Delivered and read are not
// local states; those facts live on RemoteFacts.
type LocalSendState string

// EvidenceKind is one remote observation. Accepted is not delivered or read.
type EvidenceKind string

// OutboundMessage is one local send intent. Remote evidence is attached to
// it and does not replace the local state.
type OutboundMessage struct {
	ID                   uuid.UUID
	ClientRequestID      string
	Origin               Origin
	Visibility           Visibility
	LocalState           LocalSendState
	Controller           Controller
	ControllerEpoch      int64
	RuleVersion          string
	AcceptanceDocumented bool
	Remote               RemoteFacts
	Evidence             []RemoteEvidence
}

// RemoteFacts keeps each remote clock on its own. A later read does not erase
// acceptance, and an older event does not move a clock backward.
type RemoteFacts struct {
	AcceptedAt  time.Time
	DeliveredAt time.Time
	ReadAt      time.Time
	FailedAt    time.Time
	HighWaterAt time.Time
}

// RemoteEvidence is one driver observation. At is the trusted channel time.
// IngestedAt never advances the remote clocks.
type RemoteEvidence struct {
	Kind       EvidenceKind
	At         time.Time
	IngestedAt time.Time
	ExternalID string
	Documented bool
}

// Accepted reports documented connector acceptance, which is not delivery.
func (f RemoteFacts) Accepted() bool { return !f.AcceptedAt.IsZero() }

// Delivered reports a delivery receipt. It does not imply local acceptance.
func (f RemoteFacts) Delivered() bool { return !f.DeliveredAt.IsZero() }

// Read reports a read receipt.
func (f RemoteFacts) Read() bool { return !f.ReadAt.IsZero() }

// TransitionSend moves the local outbox. Acceptance is applied only from
// documented acceptance evidence. Uncertain does not return to sending:
// a blind resend is a different operation, not a transition.
func TransitionSend(m OutboundMessage, to LocalSendState) (OutboundMessage, error) {
	if m.LocalState == to {
		return m, nil
	}
	if !legalSend(m.LocalState, to) {
		return m, ErrInvalidSendTransition
	}
	next := m
	next.LocalState = to
	return next, nil
}

// RecoverCrashedAttempt classifies a process death during sending.
// Without proof that the external call never started, the attempt is uncertain
// and must not be dispatched again automatically.
func RecoverCrashedAttempt(m OutboundMessage, provedCallDidNotStart bool) (OutboundMessage, error) {
	if m.LocalState != SendSending {
		return m, ErrInvalidSendTransition
	}
	next := m
	if provedCallDidNotStart {
		next.LocalState = SendQueued
		return next, nil
	}
	next.LocalState = SendUncertain
	return next, nil
}

// CancelUnsentAutomatic cancels one queued bot or menu send of the epoch that
// lost authority. A send already in flight is not rewritten as cancelled.
func CancelUnsentAutomatic(m OutboundMessage, epoch int64) (OutboundMessage, bool) {
	if !automaticCancellable(m, epoch) {
		return m, false
	}
	next := m
	next.LocalState = SendCancelled
	return next, true
}

// ApplyRemoteEvidence records a driver fact. Delivered and read do not set
// the local state to accepted and do not fill the acceptance clock. An event
// older than the clock already stored for that fact does not move it backward.
// The input's evidence slice is left untouched, including spare capacity.
func ApplyRemoteEvidence(m OutboundMessage, ev RemoteEvidence) (OutboundMessage, error) {
	if !ev.Kind.valid() || ev.At.IsZero() {
		return m, ErrInvalidEvidence
	}
	if ev.Kind == EvidenceAccepted && !ev.Documented {
		return m, ErrInvalidEvidence
	}
	for _, existing := range m.Evidence {
		if sameEvidence(existing, ev) {
			return m, nil
		}
	}
	next := m
	next.Evidence = make([]RemoteEvidence, len(m.Evidence)+1)
	copy(next.Evidence, m.Evidence)
	next.Evidence[len(m.Evidence)] = ev
	next.Remote.HighWaterAt = forward(m.Remote.HighWaterAt, ev.At)
	switch ev.Kind {
	case EvidenceAccepted:
		next.Remote.AcceptedAt = forward(m.Remote.AcceptedAt, ev.At)
		next.AcceptanceDocumented = true
		if m.LocalState == SendSending || m.LocalState == SendRetrying || m.LocalState == SendUncertain {
			next.LocalState = SendAccepted
		}
	case EvidenceDelivered:
		next.Remote.DeliveredAt = forward(m.Remote.DeliveredAt, ev.At)
	case EvidenceRead:
		next.Remote.ReadAt = forward(m.Remote.ReadAt, ev.At)
	case EvidenceFailed:
		next.Remote.FailedAt = forward(m.Remote.FailedAt, ev.At)
		if m.Remote.AcceptedAt.IsZero() && (m.LocalState == SendSending || m.LocalState == SendRetrying || m.LocalState == SendUncertain) {
			next.LocalState = SendFailed
		}
	default:
		return m, ErrInvalidEvidence
	}
	return next, nil
}

// StartsCustomerWait is the only predicate that allows waiting_customer.
// The reply must be public, human, locally accepted, and backed by documented
// acceptance evidence. A customer message strictly after that acceptance blocks
// the wait. Notes, bot, menu, queued, failed and uncertain never pass.
func StartsCustomerWait(m OutboundMessage, lastCustomerAt time.Time) bool {
	if m.Origin != OriginHuman || m.Visibility != VisibilityPublic {
		return false
	}
	if m.LocalState != SendAccepted || !m.AcceptanceDocumented || m.Remote.AcceptedAt.IsZero() {
		return false
	}
	if !lastCustomerAt.IsZero() && lastCustomerAt.After(m.Remote.AcceptedAt) {
		return false
	}
	return true
}

func legalSend(from, to LocalSendState) bool {
	switch from {
	case SendQueued:
		return to == SendSending || to == SendCancelled || to == SendFailed
	case SendSending:
		return to == SendFailed || to == SendUncertain || to == SendRetrying
	case SendRetrying:
		return to == SendSending || to == SendFailed || to == SendUncertain
	default:
		return false
	}
}

func (k EvidenceKind) valid() bool {
	switch k {
	case EvidenceAccepted, EvidenceDelivered, EvidenceRead, EvidenceFailed:
		return true
	default:
		return false
	}
}

func sameEvidence(a, b RemoteEvidence) bool {
	return a.Kind == b.Kind && a.At.Equal(b.At) && a.ExternalID == b.ExternalID && a.Documented == b.Documented
}

func forward(current, next time.Time) time.Time {
	if next.IsZero() {
		return current
	}
	if current.IsZero() || next.After(current) {
		return next
	}
	return current
}
