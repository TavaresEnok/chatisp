package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db/dbgen"
)

const (
	// opCloseCycle stores the cycle a close key produced. It is not a panel operation.
	opCloseCycle = "close_attendance_cycle"
	// opReopenCycle stores the cycle a reopen key produced.
	opReopenCycle = "reopen_attendance_cycle"
	// opCloseFarewell authorizes one human goodbye after the close bumps the epoch.
	// The client request id is the attempt id. Bot and menu sends never get this row.
	opCloseFarewell = "close_farewell"
)

// bindCycle remembers the cycle, version and epoch produced by one command key.
func bindCycle(ctx context.Context, q *dbgen.Queries, p Principal, operation, requestID string, conversationID uuid.UUID, cycle int32, version, epoch int64) error {
	sum, err := encodeCycle(conversationID, cycle, version, epoch)
	if err != nil {
		return err
	}
	return bindOutcome(ctx, q, p, operation, requestID, sum)
}

func readCycle(ctx context.Context, q *dbgen.Queries, p Principal, operation, requestID string, conversationID uuid.UUID) (int32, int64, int64, error) {
	sum, err := readOutcome(ctx, q, p, operation, requestID)
	if err != nil {
		return 0, 0, 0, err
	}
	id, cycle, version, epoch, err := decodeCycle(sum)
	if err != nil {
		return 0, 0, 0, err
	}
	if id != conversationID {
		return 0, 0, 0, store.ErrInvalid
	}
	return cycle, version, epoch, nil
}

func bindFarewell(ctx context.Context, q *dbgen.Queries, p Principal, conv store.Conversation, attemptID uuid.UUID) error {
	if attemptID == uuid.Nil || conv.ControllerEpoch < 1 || conv.CycleNumber < 1 {
		return store.ErrInvalid
	}
	sum := encodeFarewell(conv.ID, conv.ControllerEpoch, conv.CycleNumber)
	return bindOutcome(ctx, q, p, opCloseFarewell, attemptID.String(), sum)
}

// farewellAuthorized is true only for the human goodbye bound to this attempt's epoch.
func farewellAuthorized(ctx context.Context, tx pgx.Tx, tenantID, attemptID, conversationID uuid.UUID, epoch int64) (bool, error) {
	var origin string
	err := tx.QueryRow(ctx, `
		SELECT origin
		FROM outbound_attempts
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
	`, tenantID, attemptID).Scan(&origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, store.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if origin != store.ControllerHuman {
		return false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT request_hash
		FROM idempotency_records
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND operation = $2
		  AND client_request_id = $3
	`, tenantID, opCloseFarewell, attemptID.String())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var sum []byte
		if err := rows.Scan(&sum); err != nil {
			return false, err
		}
		if farewellMatches(sum, conversationID, epoch) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// projectCycle returns the conversation as the bound command left it.
// Later cycles stay in the row; this view does not follow them.
func projectCycle(ctx context.Context, tx pgx.Tx, tenantID, conversationID uuid.UUID, cycle int32, version, epoch int64, kind, summary string) (store.Conversation, error) {
	conv, err := loadConversation(ctx, tx, tenantID, conversationID)
	if err != nil {
		return store.Conversation{}, err
	}
	var eventVersion, eventEpoch int64
	var controller string
	var assignee *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT version, controller_epoch, controller, assignee_id
		FROM conversation_events
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND conversation_id = $2
		  AND cycle_number = $3
		  AND kind = $4
		ORDER BY at, id
		LIMIT 1
	`, tenantID, conversationID, cycle, kind).Scan(&eventVersion, &eventEpoch, &controller, &assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Conversation{}, store.ErrNotFound
	}
	if err != nil {
		return store.Conversation{}, err
	}
	if eventVersion != version || eventEpoch != epoch {
		return store.Conversation{}, store.ErrInvalid
	}
	conv.CycleNumber = cycle
	conv.Version = eventVersion
	conv.ControllerEpoch = eventEpoch
	conv.Controller = controller
	conv.AssigneeID = uuidVal(assignee)
	conv.WaitingSince = time.Time{}
	if kind == "closed" {
		conv.Status = store.StatusClosed
		if summary != "" {
			conv.Summary = summary
		}
		return conv, nil
	}
	conv.Status = store.StatusOpen
	conv.Controller = store.ControllerMenu
	conv.AssigneeID = uuid.Nil
	return conv, nil
}

func bindOutcome(ctx context.Context, q *dbgen.Queries, p Principal, operation, requestID string, sum []byte) error {
	if requestID == "" || len(sum) != 32 {
		return store.ErrInvalid
	}
	_, err := q.InsertIdempotency(ctx, dbgen.InsertIdempotencyParams{
		TenantID: p.TenantID, ID: uuid.New(), PrincipalID: p.MembershipID,
		Operation: operation, ClientRequestID: requestID, RequestHash: sum,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	row, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: p.TenantID, PrincipalID: p.MembershipID, Operation: operation, ClientRequestID: requestID,
	})
	if err != nil {
		return err
	}
	if !bytes.Equal(row.RequestHash, sum) {
		return store.ErrIdempotencyConflict
	}
	return nil
}

func readOutcome(ctx context.Context, q *dbgen.Queries, p Principal, operation, requestID string) ([]byte, error) {
	row, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: p.TenantID, PrincipalID: p.MembershipID, Operation: operation, ClientRequestID: requestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return row.RequestHash, nil
}

func encodeCycle(conversationID uuid.UUID, cycle int32, version, epoch int64) ([]byte, error) {
	if conversationID == uuid.Nil || cycle < 1 || version < 1 || epoch < 1 || epoch > int64(^uint32(0)) {
		return nil, store.ErrInvalid
	}
	buf := make([]byte, 32)
	copy(buf[:16], conversationID[:])
	binary.BigEndian.PutUint32(buf[16:20], uint32(cycle))
	binary.BigEndian.PutUint64(buf[20:28], uint64(version))
	binary.BigEndian.PutUint32(buf[28:32], uint32(epoch))
	return buf, nil
}

func decodeCycle(sum []byte) (uuid.UUID, int32, int64, int64, error) {
	if len(sum) != 32 {
		return uuid.Nil, 0, 0, 0, store.ErrInvalid
	}
	var id uuid.UUID
	copy(id[:], sum[:16])
	cycle := int32(binary.BigEndian.Uint32(sum[16:20]))
	version := int64(binary.BigEndian.Uint64(sum[20:28]))
	epoch := int64(binary.BigEndian.Uint32(sum[28:32]))
	if id == uuid.Nil || cycle < 1 || version < 1 || epoch < 1 {
		return uuid.Nil, 0, 0, 0, store.ErrInvalid
	}
	return id, cycle, version, epoch, nil
}

func encodeFarewell(conversationID uuid.UUID, epoch int64, cycle int32) []byte {
	buf := make([]byte, 32)
	copy(buf[:16], conversationID[:])
	binary.BigEndian.PutUint64(buf[16:24], uint64(epoch))
	binary.BigEndian.PutUint32(buf[24:28], uint32(cycle))
	return buf
}

func farewellMatches(sum []byte, conversationID uuid.UUID, epoch int64) bool {
	if len(sum) != 32 || epoch < 1 || conversationID == uuid.Nil {
		return false
	}
	if !bytes.Equal(sum[:16], conversationID[:]) {
		return false
	}
	return int64(binary.BigEndian.Uint64(sum[16:24])) == epoch
}
