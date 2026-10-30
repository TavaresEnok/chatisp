package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// Snapshot locks the tenant cursor, then reads the inbox list and optional
// history. NextCursor is the examined sequence, including when the page is empty.
// Callers must not do network I/O before the transaction commits.
func (Repo) Snapshot(ctx context.Context, tx pgx.Tx, in SnapshotInput) (Snapshot, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return Snapshot{}, err
	}
	if in.InboxID == uuid.Nil {
		return Snapshot{}, ErrInvalid
	}
	q := queries(tx)
	cursor, err := lockCursor(ctx, q, in.TenantID)
	if err != nil {
		return Snapshot{}, err
	}
	if in.CursorCeiling != nil && cursor.Seq > *in.CursorCeiling {
		cursor.Seq = *in.CursorCeiling
	}
	rows, err := q.ListInboxConversations(ctx, dbgen.ListInboxConversationsParams{
		TenantID:      in.TenantID,
		InboxID:       uuidPtr(in.InboxID),
		AuthorizedIds: in.AuthorizedIDs,
	})
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{NextCursor: cursor.Seq, Conversations: make([]Head, 0, len(rows))}
	for _, row := range rows {
		out.Conversations = append(out.Conversations, Head{
			ID:                    row.ID,
			Protocol:              strVal(row.Protocol),
			Status:                row.Status,
			Version:               row.Version,
			Controller:            row.Controller,
			ControllerEpoch:       row.ControllerEpoch,
			AssigneeID:            uuidVal(row.AssigneeID),
			ContactID:             uuidVal(row.ContactID),
			CycleNumber:           row.CycleNumber,
			Summary:               row.Summary,
			LastCustomerMessageAt: timeOf(row.LastCustomerMessageAt),
			WaitingSince:          timeOf(row.WaitingSince),
			CreatedAt:             timeOf(row.CreatedAt),
		})
	}
	if in.ConversationID != uuid.Nil {
		messages, err := q.ListMessages(ctx, dbgen.ListMessagesParams{
			TenantID:       in.TenantID,
			ConversationID: in.ConversationID,
		})
		if err != nil {
			return Snapshot{}, err
		}
		out.Messages = make([]Message, 0, len(messages))
		for _, row := range messages {
			out.Messages = append(out.Messages, messageFrom(row))
		}
	}
	return out, nil
}

// ReadEvents returns events with sequence in (after, cursor]. A cursor older
// than the retention floor returns ErrResync instead of a partial page.
func (Repo) ReadEvents(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, after int64) (EventPage, error) {
	return readEvents(ctx, tx, tenantID, after, nil, nil)
}

func (Repo) ReadAuthorizedEvents(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, after int64, ids []uuid.UUID, ceiling int64) (EventPage, error) {
	return readEvents(ctx, tx, tenantID, after, ids, &ceiling)
}

func readEvents(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, after int64, ids []uuid.UUID, ceiling *int64) (EventPage, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return EventPage{}, err
	}
	if after < 0 {
		return EventPage{}, ErrInvalid
	}
	q := queries(tx)
	cursor, err := lockCursor(ctx, q, tenantID)
	if err != nil {
		return EventPage{}, err
	}
	if after < cursor.RetainAfter {
		return EventPage{}, ErrResync
	}
	if ceiling != nil && cursor.Seq > *ceiling {
		cursor.Seq = *ceiling
	}
	rows, err := q.ListRealtimeEvents(ctx, dbgen.ListRealtimeEventsParams{
		TenantID:      tenantID,
		AfterSeq:      after,
		ThroughSeq:    cursor.Seq,
		AuthorizedIds: ids,
	})
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{NextCursor: cursor.Seq, Events: make([]Event, 0, len(rows))}
	for _, row := range rows {
		page.Events = append(page.Events, Event{
			Seq:           row.Seq,
			ID:            row.ID,
			Type:          row.EventType,
			AggregateType: row.AggregateType,
			AggregateID:   row.AggregateID,
			Version:       row.Version,
			Payload:       row.Payload,
		})
	}
	return page, nil
}

// CursorCeiling captures the committed sequence before authorizing the list.
func CursorCeiling(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (int64, error) {
	if err := queries(tx).EnsureRealtimeCursor(ctx, tenantID); err != nil {
		return 0, err
	}
	var seq int64
	err := tx.QueryRow(ctx, `SELECT seq FROM realtime_tenant_cursors WHERE tenant_id=$1`, tenantID).Scan(&seq)
	return seq, err
}

// Compact drops events at or below retainAfter and moves the retention floor.
// Readers behind that floor must resync.
func (Repo) Compact(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, retainAfter int64) error {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if retainAfter < 1 {
		return ErrInvalid
	}
	q := queries(tx)
	if _, err := q.CompactRealtime(ctx, dbgen.CompactRealtimeParams{RetainAfter: retainAfter, TenantID: tenantID}); isNoRow(err) {
		return ErrInvalid
	} else if err != nil {
		return err
	}
	return q.DeleteExpiredRealtime(ctx, dbgen.DeleteExpiredRealtimeParams{TenantID: tenantID, RetainAfter: retainAfter})
}

func lockCursor(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID) (dbgen.LockRealtimeCursorRow, error) {
	if err := q.EnsureRealtimeCursor(ctx, tenantID); err != nil {
		return dbgen.LockRealtimeCursorRow{}, err
	}
	row, err := q.LockRealtimeCursor(ctx, tenantID)
	if isNoRow(err) {
		return dbgen.LockRealtimeCursorRow{}, ErrNotFound
	}
	return row, err
}
