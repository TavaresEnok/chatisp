package httpapi

import (
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"time"
)

type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type InboxInput struct {
	Credentials
	InboxID uuid.UUID `path:"inbox_id"`
}
type snapshotInput struct {
	InboxInput
	ConversationID string `query:"conversation_id"`
}
type ConversationInput struct {
	InboxInput
	ConversationID uuid.UUID `path:"conversation_id"`
}
type eventsInput struct {
	InboxInput
	After int64 `query:"after"`
}
type versionBody struct {
	ExpectedVersion int64 `json:"expected_version"`
}
type sendBody struct {
	ExpectedVersion int64  `json:"expected_version"`
	ClientRequestID string `json:"client_request_id"`
	Body            string `json:"body"`
}
type closeBody struct {
	ExpectedVersion   int64  `json:"expected_version"`
	ClientRequestID   string `json:"client_request_id"`
	Reason            string `json:"reason"`
	Result            string `json:"result"`
	Summary           string `json:"summary"`
	FarewellBody      string `json:"farewell_body,omitempty"`
	FarewellRequestID string `json:"farewell_request_id,omitempty"`
}
type reopenBody struct {
	ExpectedVersion int64  `json:"expected_version"`
	ClientRequestID string `json:"client_request_id"`
}
type assumeInput struct {
	ConversationInput
	RawBody []byte
}
type sendInput struct {
	ConversationInput
	RawBody []byte
}
type closeInput struct {
	ConversationInput
	RawBody []byte
}
type reopenInput struct {
	ConversationInput
	RawBody []byte
}

type head struct {
	ID                    uuid.UUID `json:"id"`
	Protocol              string    `json:"protocol"`
	Status                string    `json:"status"`
	Version               int64     `json:"version"`
	Controller            string    `json:"controller"`
	ControllerEpoch       int64     `json:"controller_epoch"`
	AssigneeID            uuid.UUID `json:"assignee_id"`
	ContactID             uuid.UUID `json:"contact_id"`
	CycleNumber           int32     `json:"cycle_number"`
	Summary               string    `json:"summary"`
	LastCustomerMessageAt time.Time `json:"last_customer_message_at"`
	WaitingSince          time.Time `json:"waiting_since"`
	CreatedAt             time.Time `json:"created_at"`
}
type message struct {
	ID                uuid.UUID `json:"id"`
	ConversationID    uuid.UUID `json:"conversation_id"`
	ExternalMessageID string    `json:"external_message_id"`
	Direction         string    `json:"direction"`
	Origin            string    `json:"origin"`
	Visibility        string    `json:"visibility"`
	Body              string    `json:"body"`
	LocalState        string    `json:"local_state"`
	TrustedAt         time.Time `json:"trusted_at"`
	IngestedAt        time.Time `json:"ingested_at"`
	Historical        bool      `json:"historical"`
}
type timeline struct {
	ID              uuid.UUID `json:"id"`
	Kind            string    `json:"kind"`
	ActorID         uuid.UUID `json:"actor_id"`
	At              time.Time `json:"at"`
	Version         int64     `json:"version"`
	ControllerEpoch int64     `json:"controller_epoch"`
	CycleNumber     int32     `json:"cycle_number"`
	Controller      string    `json:"controller"`
	AssigneeID      uuid.UUID `json:"assignee_id"`
}
type event struct {
	Seq           int64           `json:"seq"`
	ID            uuid.UUID       `json:"id"`
	Type          string          `json:"type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Version       int64           `json:"version"`
	Payload       json.RawMessage `json:"payload"`
}
type conversation struct {
	ID                       uuid.UUID `json:"id"`
	InboxID                  uuid.UUID `json:"inbox_id"`
	ContactID                uuid.UUID `json:"contact_id"`
	IdentityID               uuid.UUID `json:"identity_id"`
	Status                   string    `json:"status"`
	Controller               string    `json:"controller"`
	ControllerEpoch          int64     `json:"controller_epoch"`
	Version                  int64     `json:"version"`
	AssigneeID               uuid.UUID `json:"assignee_id"`
	Protocol                 string    `json:"protocol"`
	ProtocolPeriod           string    `json:"protocol_period"`
	ProtocolCommunicated     bool      `json:"protocol_communicated"`
	ExternalProtocol         string    `json:"external_protocol"`
	LastCustomerMessageAt    time.Time `json:"last_customer_message_at"`
	LastEligibleHumanReplyAt time.Time `json:"last_eligible_human_reply_at"`
	WaitingSince             time.Time `json:"waiting_since"`
	CycleNumber              int32     `json:"cycle_number"`
	Summary                  string    `json:"summary"`
	CreatedAt                time.Time `json:"created_at"`
}
type closure struct {
	CycleNumber       int32     `json:"cycle_number"`
	Reason            string    `json:"reason"`
	Result            string    `json:"result"`
	Summary           string    `json:"summary"`
	AuthorID          uuid.UUID `json:"author_id"`
	AuthorKind        string    `json:"author_kind"`
	At                time.Time `json:"at"`
	ExternalReference string    `json:"external_reference"`
}
type outbound struct {
	MessageID     uuid.UUID `json:"message_id"`
	AttemptID     uuid.UUID `json:"attempt_id"`
	IdempotencyID uuid.UUID `json:"idempotency_id"`
	State         string    `json:"state"`
	Replayed      bool      `json:"replayed"`
}
type snapshotOutput struct {
	Body struct {
		NextCursor    int64     `json:"next_cursor"`
		Conversations []head    `json:"conversations"`
		Messages      []message `json:"messages"`
	}
}
type historyOutput struct {
	Body struct {
		NextCursor int64      `json:"next_cursor"`
		Head       head       `json:"head"`
		Messages   []message  `json:"messages"`
		Timeline   []timeline `json:"timeline"`
	}
}
type eventsOutput struct {
	Body struct {
		NextCursor int64   `json:"next_cursor"`
		Events     []event `json:"events"`
	}
}
type conversationOutput struct{ Body conversation }
type sendOutput struct{ Body outbound }
type closeResponseBody struct {
	Conversation   conversation `json:"conversation"`
	Closure        closure      `json:"closure"`
	Farewell       *outbound    `json:"farewell"`
	FarewellFailed bool         `json:"farewell_failed"`
}

func (closeResponseBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["farewell"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["farewell"], {Type: "null"}}}
	return s
}

type closeOutput struct {
	Body closeResponseBody
}
