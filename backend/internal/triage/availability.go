package triage

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	"chatisp/internal/presence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Availability is the operational port. It reads presence and vacancy for one
// sector inbox. It does not call distribution.Pick, does not take the
// distribution-auto advisory, does not reserve a vacancy and does not assign.
// The caller must already hold the shared tenant advisory. Decide also holds
// the conversation and the destination sector before Inspect.
//
// Lock order inside Inspect, after those caller locks:
//  1. share the sector and its inbox link
//  2. share distribution_sector_auto when a row exists
//  3. one candidate at a time, in membership id order, inside a savepoint:
//     membership share, presence.Effective, then the assignment-capacity advisory
//     used by attendance/store.LockAssignmentCapacity
//
// The savepoint is rolled back before Inspect returns, so the candidate share
// and the capacity advisory are not kept until the caller commits.
type Availability struct{}

type availabilityCandidate struct {
	User   uuid.UUID
	Member uuid.UUID
}

func (Availability) Inspect(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Snapshot, error) {
	var out Snapshot
	if tx == nil || tenant == uuid.Nil || sector == uuid.Nil || inbox == uuid.Nil || now.IsZero() {
		return out, ErrInvalid
	}
	found, active, linked, err := shareDestination(ctx, tx, tenant, sector, inbox)
	if err != nil {
		return out, err
	}
	auto, err := readAutoAssign(ctx, tx, tenant, sector)
	if err != nil {
		return out, err
	}
	out.AutoAssign = auto
	if !found || !active {
		out.Reason = ReasonSectorInactive
		return out, nil
	}
	if !linked {
		out.Reason = ReasonInboxUnlinked
		return out, nil
	}
	candidates, err := loadAvailability(ctx, tx, tenant, sector, inbox)
	if err != nil {
		return out, err
	}
	if len(candidates) == 0 {
		out.Reason = ReasonNobodyAvailable
		return out, nil
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT triage_availability`); err != nil {
		return out, err
	}
	sawAssignable := false
	ready := false
	for _, cand := range candidates {
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT triage_availability`); err != nil {
			return out, err
		}
		kept, err := lockAvailabilityCandidate(ctx, tx, tenant, sector, inbox, cand.Member)
		if err != nil {
			return out, rollbackAvailability(ctx, tx, err)
		}
		if !kept {
			continue
		}
		snap, err := presence.Effective(ctx, tx, tenant, cand.User, now)
		if err != nil {
			return out, rollbackAvailability(ctx, tx, err)
		}
		if !snap.Assignable || snap.MembershipID != cand.Member {
			continue
		}
		open, err := memberHasVacancy(ctx, tx, tenant, cand.Member)
		if err != nil {
			return out, rollbackAvailability(ctx, tx, err)
		}
		if !open {
			sawAssignable = true
			continue
		}
		ready = true
		break
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT triage_availability`); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `RELEASE SAVEPOINT triage_availability`); err != nil {
		return out, err
	}
	out.Reason = ReasonNobodyAvailable
	if sawAssignable {
		out.Reason = ReasonCapacityFull
	}
	if ready {
		out.Reason = ReasonReady
	}
	return out, nil
}

func rollbackAvailability(ctx context.Context, tx pgx.Tx, cause error) error {
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT triage_availability`); err != nil {
		return err
	}
	return cause
}

func shareDestination(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) (bool, bool, bool, error) {
	var active, linked bool
	err := tx.QueryRow(ctx, `
		SELECT s.active,
		       EXISTS(SELECT 1 FROM sector_inboxes si WHERE si.tenant_id=s.tenant_id AND si.sector_id=s.id AND si.inbox_id=$3)
		FROM sectors s
		WHERE s.tenant_id=$1 AND s.id=$2
		FOR SHARE`, tenant, sector, inbox).Scan(&active, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, false, nil
	}
	if err != nil {
		return false, false, false, err
	}
	if !linked {
		return true, active, false, nil
	}
	rows, err := tx.Query(ctx, `SELECT 1 FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3 FOR SHARE`, tenant, sector, inbox)
	if err != nil {
		return false, false, false, err
	}
	defer rows.Close()
	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		return false, false, false, err
	}
	return true, active, true, nil
}

func readAutoAssign(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT enabled FROM distribution_sector_auto WHERE tenant_id=$1 AND sector_id=$2 FOR SHARE`, tenant, sector).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return enabled, err
}

func loadAvailability(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) ([]availabilityCandidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.user_id, m.id
		FROM sector_memberships sm
		JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent')
		JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active
		WHERE sm.tenant_id=$1 AND sm.sector_id=$2
		  AND EXISTS (
		    SELECT 1 FROM sector_memberships own
		    JOIN sector_inboxes box ON box.tenant_id=own.tenant_id AND box.sector_id=own.sector_id AND box.inbox_id=$3
		    JOIN sectors os ON os.tenant_id=box.tenant_id AND os.id=box.sector_id AND os.active
		    WHERE own.tenant_id=sm.tenant_id AND own.membership_id=sm.membership_id
		  )`, tenant, sector, inbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []availabilityCandidate
	for rows.Next() {
		var item availabilityCandidate
		if err = rows.Scan(&item.User, &item.Member); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Member[:], out[j].Member[:]) < 0
	})
	return out, nil
}

func lockAvailabilityCandidate(ctx context.Context, tx pgx.Tx, tenant, sector, inbox, member uuid.UUID) (bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT 1
		FROM sector_memberships sm
		JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent')
		JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active
		WHERE sm.tenant_id=$1 AND sm.sector_id=$2 AND sm.membership_id=$3
		  AND EXISTS (
		    SELECT 1 FROM sector_memberships own
		    JOIN sector_inboxes box ON box.tenant_id=own.tenant_id AND box.sector_id=own.sector_id AND box.inbox_id=$4
		    JOIN sectors os ON os.tenant_id=box.tenant_id AND os.id=box.sector_id AND os.active
		    WHERE own.tenant_id=sm.tenant_id AND own.membership_id=sm.membership_id
		  )
		FOR SHARE OF sm, m, s`, tenant, sector, member, inbox)
	if err != nil {
		return false, err
	}
	kept := rows.Next()
	err = rows.Err()
	rows.Close()
	return kept, err
}

// memberHasVacancy takes the same advisory key as attendance/store.LockAssignmentCapacity.
// Call it only inside the availability savepoint and only after the conversation lock.
func memberHasVacancy(ctx context.Context, tx pgx.Tx, tenant, member uuid.UUID) (bool, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:' || $1::text || ':' || $2::text, 0))`, tenant, member); err != nil {
		return false, err
	}
	var limit, count int
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT capacity FROM assignment_capacities WHERE tenant_id=$1 AND membership_id=$2), 20),
		       (SELECT count(*)::int FROM conversations WHERE tenant_id=$1 AND assignee_id=$2 AND status IN ('open','waiting_customer'))`,
		tenant, member).Scan(&limit, &count)
	if err != nil {
		return false, err
	}
	return count < limit, nil
}
