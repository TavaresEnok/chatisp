package outwebhook

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/urlguard"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRetryAfter tells the River worker to snooze until the next attempt.
type ErrRetryAfter struct {
	After time.Duration
}

func (e ErrRetryAfter) Error() string { return "entrega adiada" }

// Deps is the delivery worker's database, key ring and outbound policy.
type Deps struct {
	Store  *db.Store
	Ring   *crypto.Ring
	Policy *urlguard.Policy
	Mode   string
	Poster Poster
	Now    func() time.Time
}

func (d Deps) clock() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d Deps) poster() Poster {
	if d.Poster != nil {
		return d.Poster
	}
	policy := d.Policy
	if policy == nil {
		policy, _ = urlguard.NewPolicy()
	}
	return HTTPPoster{Policy: policy, Mode: d.Mode}
}

// DeliverSource materializes the stable event and performs one due attempt.
// A delivered event is not posted again. A refused private destination is dead.
// A retryable failure returns ErrRetryAfter and keeps the same event id.
func DeliverSource(ctx context.Context, deps Deps, tenant uuid.UUID, eventType string, source uuid.UUID) error {
	if deps.Store == nil || deps.Ring == nil {
		return ErrEvent
	}
	var deliveries []Delivery
	err := deps.Store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		deliveries, err = Materialize(ctx, tx, deps.Ring, tenant, eventType, source)
		return err
	})
	if errors.Is(err, ErrStored) {
		return nil
	}
	if err != nil {
		return err
	}
	var wait time.Duration
	for _, item := range deliveries {
		if item.Status != StatusPending {
			continue
		}
		// Each claim reads the clock again. A slow POST must not stamp the
		// next row with the time of the first claim, or the lease is already
		// expired and another worker posts the same delivery.
		now := deps.clock()
		if now.Before(item.NextAt) {
			wait = sooner(wait, item.NextAt.Sub(now))
			continue
		}
		var claimed Delivery
		var leased bool
		err = deps.Store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			claimed, leased, err = ClaimDelivery(ctx, tx, tenant, item.ID, now)
			return err
		})
		if err != nil {
			return err
		}
		if !leased {
			held, err := deliveryWait(ctx, deps, tenant, item.ID, deps.clock())
			if err != nil {
				return err
			}
			if held > 0 {
				wait = sooner(wait, held)
			}
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptBudget)
		acted := deps.clock()
		next, refused, err := attempt(attemptCtx, deps, claimed, acted)
		cancel()
		if err != nil {
			return err
		}
		// Advance stamps NextAt from the start of the POST. A slow 503 or a
		// client timeout can outlast that backoff, leaving the row pending
		// with NextAt already due. The River job would then complete, and
		// recovery will not enqueue the source again because enqueued_at is
		// set. Count the next delay from the end of the attempt, and keep
		// every pending row scheduled.
		finished := deps.clock()
		if next.Status == StatusPending && !next.NextAt.After(finished) {
			next.NextAt = finished.Add(backoff(next.Attempt))
		}
		next.Claim = claimed.Claim
		if err := deps.Store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return UpdateDelivery(ctx, tx, next)
		}); err != nil {
			return err
		}
		if refused {
			continue
		}
		if next.Status == StatusPending {
			wait = sooner(wait, next.NextAt.Sub(deps.clock()))
		}
	}
	if wait > 0 {
		return ErrRetryAfter{After: wait}
	}
	return nil
}

func deliveryWait(ctx context.Context, deps Deps, tenant, id uuid.UUID, now time.Time) (time.Duration, error) {
	var status string
	var nextAt time.Time
	var claimed bool
	var claimedAt *time.Time
	err := deps.Store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT status, next_attempt_at, claim_token IS NOT NULL, claimed_at
			FROM webhook_deliveries
			WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&status, &nextAt, &claimed, &claimedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrStored
	}
	if err != nil {
		return 0, err
	}
	if status != StatusPending {
		return 0, nil
	}
	if now.Before(nextAt) {
		return nextAt.Sub(now), nil
	}
	if claimed && claimedAt != nil && now.Sub(claimedAt.UTC()) < claimLease {
		remain := claimLease - now.Sub(claimedAt.UTC())
		if remain < time.Second {
			remain = time.Second
		}
		return remain, nil
	}
	return time.Second, nil
}

func sooner(current, candidate time.Duration) time.Duration {
	if candidate < time.Second {
		candidate = time.Second
	}
	if current == 0 || candidate < current {
		return candidate
	}
	return current
}

func attempt(ctx context.Context, deps Deps, item Delivery, now time.Time) (Delivery, bool, error) {
	var stored StoredSubscription
	err := deps.Store.WithTenantTx(ctx, item.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		stored, err = LoadSubscription(ctx, tx, item.TenantID, item.SubscriptionID)
		return err
	})
	if err != nil {
		return Delivery{}, false, err
	}
	if !stored.Enabled {
		item.Status = StatusDead
		item.Attempt++
		return item, true, nil
	}
	if deps.Policy == nil {
		deps.Policy, err = urlguard.NewPolicy()
		if err != nil {
			return Delivery{}, false, err
		}
	}
	if err := AllowDestination(deps.Policy, deps.Mode, stored.URL); err != nil {
		item.Status = StatusDead
		item.Attempt++
		return item, true, nil
	}
	secret, err := OpenSecret(deps.Ring, stored)
	if err != nil {
		return Delivery{}, false, err
	}
	next, err := Dispatch(ctx, deps.poster(), secret, stored.URL, item, now)
	if err != nil {
		return Delivery{}, false, err
	}
	return next, false, nil
}
