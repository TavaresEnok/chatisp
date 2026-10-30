package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"chatisp/internal/attendance/store"
	"chatisp/internal/conversation"
	"chatisp/internal/platform/db/dbgen"
)

func (s Service) tx(ctx context.Context, tenantID uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, tenantID, fn)
}

func queries(tx pgx.Tx) *dbgen.Queries {
	return dbgen.New(tx)
}

func loadConversation(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (store.Conversation, error) {
	row, err := queries(tx).GetConversation(ctx, dbgen.GetConversationParams{TenantID: tenantID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Conversation{}, store.ErrNotFound
	}
	if err != nil {
		return store.Conversation{}, err
	}
	return conversationFrom(row), nil
}

func lockConversation(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (store.Conversation, error) {
	if _, err := queries(tx).LockConversation(ctx, dbgen.LockConversationParams{TenantID: tenantID, ID: id}); errors.Is(err, pgx.ErrNoRows) {
		return store.Conversation{}, store.ErrNotFound
	} else if err != nil {
		return store.Conversation{}, err
	}
	return loadConversation(ctx, tx, tenantID, id)
}

func conversationFrom(row dbgen.Conversation) store.Conversation {
	return store.Conversation{
		ID:                       row.ID,
		TenantID:                 row.TenantID,
		InboxID:                  uuidVal(row.InboxID),
		ContactID:                uuidVal(row.ContactID),
		IdentityID:               uuidVal(row.ContactIdentityID),
		Status:                   row.Status,
		Controller:               row.Controller,
		ControllerEpoch:          row.ControllerEpoch,
		Version:                  row.Version,
		AssigneeID:               uuidVal(row.AssigneeID),
		Protocol:                 strVal(row.Protocol),
		ProtocolPeriod:           strVal(row.ProtocolPeriod),
		ProtocolCommunicated:     row.ProtocolCommunicated,
		ExternalProtocol:         strVal(row.ExternalProtocol),
		LastCustomerMessageAt:    timeOf(row.LastCustomerMessageAt),
		LastEligibleHumanReplyAt: timeOf(row.LastEligibleHumanReplyAt),
		WaitingSince:             timeOf(row.WaitingSince),
		CycleNumber:              row.CycleNumber,
		Summary:                  row.Summary,
		CreatedAt:                timeOf(row.CreatedAt),
	}
}

func modelFrom(conv store.Conversation) conversation.Conversation {
	cycle := conversation.Cycle{Number: int(conv.CycleNumber)}
	if conv.Status == store.StatusClosed {
		cycle.ClosedAt = conv.CreatedAt
		if cycle.ClosedAt.IsZero() {
			cycle.ClosedAt = time.Unix(1, 0).UTC()
		}
		cycle.Closure.Result = conversation.CloseResult(conv.Summary)
	}
	if conv.CycleNumber < 1 {
		cycle.Number = 1
	}
	return conversation.Conversation{
		ID:                       conv.ID,
		TenantID:                 conv.TenantID,
		InboxID:                  conv.InboxID,
		ContactID:                conv.ContactID,
		Status:                   conversation.Status(conv.Status),
		Version:                  conv.Version,
		Controller:               conversation.Controller(conv.Controller),
		ControllerEpoch:          conv.ControllerEpoch,
		AssigneeID:               conv.AssigneeID,
		Protocol:                 conv.Protocol,
		LastCustomerMessageAt:    conv.LastCustomerMessageAt,
		LastEligibleHumanReplyAt: conv.LastEligibleHumanReplyAt,
		WaitingSince:             conv.WaitingSince,
		Cycles:                   []conversation.Cycle{cycle},
	}
}

func claim(ctx context.Context, q *dbgen.Queries, tenantID, principal uuid.UUID, operation, requestID string, sum []byte) (bool, error) {
	if requestID == "" || len(sum) != 32 {
		return false, store.ErrInvalid
	}
	row, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: tenantID, PrincipalID: principal, Operation: operation, ClientRequestID: requestID,
	})
	if err == nil {
		if !bytes.Equal(row.RequestHash, sum) {
			return false, store.ErrIdempotencyConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = q.InsertIdempotency(ctx, dbgen.InsertIdempotencyParams{
		TenantID: tenantID, ID: uuid.New(), PrincipalID: principal,
		Operation: operation, ClientRequestID: requestID, RequestHash: sum,
	})
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	row, err = q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: tenantID, PrincipalID: principal, Operation: operation, ClientRequestID: requestID,
	})
	if err != nil {
		return false, err
	}
	if !bytes.Equal(row.RequestHash, sum) {
		return false, store.ErrIdempotencyConflict
	}
	return true, nil
}

func publish(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, eventType string, aggregate uuid.UUID, version int64, fields map[string]any) error {
	if err := q.EnsureRealtimeCursor(ctx, tenantID); err != nil {
		return err
	}
	seq, err := q.AdvanceRealtimeCursor(ctx, tenantID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return q.InsertRealtimeEvent(ctx, dbgen.InsertRealtimeEventParams{
		TenantID: tenantID, Seq: seq, ID: uuid.New(), EventType: eventType,
		AggregateType: "conversation", AggregateID: aggregate, Version: version, Payload: payload,
	})
}

func enqueueJob(ctx context.Context, jobs Queuer, tx pgx.Tx, tenantID, attemptID uuid.UUID) (int64, error) {
	if jobs == nil {
		return 0, ErrJobRequired
	}
	id, err := jobs.EnqueueOutbound(ctx, tx, tenantID, attemptID)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, ErrJobRequired
	}
	return id, nil
}

func savepoint(ctx context.Context, tx pgx.Tx, name string) error {
	_, err := tx.Exec(ctx, "SAVEPOINT "+name)
	return err
}

func rollbackSavepoint(ctx context.Context, tx pgx.Tx, name string) {
	_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name)
}

func releaseSavepoint(ctx context.Context, tx pgx.Tx, name string) error {
	_, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func uuidVal(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func strVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func timeOf(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func ts(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
