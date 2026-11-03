package presence

import (
	"time"

	"chatisp/internal/platform/auth"
	"github.com/google/uuid"
)

const (
	// StateAvailable is a live attendant who is not manually busy.
	StateAvailable = "available"
	// StateBusy is a live attendant whose manual state is busy.
	StateBusy = "busy"
	// StateOffline is the aggregate when no tab is live, or the actor cannot work.
	StateOffline = "offline"
	// ManualAvailable is the default manual state.
	ManualAvailable = "available"
	// ManualBusy prevails over available while the attendant is connected.
	ManualBusy = "busy"
	// HeartbeatTTL is the life of one tab heartbeat. Tests inject the clock.
	HeartbeatTTL = 30 * time.Second
	// DefaultOfflineTolerance is used until the tenant stores a value.
	DefaultOfflineTolerance = 5 * time.Minute
	// MaxOfflineTolerance is the upper bound stored per tenant (24h).
	MaxOfflineTolerance = 24 * time.Hour
)

// Principal is the authenticated panel actor. Tenant and membership come from the session.
type Principal = auth.Principal

// Snapshot is the aggregate for one user. OfflineForUnassign is informational.
type Snapshot struct {
	UserID             uuid.UUID  `json:"user_id"`
	MembershipID       uuid.UUID  `json:"membership_id"`
	State              string     `json:"state"`
	Manual             string     `json:"manual"`
	Connected          bool       `json:"connected"`
	TabCount           int        `json:"tab_count"`
	LastHeartbeat      *time.Time `json:"last_heartbeat"`
	ConnectedUntil     *time.Time `json:"connected_until"`
	OfflineSince       *time.Time `json:"offline_since"`
	ToleranceSeconds   int        `json:"tolerance_seconds"`
	OfflineForUnassign bool       `json:"offline_for_unassign"`
	Assignable         bool       `json:"assignable"`
}

// ManualCommand sets available or busy for the caller.
type ManualCommand struct {
	RequestID uuid.UUID `json:"request_id"`
	State     string    `json:"state"`
}

// ToleranceCommand replaces the tenant offline grace used only as a signal.
type ToleranceCommand struct {
	RequestID               uuid.UUID `json:"request_id"`
	ExpectedVersion         int64     `json:"expected_version"`
	OfflineToleranceSeconds int       `json:"offline_tolerance_seconds"`
}

// Settings is the tenant grace. Version 0 and Configured false mean the default.
type Settings struct {
	Version                 int64 `json:"version"`
	Configured              bool  `json:"configured"`
	OfflineToleranceSeconds int   `json:"offline_tolerance_seconds"`
}
