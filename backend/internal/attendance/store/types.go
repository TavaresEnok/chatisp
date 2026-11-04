package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	StatusTriage          = "triage"
	StatusOpen            = "open"
	StatusWaitingCustomer = "waiting_customer"
	StatusClosed          = "closed"

	ControllerMenu  = "menu"
	ControllerBot   = "bot"
	ControllerHuman = "human"

	ResultResolved         = "resolved"
	ResultNoResponse       = "no_response"
	ResultReferredExternal = "referred_external"
	ResultSpam             = "spam"

	QueueIngress  = "ingress"
	QueueInbound  = "inbound"
	QueueOutbound = "outbound"
)

// Repo is stateless. It uses the transaction the caller already opened.
type Repo struct{}

// Conversation is one attendance row, including a placeholder that has no inbox.
type Conversation struct {
	ID                       uuid.UUID
	TenantID                 uuid.UUID
	InboxID                  uuid.UUID
	SectorID                 uuid.UUID
	ContactID                uuid.UUID
	IdentityID               uuid.UUID
	Status                   string
	Controller               string
	ControllerEpoch          int64
	Version                  int64
	AssigneeID               uuid.UUID
	Protocol                 string
	ProtocolPeriod           string
	ProtocolCommunicated     bool
	ExternalProtocol         string
	LastCustomerMessageAt    time.Time
	LastEligibleHumanReplyAt time.Time
	WaitingSince             time.Time
	CycleNumber              int32
	Summary                  string
	CreatedAt                time.Time
}

// OpenInput starts cycle 1 when no active attendance exists for the identity.
type OpenInput struct {
	TenantID   uuid.UUID
	InboxID    uuid.UUID
	ContactID  uuid.UUID
	IdentityID uuid.UUID
	Period     string
	Status     string
	OpenedAt   time.Time
}

// OpenResult reports whether this call inserted the row.
type OpenResult struct {
	Conversation Conversation
	Created      bool
}

// AssumeInput hands the current version to one human membership.
type AssumeInput struct {
	TenantID        uuid.UUID
	ConversationID  uuid.UUID
	MembershipID    uuid.UUID
	ExpectedVersion int64
	At              time.Time
}

// CloseInput ends the open cycle. The farewell outbox is a separate command.
type CloseInput struct {
	TenantID        uuid.UUID
	ConversationID  uuid.UUID
	MembershipID    uuid.UUID
	ExpectedVersion int64
	Reason          string
	Result          string
	Summary         string
	AuthorKind      string
	At              time.Time
}

// Closure is the persisted end of a cycle.
type Closure struct {
	CycleNumber       int32
	Reason            string
	Result            string
	Summary           string
	AuthorID          uuid.UUID
	AuthorKind        string
	At                time.Time
	ExternalReference string
}

// InboundInput is one customer message already authenticated by ingress.
type InboundInput struct {
	TenantID          uuid.UUID
	InboxID           uuid.UUID
	ContactID         uuid.UUID
	IdentityID        uuid.UUID
	ConnectorID       uuid.UUID
	ExternalMessageID string
	Body              string
	Period            string
	TrustedAt         time.Time
	OpenedAt          time.Time
	Historical        bool
}

// InboundResult is the stored message. Duplicate means the external id already existed.
type InboundResult struct {
	Conversation        Conversation
	MessageID           uuid.UUID
	Duplicate           bool
	CreatedConversation bool
}

// OutboundInput enqueues one text send. Payload is the canonical request bytes.
// The idempotency hash also covers the conversation, the connector and the body.
// The same tuple replays the same message and attempt, including after close.
// A different tuple conflicts. A new key on a closed conversation is ErrClosed.
type OutboundInput struct {
	TenantID        uuid.UUID
	ConversationID  uuid.UUID
	ConnectorID     uuid.UUID
	PrincipalID     uuid.UUID
	Operation       string
	ClientRequestID string
	Payload         []byte
	Body            string
	At              time.Time
	// AllowEmptyBody is only for media whose caption is optional.
	// Text sends leave it false and still reject an empty body.
	AllowEmptyBody bool
}

// OutboundResult is the single message and outbox row for that key.
type OutboundResult struct {
	MessageID     uuid.UUID
	AttemptID     uuid.UUID
	IdempotencyID uuid.UUID
	State         string
	Replayed      bool
}

// AttemptTransition moves one outbox row. Uncertain cannot return to sending.
type AttemptTransition struct {
	TenantID          uuid.UUID
	AttemptID         uuid.UUID
	To                string
	DriverRef         string
	ErrorCode         string
	ErrorMessage      string
	ExternalMessageID string
	DispatchedAt      time.Time
	LeaseVersion      int64
}

// StatusInput is one remote observation. It is not the message row.
type StatusInput struct {
	TenantID          uuid.UUID
	ConnectorID       uuid.UUID
	ExternalMessageID string
	ExternalEventID   string
	Kind              string
	TrustedAt         time.Time
	BillingCategory   string
	Billable          string
	Detail            string
}

// StatusResult is the stored observation. Pending is true until a message exists.
type StatusResult struct {
	ID        uuid.UUID
	MessageID uuid.UUID
	Pending   bool
	Duplicate bool
}

// Message is one persisted message without remote evidence mixed into the body.
type Message struct {
	ID                uuid.UUID
	ConversationID    uuid.UUID
	ExternalMessageID string
	Direction         string
	Origin            string
	Visibility        string
	Body              string
	LocalState        string
	TrustedAt         time.Time
	IngestedAt        time.Time
	Historical        bool
}

// Head is one row of the inbox list.
type Head struct {
	ID                    uuid.UUID
	Protocol              string
	Status                string
	Version               int64
	Controller            string
	ControllerEpoch       int64
	AssigneeID            uuid.UUID
	ContactID             uuid.UUID
	CycleNumber           int32
	Summary               string
	LastCustomerMessageAt time.Time
	WaitingSince          time.Time
	CreatedAt             time.Time
}

// SnapshotInput reads the list and, when set, one conversation history.
type SnapshotInput struct {
	TenantID       uuid.UUID
	InboxID        uuid.UUID
	ConversationID uuid.UUID
	// AuthorizedIDs restricts the list when non-nil; authorization is pinned by the caller.
	AuthorizedIDs []uuid.UUID
	CursorCeiling *int64
}

// Snapshot is a consistent list plus the cursor of the examined range.
type Snapshot struct {
	NextCursor    int64
	Conversations []Head
	Messages      []Message
}

// Event is one committed realtime fact. Payload has ids, not the message body.
type Event struct {
	Seq           int64
	ID            uuid.UUID
	Type          string
	AggregateType string
	AggregateID   uuid.UUID
	Version       int64
	Payload       json.RawMessage
}

// EventPage is the authorized range (authorization is applied by the caller)
// and the cursor of every examined sequence, including ones the caller may hide.
type EventPage struct {
	NextCursor int64
	Events     []Event
}

// JobReservation is one live worker slot.
type JobReservation struct {
	ID        uuid.UUID
	Queue     string
	ExpiresAt time.Time
}

// Lease is the send coordinator for one inbox.
type Lease struct {
	OwnerID      uuid.UUID
	LeaseVersion int64
	ExpiresAt    time.Time
}

// Secret is ciphertext, key version and the associated data. Never plaintext.
type Secret struct {
	KeyVersion string
	Ciphertext []byte
	AAD        string
}

// Route is the trusted app–WABA–number binding for one phone number id.
type Route struct {
	TenantID      uuid.UUID
	InboxID       uuid.UUID
	ConnectorID   uuid.UUID
	PhoneNumberID string
}

// Delivery is the stored raw envelope.
type Delivery struct {
	ID       uuid.UUID
	State    string
	Replayed bool
}

// MetaBinding is the registry written by the tenant, read by ingress.
type MetaBinding struct {
	TenantID uuid.UUID
	AppRowID uuid.UUID
	AppID    string
	Secret   Secret
	// RegistryID and Capability are provided by a trusted ingress reader after
	// authenticating an existing canonical secret. Zero values create a registry.
	RegistryID    uuid.UUID
	Capability    uuid.UUID
	AADScope      string
	AADTenantID   *uuid.UUID
	AADEntity     string
	AADEntityID   uuid.UUID
	AADField      string
	WabaRowID     uuid.UUID
	WabaID        string
	PhoneRowID    uuid.UUID
	PhoneNumberID string
	DisplayNumber string
	ConnectorID   uuid.UUID
	InboxID       uuid.UUID
}
