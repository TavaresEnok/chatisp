package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/presence"
)

// offlineBatch bounds the conversations released for one attendant per tick.
const offlineBatch = 50

// UnassignOffline returns to the queue the open conversations of attendants
// whose presence has stayed offline beyond the tenant tolerance. A quick
// connection drop does not release anything: presence only reports
// OfflineForUnassign after the tolerance, and an attendant without any presence
// record is never released. Conversations waiting for the customer keep their
// owner (the inactivity clock depends on it); they are released on a later tick
// once a customer message returns them to open. Each attendant uses its own
// transaction, locking the conversations in id order before reading presence,
// the same order used by snooze return and inactivity.
func UnassignOffline(ctx context.Context, database interface {
	WithTenantTx(context.Context, uuid.UUID, func(context.Context, pgx.Tx) error) error
}, tenant uuid.UUID, now time.Time) (int, error) {
	type owner struct{ member, user uuid.UUID }
	var owners []owner
	if err := database.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT m.id, m.user_id FROM conversations c JOIN memberships m ON m.tenant_id=c.tenant_id AND m.id=c.assignee_id WHERE c.tenant_id=$1 AND c.status='open' AND c.controller='human' ORDER BY m.id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o owner
			if err := rows.Scan(&o.member, &o.user); err != nil {
				return err
			}
			owners = append(owners, o)
		}
		return rows.Err()
	}); err != nil {
		return 0, err
	}
	released := 0
	var failures []error
	for _, o := range owners {
		err := database.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id, version FROM conversations WHERE tenant_id=$1 AND assignee_id=$2 AND status='open' AND controller='human' ORDER BY id LIMIT $3 FOR UPDATE`, tenant, o.member, offlineBatch)
			if err != nil {
				return err
			}
			type held struct {
				id      uuid.UUID
				version int64
			}
			var convs []held
			for rows.Next() {
				var h held
				if err := rows.Scan(&h.id, &h.version); err != nil {
					rows.Close()
					return err
				}
				convs = append(convs, h)
			}
			rows.Close()
			if err := rows.Err(); err != nil || len(convs) == 0 {
				return err
			}
			snapshot, err := presence.Effective(ctx, tx, tenant, o.user, now)
			if err != nil {
				return err
			}
			if !snapshot.OfflineForUnassign {
				return nil
			}
			for _, h := range convs {
				if _, err := (store.Repo{}).ApplyAssignment(ctx, tx, tenant, h.id, uuid.Nil, uuid.Nil, uuid.Nil, h.version, "offline_unassigned", now); err != nil {
					if errors.Is(err, store.ErrStaleVersion) || errors.Is(err, store.ErrClosed) {
						continue
					}
					return err
				}
				released++
			}
			return nil
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return released, errors.Join(failures...)
}
