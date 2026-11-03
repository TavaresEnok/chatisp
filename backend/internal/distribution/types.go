package distribution

import (
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultWaitAlertSeconds is the normal-queue alert when the tenant has no row.
	DefaultWaitAlertSeconds = 900
	// MaxWaitAlertSeconds is the stored upper bound, seven days.
	MaxWaitAlertSeconds = 604800

	// StatusAssigned means one attendant received the conversation and its slot.
	StatusAssigned = "assigned"
	// StatusQueued means the waiting set was not assigned.
	StatusQueued = "queued"

	// ReasonAssigned is the successful reservation.
	ReasonAssigned = "assigned"
	// ReasonEmpty means the sector inbox has no open unassigned conversation.
	ReasonEmpty = "empty"
	// ReasonNobodyAvailable means no active member is present and assignable.
	ReasonNobodyAvailable = "nobody_available"
	// ReasonCapacityFull means every assignable member is at the vacancy limit.
	ReasonCapacityFull = "capacity_full"
	// ReasonSectorInactive means the sector is missing or inactive.
	ReasonSectorInactive = "sector_inactive"
	// ReasonInboxUnlinked means the sector does not serve that inbox.
	ReasonInboxUnlinked = "inbox_unlinked"
	// ReasonAutoDisabled means the sector keeps its queue and does not auto-assign.
	ReasonAutoDisabled = "auto_disabled"
	// ReasonReady is a queue view whose sector and inbox are linked.
	ReasonReady = "ready"

	priorityUrgent = "urgent"
	priorityNormal = "normal"
)

// Result is the explicit outcome of one Pick. A queued reason leaves every
// waiting conversation unassigned.
type Result struct {
	Status         string
	Reason         string
	ConversationID uuid.UUID
	MembershipID   uuid.UUID
	UserID         uuid.UUID
	Priority       string
}

// Item is one conversation still waiting in the sector inbox.
// Alert is true only for normal priority once the wait reaches the threshold.
type Item struct {
	ConversationID uuid.UUID
	Priority       string
	QueuedAt       time.Time
	Alert          bool
}

// Settings is the tenant wait threshold. Version 0 means the default.
type Settings struct {
	Version          int64
	Configured       bool
	WaitAlertSeconds int
}

// SectorAuto is the per-sector automatic-assignment switch.
// Version 0 and Configured false mean the sector stays enabled.
type SectorAuto struct {
	Enabled    bool
	Version    int64
	Configured bool
}

// QueueView is the ordered waiting set for one sector inbox.
// AutoAssign reports the sector switch. The waiting set stays visible when it is off.
type QueueView struct {
	Reason     string
	AutoAssign bool
	Settings   Settings
	Items      []Item
}
