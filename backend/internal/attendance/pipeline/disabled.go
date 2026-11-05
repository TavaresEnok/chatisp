package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
)

// ReleaseDisabled returns to the queue the conversations of memberships that are
// no longer active. A disabled attendant has no presence to wait for, so there is
// no tolerance: open and waiting conversations lose the owner at once and keep
// their history, recorded as offline_unassigned like the presence path. Closed conversations are untouched. Each membership uses its own
// transaction and locks conversations in id order, like UnassignOffline.
func ReleaseDisabled(ctx context.Context, database interface {
	WithTenantTx(context.Context, uuid.UUID, func(context.Context, pgx.Tx) error) error
}, tenant uuid.UUID, now time.Time) (int, error) {
	var members []uuid.UUID
	if err := database.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT m.id FROM conversations c JOIN memberships m ON m.tenant_id=c.tenant_id AND m.id=c.assignee_id WHERE c.tenant_id=$1 AND c.status IN ('open','waiting_customer') AND m.status<>'active' ORDER BY m.id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			members = append(members, id)
		}
		return rows.Err()
	}); err != nil {
		return 0, err
	}
	released := 0
	var failures []error
	for _, member := range members {
		err := database.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT c.id, c.version FROM conversations c JOIN memberships m ON m.tenant_id=c.tenant_id AND m.id=c.assignee_id WHERE c.tenant_id=$1 AND c.assignee_id=$2 AND c.status IN ('open','waiting_customer') AND m.status<>'active' ORDER BY c.id LIMIT $3 FOR UPDATE OF c`, tenant, member, offlineBatch)
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
			if err := rows.Err(); err != nil {
				return err
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
