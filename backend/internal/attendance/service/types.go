package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
)

const (
	// OpSendText is the idempotency operation for one public text send.
	OpSendText = "send_text"
	// OpClose is the idempotency operation for closing a cycle.
	OpClose = "close_attendance"
	// OpReopen is the idempotency operation for a manual reopen.
	OpReopen = "reopen_attendance"

	payloadVersion = 1
)

// Principal is the authenticated tenant actor. MembershipID is the operational
// enrollment. IsSuperadmin does not grant attendance access by itself.
type Principal struct {
	UserID       uuid.UUID
	MembershipID uuid.UUID
	TenantID     uuid.UUID
	Role         string
	IsSuperadmin bool
}

// Queuer inserts one outbound job on the caller transaction and does not commit it.
// *jobs.Client implements it. A nil or zero job id is a failed send.
type Queuer interface {
	EnqueueOutbound(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID) (int64, error)
}

// SendInput is one human text. ClientRequestID is scoped by tenant, principal
// and OpSendText. Payload bytes are the canonical JSON of the same intention.
type SendInput struct {
	InboxID         uuid.UUID
	ConversationID  uuid.UUID
	ExpectedVersion int64
	ClientRequestID string
	Body            string
	At              time.Time
}

// SendResult is the single message and outbox row for that key.
type SendResult struct {
	store.OutboundResult
	JobID int64
}

// CloseInput ends the open cycle. FarewellBody is optional and uses its own
// client request id. A farewell failure is returned on CloseResult and does
// not roll back the close.
type CloseInput struct {
	InboxID           uuid.UUID
	ConversationID    uuid.UUID
	ExpectedVersion   int64
	ClientRequestID   string
	Reason            string
	Result            string
	Summary           string
	At                time.Time
	FarewellBody      string
	FarewellRequestID string
}

// Farewell is the optional goodbye. JobID is zero when this call replayed a
// row that was already queued. A failed insert leaves both nil and FarewellErr set.
type Farewell struct {
	store.OutboundResult
	JobID int64
}

// CloseResult is the committed close. FarewellErr is set when the optional
// goodbye did not become an outbox row. The cycle stays closed.
type CloseResult struct {
	Conversation store.Conversation
	Closure      store.Closure
	Farewell     *Farewell
	FarewellErr  error
}

// ReopenInput opens the next cycle on a closed attendance and keeps the protocol.
type ReopenInput struct {
	InboxID         uuid.UUID
	ConversationID  uuid.UUID
	ExpectedVersion int64
	ClientRequestID string
	At              time.Time
}

// ReceiveInput is one authenticated customer text. WithinReopenWindow is the
// caller's decision for the configured window; this package does not invent
// its duration. Historical does not move clocks and does not open a cycle.
type ReceiveInput struct {
	InboxID            uuid.UUID
	ContactID          uuid.UUID
	IdentityID         uuid.UUID
	ConnectorID        uuid.UUID
	ExternalMessageID  string
	Body               string
	Period             string
	TrustedAt          time.Time
	OpenedAt           time.Time
	Historical         bool
	WithinReopenWindow bool
}

// ReceiveResult is the stored customer message.
type ReceiveResult struct {
	store.InboundResult
	Reopened bool
}

// TimelineEvent is one authored fact. It is not a panel DTO.
type TimelineEvent struct {
	ID              uuid.UUID
	Kind            string
	ActorID         uuid.UUID
	At              time.Time
	Version         int64
	ControllerEpoch int64
	CycleNumber     int32
	Controller      string
	AssigneeID      uuid.UUID
}

// History is the conversation snapshot plus the timeline.
type History struct {
	NextCursor int64
	Head       store.Head
	Messages   []store.Message
	Timeline   []TimelineEvent
}

// ReserveInput claims the inbox coordinator and one send slot.
// LeaseVersion 0 takes a free or expired lease. A positive version must still
// be the caller's fence. ExpiresAt is the PostgreSQL lease and reservation deadline.
type ReserveInput struct {
	TenantID     uuid.UUID
	InboxID      uuid.UUID
	AttemptID    uuid.UUID
	OwnerID      uuid.UUID
	LeaseVersion int64
	ExpiresAt    time.Time
	At           time.Time
}

// Reservation is the fence stored before any network call.
type Reservation struct {
	ID           uuid.UUID
	OwnerID      uuid.UUID
	LeaseVersion int64
	ExpiresAt    time.Time
	State        string
	Epoch        int64
}

// AttemptResultInput records a dispatch outcome already decided by the worker.
// Accepted requires documented evidence. Uncertain is stored and is not resent.
// OwnerID and LeaseVersion are the fence from ReserveDispatch. Zero is rejected.
// Observation is a remote receipt: lease stays 0, owner stays empty, and the
// stored lease_version is not replaced. It may only accept or fail.
type AttemptResultInput struct {
	TenantID          uuid.UUID
	AttemptID         uuid.UUID
	To                string
	ExternalMessageID string
	DriverRef         string
	ErrorCode         string
	ErrorMessage      string
	AcceptedAt        time.Time
	Documented        bool
	OwnerID           uuid.UUID
	LeaseVersion      int64
	Observation       bool
}

// AttemptResult is the outbox state after the transition.
// IgnoredLate means an old cycle or epoch did not move the current clocks.
type AttemptResult struct {
	State       string
	Waiting     bool
	IgnoredLate bool
}
