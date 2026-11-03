package lifecycle

import (
	"time"

	"chatisp/internal/sectors"
	"github.com/google/uuid"
)

const (
	// DefaultWarningAfter is the grace before the inactivity warning when the tenant has no row.
	DefaultWarningAfter = 15 * time.Minute
	// DefaultCloseAfter is the grace after a confirmed warning before close.
	DefaultCloseAfter = 15 * time.Minute
	// MinDelay and MaxDelay bound both inactivity timers (one minute to seven days).
	MinDelay = 60 * time.Second
	MaxDelay = 7 * 24 * time.Hour

	// WarningConfirmed records a real delivery of the public warning for one generation.
	// Only this outcome may arm the close deadline. It is not a conversation status.
	WarningConfirmed = "confirmed"
	// WarningUncertain means delivery failed or was not confirmed. Close must not run.
	WarningUncertain = "uncertain"
	// WarningBody is the public text stored on the durable warning request.
	WarningBody = "Seguimos à disposição neste atendimento."

	stateActive            = "active"
	stateReturned          = "returned"
	stateQueued            = "queued"
	stateClearedByCustomer = "cleared_by_customer"
	resolutionAssigned     = "assigned"
	resolutionQueued       = "queued"
	resolutionAlready      = "already_assigned"
	resolutionClosed       = "closed"
	watchWaiting           = "waiting"
	watchRequested         = "requested"
	watchSent              = "sent"
	watchUncertain         = "uncertain"
	watchCancelled         = "cancelled"
	requestPending         = "pending"
	requestConfirmed       = "confirmed"
	requestUncertain       = "uncertain"
	operationSnooze        = "snooze"
	operationInactivity    = "inactivity_settings"
	closeReason            = "sem resposta"
	closeSummary           = "Encerrado sem retorno do cliente."
)

// Principal is the panel actor. Tenant and membership come from the session.
type Principal = sectors.Principal

// SnoozeCommand asks to defer one open human conversation until a clock instant.
type SnoozeCommand struct {
	RequestID       uuid.UUID `json:"request_id"`
	InboxID         uuid.UUID `json:"inbox_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Until           time.Time `json:"until"`
	Reason          string    `json:"reason"`
}

// Snooze is the active deferral. AssigneeID is null while the vacancy is free.
type Snooze struct {
	ID                      uuid.UUID  `json:"id"`
	ConversationID          uuid.UUID  `json:"conversation_id"`
	Until                   time.Time  `json:"snoozed_until"`
	Reason                  string     `json:"reason"`
	DestinationMembershipID uuid.UUID  `json:"destination_membership_id"`
	DestinationSectorID     uuid.UUID  `json:"destination_sector_id"`
	State                   string     `json:"state"`
	ConversationStatus      string     `json:"conversation_status"`
	AssigneeID              *uuid.UUID `json:"assignee_id"`
	Version                 int64      `json:"version"`
}

// InactivityCommand replaces the tenant warning and close delays.
type InactivityCommand struct {
	RequestID           uuid.UUID `json:"request_id"`
	ExpectedVersion     int64     `json:"expected_version"`
	WarningAfterSeconds int       `json:"warning_after_seconds"`
	CloseAfterSeconds   int       `json:"close_after_seconds"`
}

// InactivitySettings is the tenant pair of delays. Version 0 means the defaults.
type InactivitySettings struct {
	Version             int64 `json:"version"`
	Configured          bool  `json:"configured"`
	WarningAfterSeconds int   `json:"warning_after_seconds"`
	CloseAfterSeconds   int   `json:"close_after_seconds"`
}

// Release is one snooze that left the active set. Status stays the conversation status.
type Release struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Cleared        bool      `json:"cleared"`
	Assigned       bool      `json:"assigned"`
	MembershipID   uuid.UUID `json:"membership_id"`
	Resolution     string    `json:"resolution"`
	Status         string    `json:"status"`
}

// WarningReceipt correlates one delivery result to the generation that requested it.
// Outcome is WarningConfirmed or WarningUncertain. A stale generation does not confirm another.
type WarningReceipt struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Generation     int64     `json:"generation"`
	Outcome        string    `json:"outcome"`
}

// Effect is one inactivity transition. Action "closed" is the only close.
type Effect struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Action         string    `json:"action"`
	Generation     int64     `json:"generation"`
}
