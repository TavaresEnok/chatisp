package realtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/db/dbgen"
	"chatisp/internal/sectors"
)

func sectorPrincipal(p attendanceService.Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}

// BoundedAttendance wraps queries against PostgreSQL with strict SQL limits,
// preventing unconstrained memory allocation before materializing full result sets.
type BoundedAttendance struct {
	db       *db.Store
	fallback AttendanceService
}

// NewBoundedAttendance creates an AttendanceService that enforces bounded SQL queries.
func NewBoundedAttendance(database *db.Store, fallback AttendanceService) *BoundedAttendance {
	return &BoundedAttendance{
		db:       database,
		fallback: fallback,
	}
}

// Events returns at most maxEventsPerBatch events from durable storage.
// NextCursor represents the examined sequence, distinguishing examined page from remaining pending range.
func (b *BoundedAttendance) Events(ctx context.Context, p attendanceService.Principal, inboxID uuid.UUID, after int64) (store.EventPage, error) {
	if _, ok := b.fallback.(attendanceService.Service); b.db == nil || !ok {
		if b.fallback != nil {
			return b.fallback.Events(ctx, p, inboxID, after)
		}
		return store.EventPage{}, ErrForbidden
	}

	if after < 0 {
		return store.EventPage{}, store.ErrInvalid
	}

	var page store.EventPage
	err := b.db.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := authorizeInbox(ctx, tx, p, inboxID); err != nil {
			return err
		}
		ceiling, err := store.CursorCeiling(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		ids, err := sectors.AuthorizedConversationIDs(ctx, tx, sectorPrincipal(p), inboxID)
		if err != nil {
			return err
		}

		q := dbgen.New(tx)
		if err := q.EnsureRealtimeCursor(ctx, p.TenantID); err != nil {
			return err
		}
		cursor, err := q.LockRealtimeCursor(ctx, p.TenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}

		if after < cursor.RetainAfter {
			return store.ErrResync
		}
		if cursor.Seq > ceiling {
			cursor.Seq = ceiling
		}

		if cursor.Seq <= after {
			page = store.EventPage{NextCursor: cursor.Seq, Events: nil}
			return nil
		}

		rows, err := tx.Query(ctx, `
			SELECT seq, id, event_type, aggregate_type, aggregate_id, version, payload
			FROM realtime_events
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND seq > $2
			  AND seq <= $3
			  AND aggregate_type = 'conversation' AND aggregate_id = ANY($5::uuid[])
			ORDER BY seq
			LIMIT $4
		`, p.TenantID, after, cursor.Seq, maxEventsPerBatch, ids)
		if err != nil {
			return err
		}
		defer rows.Close()

		events := make([]store.Event, 0, maxEventsPerBatch)
		var lastSeq int64 = after
		for rows.Next() {
			var ev store.Event
			var payload []byte
			if err := rows.Scan(&ev.Seq, &ev.ID, &ev.Type, &ev.AggregateType, &ev.AggregateID, &ev.Version, &payload); err != nil {
				return err
			}
			ev.Payload = payload
			lastSeq = ev.Seq
			events = append(events, ev)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		nextCursor := cursor.Seq
		if len(events) == maxEventsPerBatch && lastSeq < cursor.Seq {
			nextCursor = lastSeq
		}

		page = store.EventPage{
			NextCursor: nextCursor,
			Events:     events,
		}
		return nil
	})

	return page, err
}

// Snapshot returns inbox conversations and optional message history with strict size caps.
// If the count exceeds maximum capacity, ErrLoadLimitExceeded is returned before full table materialization.
func (b *BoundedAttendance) Snapshot(ctx context.Context, p attendanceService.Principal, in store.SnapshotInput) (store.Snapshot, error) {
	if _, ok := b.fallback.(attendanceService.Service); b.db == nil || !ok {
		if b.fallback != nil {
			return b.fallback.Snapshot(ctx, p, in)
		}
		return store.Snapshot{}, ErrForbidden
	}

	if in.InboxID == uuid.Nil {
		return store.Snapshot{}, store.ErrInvalid
	}

	var out store.Snapshot
	err := b.db.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := authorizeInbox(ctx, tx, p, in.InboxID); err != nil {
			return err
		}
		ceiling, err := store.CursorCeiling(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		ids, err := sectors.AuthorizedConversationIDs(ctx, tx, sectorPrincipal(p), in.InboxID)
		if err != nil {
			return err
		}

		if in.ConversationID != uuid.Nil {
			if err := sectors.RequireConversation(ctx, tx, sectorPrincipal(p), in.InboxID, in.ConversationID); err != nil {
				if errors.Is(err, sectors.ErrForbidden) {
					return attendanceService.ErrForbidden
				}
				return err
			}
			var convInbox uuid.UUID
			err := tx.QueryRow(ctx, `
				SELECT inbox_id
				FROM conversations
				WHERE tenant_id = $1 AND id = $2
			`, p.TenantID, in.ConversationID).Scan(&convInbox)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return store.ErrNotFound
				}
				return err
			}
			if convInbox != in.InboxID {
				return store.ErrNotFound
			}
		}

		q := dbgen.New(tx)
		if err := q.EnsureRealtimeCursor(ctx, p.TenantID); err != nil {
			return err
		}
		cursor, err := q.LockRealtimeCursor(ctx, p.TenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		if cursor.Seq > ceiling {
			cursor.Seq = ceiling
		}

		// Read conversations with limit = maxSnapshotConversations + 1 to detect overflow without unbounded materialization
		convRows, err := tx.Query(ctx, `
			SELECT id, protocol, status, version, controller, controller_epoch, assignee_id,
			       contact_id, cycle_number, summary, last_customer_message_at, waiting_since, created_at
			FROM conversations
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND inbox_id = $2
			  AND id = ANY($4::uuid[])
			ORDER BY created_at, id
			LIMIT $3
		`, p.TenantID, in.InboxID, maxSnapshotConversations+1, ids)
		if err != nil {
			return err
		}
		defer convRows.Close()

		heads := make([]store.Head, 0, maxSnapshotConversations)
		for convRows.Next() {
			var h store.Head
			var proto *string
			var assignee, contact *uuid.UUID
			var lastCustomerMsg, waitingSince, createdAt pgtype.Timestamptz
			if err := convRows.Scan(
				&h.ID, &proto, &h.Status, &h.Version, &h.Controller, &h.ControllerEpoch,
				&assignee, &contact, &h.CycleNumber, &h.Summary,
				&lastCustomerMsg, &waitingSince, &createdAt,
			); err != nil {
				return err
			}
			h.Protocol = strVal(proto)
			h.AssigneeID = uuidVal(assignee)
			h.ContactID = uuidVal(contact)
			h.LastCustomerMessageAt = timeOf(lastCustomerMsg)
			h.WaitingSince = timeOf(waitingSince)
			h.CreatedAt = timeOf(createdAt)
			heads = append(heads, h)
		}
		if err := convRows.Err(); err != nil {
			return err
		}

		if len(heads) > maxSnapshotConversations {
			out.NextCursor = cursor.Seq
			return ErrLoadLimitExceeded
		}

		var messages []store.Message
		if in.ConversationID != uuid.Nil {
			msgRows, err := tx.Query(ctx, `
				SELECT id, conversation_id, external_message_id, direction, origin, visibility, body,
				       local_state, trusted_at, ingested_at, historical
				FROM messages
				WHERE tenant_id = $1
				  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
				  AND conversation_id = $2
				ORDER BY ingested_at, id
				LIMIT $3
			`, p.TenantID, in.ConversationID, maxSnapshotMessages+1)
			if err != nil {
				return err
			}
			defer msgRows.Close()

			messages = make([]store.Message, 0, maxSnapshotMessages)
			for msgRows.Next() {
				var m store.Message
				var extID, localState *string
				var trustedAt, ingestedAt pgtype.Timestamptz
				if err := msgRows.Scan(
					&m.ID, &m.ConversationID, &extID, &m.Direction, &m.Origin, &m.Visibility,
					&m.Body, &localState, &trustedAt, &ingestedAt, &m.Historical,
				); err != nil {
					return err
				}
				m.ExternalMessageID = strVal(extID)
				m.LocalState = strVal(localState)
				m.TrustedAt = timeOf(trustedAt)
				m.IngestedAt = timeOf(ingestedAt)
				messages = append(messages, m)
			}
			if err := msgRows.Err(); err != nil {
				return err
			}

			if len(messages) > maxSnapshotMessages {
				out.NextCursor = cursor.Seq
				return ErrLoadLimitExceeded
			}
		}

		out = store.Snapshot{
			NextCursor:    cursor.Seq,
			Conversations: heads,
			Messages:      messages,
		}
		return nil
	})

	return out, err
}

func authorizeInbox(ctx context.Context, tx pgx.Tx, p attendanceService.Principal, inboxID uuid.UUID) error {
	if err := sectors.RequireInbox(ctx, tx, sectorPrincipal(p), inboxID); err != nil {
		if errors.Is(err, sectors.ErrForbidden) {
			return attendanceService.ErrInboxForbidden
		}
		return err
	}
	if p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return attendanceService.ErrForbidden
	}
	var membershipStatus, role string
	err := tx.QueryRow(ctx, `
		SELECT status, role
		FROM memberships
		WHERE tenant_id = $1 AND id = $2 AND user_id = $3
	`, p.TenantID, p.MembershipID, p.UserID).Scan(&membershipStatus, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return attendanceService.ErrForbidden
		}
		return err
	}
	if membershipStatus != "active" {
		return attendanceService.ErrForbidden
	}
	switch role {
	case "admin", "supervisor", "agent":
	default:
		return attendanceService.ErrForbidden
	}

	var tenantStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, p.TenantID).Scan(&tenantStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return attendanceService.ErrTenantInactive
		}
		return err
	}
	if tenantStatus != "active" {
		return attendanceService.ErrTenantInactive
	}

	if inboxID != uuid.Nil {
		var exists bool
		err = tx.QueryRow(ctx, `
			SELECT true
			FROM inboxes AS i
			JOIN channel_connectors AS c ON c.tenant_id = i.tenant_id AND c.id = i.connector_id
			WHERE i.tenant_id = $1 AND i.id = $2
		`, p.TenantID, inboxID).Scan(&exists)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return attendanceService.ErrInboxForbidden
			}
			return err
		}
	}
	return nil
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func uuidVal(u *uuid.UUID) uuid.UUID {
	if u == nil {
		return uuid.Nil
	}
	return *u
}

func timeOf(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
