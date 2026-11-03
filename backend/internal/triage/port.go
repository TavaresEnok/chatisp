package triage

import (
	"context"
	"time"

	"chatisp/internal/distribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// ReasonReady is distribution.Queue's view of a linked sector.
	ReasonReady = distribution.ReasonReady
	// ReasonNobodyAvailable is reported by a port when no attendant can take the queue.
	ReasonNobodyAvailable = distribution.ReasonNobodyAvailable
	// ReasonCapacityFull means attendants exist and every vacancy is taken.
	ReasonCapacityFull = distribution.ReasonCapacityFull
	// ReasonEmpty means the waiting set has no conversation.
	ReasonEmpty = distribution.ReasonEmpty
	// ReasonSectorInactive means the sector is missing or inactive.
	ReasonSectorInactive = distribution.ReasonSectorInactive
	// ReasonInboxUnlinked means the sector does not serve the inbox.
	ReasonInboxUnlinked = distribution.ReasonInboxUnlinked
	// ReasonAutoDisabled means automatic assignment is off. The human queue still exists.
	ReasonAutoDisabled = distribution.ReasonAutoDisabled
)

// Snapshot is the queue fact Decide may turn into a declarative action.
// Reason nobody_available emits KindNobodyAvailable. Capacity does not.
type Snapshot struct {
	Reason     string
	AutoAssign bool
}

// Port reads whether a destination can take a person, without sending a message.
// Inspect must not call distribution.Pick and must not take the
// distribution-auto advisory: Decide already holds sector shares from
// businesshours.EvaluateTx. Availability is the operational implementation.
// ReadDistributionQueue follows the queue lock order and is for a transaction
// that has not taken those shares. The queue view never reports nobody_available.
type Port interface {
	Inspect(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Snapshot, error)
}

// FixedPort returns one snapshot and does not read presence or capacity.
// Tests use it as a double. Production decisions use Availability.
type FixedPort struct {
	Reason     string
	AutoAssign bool
	Err        error
	Calls      int
}

func (p *FixedPort) Inspect(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (Snapshot, error) {
	if p == nil {
		return Snapshot{}, nil
	}
	p.Calls++
	if p.Err != nil {
		return Snapshot{}, p.Err
	}
	return Snapshot{Reason: p.Reason, AutoAssign: p.AutoAssign}, nil
}

// ReadDistributionQueue adapts distribution.Queue. It does not assign, reserve a
// vacancy, advance a cursor, or insert a message. Call it before EvaluateTx.
func ReadDistributionQueue(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Snapshot, error) {
	view, err := distribution.Queue(ctx, tx, tenant, sector, inbox, now)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Reason: view.Reason, AutoAssign: view.AutoAssign}, nil
}
