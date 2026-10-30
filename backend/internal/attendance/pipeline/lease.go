package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

type outboundLease struct {
	dbStore *db.Store
	repo    store.Repo
}

// NewOutboundLease constructs the River OutboundLease coordinator.
func NewOutboundLease(database *db.Store) jobs.OutboundLease {
	return &outboundLease{
		dbStore: database,
		repo:    store.Repo{},
	}
}

// AllowOutbound implements jobs.OutboundLease.
// It checks whether the tenant has available outbound slots and whether
// the inbox send lease is not currently held by another worker.
func (l *outboundLease) AllowOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) (bool, error) {
	if l == nil || l.dbStore == nil {
		return false, ErrUnavailable
	}
	if tenantID == uuid.Nil || attemptID == uuid.Nil {
		return false, ErrInvalid
	}

	var allowed bool
	err := l.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 1. Check tenant worker slot capacity for outbound queue.
		// Total outbound workers in jobs.Client is 2. To ensure fair sharing and prevent
		// a noisy tenant from starving other tenants, default limit is 1 slot per tenant.
		var maxSlots int32 = 1
		_ = tx.QueryRow(ctx, `
			SELECT max_slots
			FROM tenant_job_slots
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND queue_name = 'outbound'
		`, tenantID).Scan(&maxSlots)
		if maxSlots < 1 || maxSlots >= 2 {
			maxSlots = 1
		}

		var liveSlots int64
		err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM tenant_job_reservations
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND queue_name = 'outbound'
			  AND expires_at > now()
		`, tenantID).Scan(&liveSlots)
		if err != nil {
			return err
		}
		if liveSlots >= int64(maxSlots) {
			allowed = false
			return nil
		}

		// 2. Peek attempt state and inbox_id.
		var (
			state   string
			inboxID uuid.UUID
		)
		err = tx.QueryRow(ctx, `
			SELECT o.state, c.inbox_id
			FROM outbound_attempts o
			JOIN conversations c
			  ON c.tenant_id = o.tenant_id AND c.id = o.conversation_id
			WHERE o.tenant_id = $1
			  AND o.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND o.id = $2
		`, tenantID, attemptID).Scan(&state, &inboxID)
		if errors.Is(err, pgx.ErrNoRows) {
			allowed = true
			return nil
		}
		if err != nil {
			return err
		}

		// Already finished states or crashed sending state pass through so handler runs completion / recovery.
		if state == "accepted" || state == "failed" || state == "uncertain" || state == "cancelled" || state == "sending" {
			allowed = true
			return nil
		}

		// 3. Check inbox send lease.
		var (
			ownerID   uuid.UUID
			expiresAt time.Time
		)
		err = tx.QueryRow(ctx, `
			SELECT owner_id, expires_at
			FROM inbox_send_leases
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND inbox_id = $2
		`, tenantID, inboxID).Scan(&ownerID, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			allowed = true
			return nil
		}
		if err != nil {
			return err
		}

		// If lease is unexpired, snooze.
		if expiresAt.After(time.Now()) {
			allowed = false
			return nil
		}

		allowed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}
