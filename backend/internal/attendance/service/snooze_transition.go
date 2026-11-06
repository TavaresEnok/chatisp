package service

import (
	"context"
	"time"

	"chatisp/internal/attendance/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// clearManualSnooze runs after the conversation lock and before the state
// change, so the queue trigger sees the new assignment without an active snooze.
func clearManualSnooze(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, at time.Time, resolution string) (bool, error) {
	state := "returned"
	if resolution == "queued" {
		state = "queued"
	}
	tag, err := tx.Exec(ctx, `UPDATE conversation_snoozes SET state=$3,resolution=$4,resolved_at=GREATEST(created_at,$5) WHERE tenant_id=$1 AND conversation_id=$2 AND state='active'`, tenant, conversation, state, resolution, at)
	return tag.RowsAffected() > 0, err
}

func recordManualSnoozeClear(ctx context.Context, tx pgx.Tx, tenant, actor uuid.UUID, conv store.Conversation, at time.Time, cleared bool) error {
	if !cleared {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO conversation_events(tenant_id,id,conversation_id,cycle_number,kind,actor_id,at,version,controller_epoch,controller,assignee_id) VALUES($1,$2,$3,$4,'snooze_cleared',$5,$6,$7,$8,$9,$10)`, tenant, uuid.New(), conv.ID, conv.CycleNumber, uuidPtr(actor), at, conv.Version, conv.ControllerEpoch, conv.Controller, uuidPtr(conv.AssigneeID))
	return err
}
