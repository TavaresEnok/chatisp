package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCapacityFull indicates that the target has no free manual capacity.
var ErrCapacityFull = errors.New("attendance capacity full")

// LockAssignmentCapacity serializes every capacity-increasing assignment for a
// member until commit. Call after the tenant policy gate and conversation lock,
// before changing that conversation. All callers (including Assume/Reopen)
// must use this same protocol. Counts include all inboxes and sectors.
func LockAssignmentCapacity(ctx context.Context, tx pgx.Tx, tenantID, memberID, conversationID uuid.UUID) error {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if memberID == uuid.Nil || conversationID == uuid.Nil {
		return ErrInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:' || $1::text || ':' || $2::text, 0))`, tenantID, memberID); err != nil {
		return err
	}
	var limit, count int
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT capacity FROM assignment_capacities WHERE tenant_id=$1 AND membership_id=$2),20),
 (SELECT count(*) FROM conversations WHERE tenant_id=$1 AND assignee_id=$2 AND status IN ('open','waiting_customer') AND id<>$3)`, tenantID, memberID, conversationID).Scan(&limit, &count)
	if err != nil {
		return err
	}
	if count >= limit {
		return ErrCapacityFull
	}
	return nil
}

// ApplyAssignment changes a locked conversation and writes its timeline,
// automatic-send cancellations and realtime event in the caller's transaction.
func (Repo) ApplyAssignment(ctx context.Context, tx pgx.Tx, tenantID, conversationID, actorID, memberID, sectorID uuid.UUID, expected int64, kind string, at time.Time) (Conversation, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Conversation{}, err
	}
	q := queries(tx)
	current, err := lock(ctx, q, tenantID, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if current.Version != expected {
		return Conversation{}, ErrStaleVersion
	}
	if current.Status == StatusClosed {
		return Conversation{}, ErrClosed
	}
	var sourceSector uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2`, tenantID, conversationID).Scan(&sourceSector); err != nil {
		return Conversation{}, err
	}
	destinationSector := sourceSector
	if sectorID != uuid.Nil {
		destinationSector = sectorID
	}
	eventKind := fmt.Sprintf("%s:%s:%s:%s:%s", kind, sourceSector, destinationSector, current.AssigneeID, memberID)
	controller := ControllerMenu
	var member any
	if memberID != uuid.Nil {
		controller = ControllerHuman
		member = memberID
	}
	var targetSector any
	if sectorID != uuid.Nil {
		targetSector = sectorID
	}
	_, err = tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3,controller=$4,sector_id=COALESCE($5::uuid,sector_id),status=CASE WHEN status='triage' THEN 'open' ELSE status END,controller_epoch=controller_epoch+1,version=version+1,updated_at=$6 WHERE tenant_id=$1 AND id=$2`, tenantID, conversationID, member, controller, targetSector, at)
	if err != nil {
		return Conversation{}, err
	}
	if err := cancelQueuedAutomatic(ctx, q, tenantID, conversationID, current.ControllerEpoch, current.Version+1); err != nil {
		return Conversation{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO conversation_events(tenant_id,id,conversation_id,cycle_number,kind,actor_id,at,version,controller_epoch,controller,assignee_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenantID, uuid.New(), conversationID, current.CycleNumber, eventKind, actorID, at, current.Version+1, current.ControllerEpoch+1, controller, member)
	if err != nil {
		return Conversation{}, err
	}
	if err := publish(ctx, q, tenantID, "conversation."+kind, "conversation", conversationID, current.Version+1, map[string]any{"conversation_id": conversationID.String()}); err != nil {
		return Conversation{}, err
	}
	return load(ctx, q, tenantID, conversationID)
}
