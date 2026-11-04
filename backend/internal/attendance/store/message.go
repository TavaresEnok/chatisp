package store

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// AcceptInbound stores one customer message. The same external id returns the
// original row. A missing active attendance is opened in this transaction.
func (r Repo) AcceptInbound(ctx context.Context, tx pgx.Tx, in InboundInput) (InboundResult, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return InboundResult{}, err
	}
	external, ok := text(in.ExternalMessageID, 256)
	if !ok || in.ConnectorID == uuid.Nil || in.TrustedAt.IsZero() {
		return InboundResult{}, ErrInvalid
	}
	body := strings.TrimSpace(in.Body)
	if len(body) > 16000 {
		return InboundResult{}, ErrInvalid
	}
	q := queries(tx)
	found, err := q.GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
		TenantID:          in.TenantID,
		ConnectorID:       in.ConnectorID,
		ExternalMessageID: &external,
	})
	if err == nil {
		conv, err := load(ctx, q, in.TenantID, found.ConversationID)
		if err != nil {
			return InboundResult{}, err
		}
		return InboundResult{Conversation: conv, MessageID: found.ID, Duplicate: true}, nil
	}
	if !isNoRow(err) {
		return InboundResult{}, err
	}
	opened, err := r.Open(ctx, tx, OpenInput{
		TenantID:   in.TenantID,
		InboxID:    in.InboxID,
		ContactID:  in.ContactID,
		IdentityID: in.IdentityID,
		Period:     in.Period,
		OpenedAt:   in.OpenedAt,
	})
	if err != nil {
		return InboundResult{}, err
	}
	conv := opened.Conversation
	if _, err := q.LockConversation(ctx, dbgen.LockConversationParams{TenantID: in.TenantID, ID: conv.ID}); err != nil {
		return InboundResult{}, err
	}
	conv, err = load(ctx, q, in.TenantID, conv.ID)
	if err != nil {
		return InboundResult{}, err
	}
	if err := savepoint(ctx, tx, "store_msg"); err != nil {
		return InboundResult{}, err
	}
	state := "received"
	messageID := uuid.New()
	_, err = q.InsertMessage(ctx, dbgen.InsertMessageParams{
		TenantID:          in.TenantID,
		ID:                messageID,
		ConversationID:    conv.ID,
		ConnectorID:       in.ConnectorID,
		ExternalMessageID: &external,
		Direction:         "inbound",
		Origin:            "customer",
		Visibility:        "public",
		Body:              body,
		LocalState:        &state,
		TrustedAt:         ts(in.TrustedAt),
		Historical:        in.Historical,
	})
	if isUnique(err) {
		rollbackSavepoint(ctx, tx, "store_msg")
		found, err = q.GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
			TenantID:          in.TenantID,
			ConnectorID:       in.ConnectorID,
			ExternalMessageID: &external,
		})
		if err != nil {
			return InboundResult{}, err
		}
		conv, err = load(ctx, q, in.TenantID, found.ConversationID)
		if err != nil {
			return InboundResult{}, err
		}
		return InboundResult{Conversation: conv, MessageID: found.ID, Duplicate: true}, nil
	}
	if err != nil {
		rollbackSavepoint(ctx, tx, "store_msg")
		return InboundResult{}, err
	}
	if err := releaseSavepoint(ctx, tx, "store_msg"); err != nil {
		return InboundResult{}, err
	}
	if _, err := q.LinkPendingStatus(ctx, dbgen.LinkPendingStatusParams{
		MessageID:         &messageID,
		TenantID:          in.TenantID,
		ConnectorID:       in.ConnectorID,
		ExternalMessageID: &external,
	}); err != nil {
		return InboundResult{}, err
	}
	moved := false
	if !in.Historical && (conv.LastCustomerMessageAt.IsZero() || in.TrustedAt.After(conv.LastCustomerMessageAt)) {
		wasWaiting := conv.Status == StatusWaitingCustomer
		clock, err := q.ReceiveCustomerClock(ctx, dbgen.ReceiveCustomerClockParams{
			TrustedAt:       ts(in.TrustedAt),
			TenantID:        in.TenantID,
			ID:              conv.ID,
			ExpectedVersion: conv.Version,
		})
		if err != nil && !isNoRow(err) {
			return InboundResult{}, err
		}
		if err == nil {
			moved = true
			if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
				TenantID:        in.TenantID,
				ID:              uuid.New(),
				ConversationID:  conv.ID,
				CycleNumber:     conv.CycleNumber,
				Kind:            "customer_message",
				At:              ts(in.TrustedAt),
				Version:         clock.Version,
				ControllerEpoch: conv.ControllerEpoch,
				Controller:      conv.Controller,
				AssigneeID:      uuidPtr(conv.AssigneeID),
			}); err != nil {
				return InboundResult{}, err
			}
			if wasWaiting {
				if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
					TenantID:        in.TenantID,
					ID:              uuid.New(),
					ConversationID:  conv.ID,
					CycleNumber:     conv.CycleNumber,
					Kind:            "wait_ended",
					At:              ts(in.TrustedAt),
					Version:         clock.Version,
					ControllerEpoch: conv.ControllerEpoch,
					Controller:      conv.Controller,
					AssigneeID:      uuidPtr(conv.AssigneeID),
				}); err != nil {
					return InboundResult{}, err
				}
			}
		}
	}
	conv, err = load(ctx, q, in.TenantID, conv.ID)
	if err != nil {
		return InboundResult{}, err
	}
	if err := publish(ctx, q, in.TenantID, "conversation.message", "conversation", conv.ID, conv.Version, map[string]any{
		"conversation_id":     conv.ID.String(),
		"message_id":          messageID.String(),
		"external_message_id": external,
		"historical":          in.Historical,
		"clock_moved":         moved,
	}); err != nil {
		return InboundResult{}, err
	}
	return InboundResult{Conversation: conv, MessageID: messageID, CreatedConversation: opened.Created}, nil
}

// EnqueueText writes one idempotency row, one message and one outbox attempt.
// A replay of the same bytes returns that pair. Different bytes conflict.
func (Repo) EnqueueText(ctx context.Context, tx pgx.Tx, in OutboundInput) (OutboundResult, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return OutboundResult{}, err
	}
	operation, ok := text(in.Operation, 64)
	if !ok {
		return OutboundResult{}, ErrInvalid
	}
	requestID, ok := text(in.ClientRequestID, 200)
	if !ok || len(in.Payload) == 0 || len(in.Payload) > 8192 || in.PrincipalID == uuid.Nil || in.At.IsZero() {
		return OutboundResult{}, ErrInvalid
	}
	body, ok := text(in.Body, 16000)
	if !ok {
		if in.AllowEmptyBody && strings.TrimSpace(in.Body) == "" {
			body = ""
			ok = true
		}
	}
	if !ok || in.ConnectorID == uuid.Nil {
		return OutboundResult{}, ErrInvalid
	}
	q := queries(tx)
	conv, err := lock(ctx, q, in.TenantID, in.ConversationID)
	if err != nil {
		return OutboundResult{}, err
	}
	if _, err := q.GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: in.TenantID, ID: in.PrincipalID}); isNoRow(err) {
		return OutboundResult{}, ErrNotFound
	} else if err != nil {
		return OutboundResult{}, err
	}
	sum := requestHash(conv.ID, in.ConnectorID, body, in.Payload)
	if _, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID:        in.TenantID,
		PrincipalID:     in.PrincipalID,
		Operation:       operation,
		ClientRequestID: requestID,
	}); err == nil {
		return replayOutbound(ctx, q, in.TenantID, in.PrincipalID, operation, requestID, sum)
	} else if !isNoRow(err) {
		return OutboundResult{}, err
	}
	if conv.Status == StatusClosed {
		return OutboundResult{}, ErrClosed
	}
	inboxConnector, err := q.GetInboxConnector(ctx, dbgen.GetInboxConnectorParams{TenantID: in.TenantID, ID: conv.InboxID})
	if isNoRow(err) {
		return OutboundResult{}, ErrNotFound
	}
	if err != nil {
		return OutboundResult{}, err
	}
	if inboxConnector != in.ConnectorID {
		return OutboundResult{}, ErrInvalid
	}
	idemID := uuid.New()
	inserted, err := q.InsertIdempotency(ctx, dbgen.InsertIdempotencyParams{
		TenantID:        in.TenantID,
		ID:              idemID,
		PrincipalID:     in.PrincipalID,
		Operation:       operation,
		ClientRequestID: requestID,
		RequestHash:     sum,
	})
	if isNoRow(err) {
		return replayOutbound(ctx, q, in.TenantID, in.PrincipalID, operation, requestID, sum)
	}
	if err != nil {
		return OutboundResult{}, err
	}
	idemID = inserted.ID
	origin := conv.Controller
	if origin != ControllerHuman && origin != ControllerBot && origin != ControllerMenu {
		origin = ControllerMenu
	}
	state := "queued"
	messageID := uuid.New()
	if _, err := q.InsertMessage(ctx, dbgen.InsertMessageParams{
		TenantID:           in.TenantID,
		ID:                 messageID,
		ConversationID:     conv.ID,
		ConnectorID:        in.ConnectorID,
		IdempotencyID:      &idemID,
		Direction:          "outbound",
		Origin:             origin,
		Visibility:         "public",
		Body:               body,
		LocalState:         &state,
		Controller:         &conv.Controller,
		ControllerEpoch:    &conv.ControllerEpoch,
		ClientRequestID:    &requestID,
		AuthorMembershipID: uuidPtr(in.PrincipalID),
		TrustedAt:          ts(in.At),
	}); err != nil {
		return OutboundResult{}, err
	}
	attempt, err := q.InsertOutbound(ctx, dbgen.InsertOutboundParams{
		TenantID:        in.TenantID,
		ID:              uuid.New(),
		MessageID:       messageID,
		ConversationID:  conv.ID,
		IdempotencyID:   &idemID,
		AttemptNo:       1,
		Controller:      conv.Controller,
		ControllerEpoch: conv.ControllerEpoch,
		Origin:          origin,
	})
	if err != nil {
		return OutboundResult{}, err
	}
	if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
		TenantID:        in.TenantID,
		ID:              uuid.New(),
		ConversationID:  conv.ID,
		CycleNumber:     conv.CycleNumber,
		Kind:            "outbound_queued",
		ActorID:         nil,
		At:              ts(in.At),
		Version:         conv.Version,
		ControllerEpoch: conv.ControllerEpoch,
		Controller:      conv.Controller,
		AssigneeID:      uuidPtr(conv.AssigneeID),
	}); err != nil {
		return OutboundResult{}, err
	}
	if err := publish(ctx, q, in.TenantID, "conversation.message", "conversation", conv.ID, conv.Version, map[string]any{
		"conversation_id": conv.ID.String(),
		"message_id":      messageID.String(),
		"attempt_id":      attempt.ID.String(),
	}); err != nil {
		return OutboundResult{}, err
	}
	return OutboundResult{
		MessageID:     messageID,
		AttemptID:     attempt.ID,
		IdempotencyID: idemID,
		State:         attempt.State,
	}, nil
}

func replayOutbound(ctx context.Context, q *dbgen.Queries, tenantID, principal uuid.UUID, operation, requestID string, sum []byte) (OutboundResult, error) {
	row, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID:        tenantID,
		PrincipalID:     principal,
		Operation:       operation,
		ClientRequestID: requestID,
	})
	if err != nil {
		return OutboundResult{}, err
	}
	if !bytes.Equal(row.RequestHash, sum) {
		return OutboundResult{}, ErrIdempotencyConflict
	}
	message, err := q.GetMessageByIdempotency(ctx, dbgen.GetMessageByIdempotencyParams{
		TenantID:      tenantID,
		IdempotencyID: &row.ID,
	})
	if err != nil {
		return OutboundResult{}, err
	}
	attempt, err := q.GetOutboundByIdempotency(ctx, dbgen.GetOutboundByIdempotencyParams{
		TenantID:      tenantID,
		IdempotencyID: &row.ID,
	})
	if err != nil {
		return OutboundResult{}, err
	}
	return OutboundResult{
		MessageID:     message.ID,
		AttemptID:     attempt.ID,
		IdempotencyID: row.ID,
		State:         attempt.State,
		Replayed:      true,
	}, nil
}

// TransitionAttempt moves the outbox. The message row is locked before the
// attempt. The database rejects an automatic resend of an uncertain attempt.
func (Repo) TransitionAttempt(ctx context.Context, tx pgx.Tx, in AttemptTransition) (string, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return "", err
	}
	if in.AttemptID == uuid.Nil || in.To == "" {
		return "", ErrInvalid
	}
	q := queries(tx)
	peek, err := q.PeekOutbound(ctx, dbgen.PeekOutboundParams{TenantID: in.TenantID, ID: in.AttemptID})
	if isNoRow(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := q.LockMessage(ctx, dbgen.LockMessageParams{TenantID: in.TenantID, ID: peek.MessageID}); isNoRow(err) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	current, err := q.LockOutbound(ctx, dbgen.LockOutboundParams{TenantID: in.TenantID, ID: in.AttemptID})
	if isNoRow(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var lease *int64
	if in.LeaseVersion > 0 {
		lease = &in.LeaseVersion
	}
	updated, err := q.UpdateOutboundState(ctx, dbgen.UpdateOutboundStateParams{
		State:        in.To,
		DispatchedAt: ts(in.DispatchedAt),
		DriverRef:    emptyPtr(in.DriverRef),
		ErrorCode:    emptyPtr(in.ErrorCode),
		ErrorMessage: emptyPtr(in.ErrorMessage),
		LeaseVersion: lease,
		TenantID:     in.TenantID,
		ID:           in.AttemptID,
	})
	if uncertain(err) {
		return "", ErrUncertain
	}
	if isCheck(err) {
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	var external *string
	if strings.TrimSpace(in.ExternalMessageID) != "" {
		value := strings.TrimSpace(in.ExternalMessageID)
		external = &value
	}
	if _, err := q.UpdateMessageState(ctx, dbgen.UpdateMessageStateParams{
		LocalState:        &updated.State,
		ExternalMessageID: external,
		TenantID:          in.TenantID,
		ID:                current.MessageID,
	}); err != nil {
		return "", err
	}
	conv, err := load(ctx, q, in.TenantID, current.ConversationID)
	if err != nil {
		return "", err
	}
	fields := map[string]any{
		"conversation_id": current.ConversationID.String(),
		"message_id":      current.MessageID.String(),
		"attempt_id":      in.AttemptID.String(),
		"state":           updated.State,
	}
	if external != nil {
		fields["external_message_id"] = *external
	}
	if err := publish(ctx, q, in.TenantID, "message.attempt", "conversation", current.ConversationID, conv.Version, fields); err != nil {
		return "", err
	}
	return updated.State, nil
}

// OutboundState reads one attempt under the tenant scope.
func (Repo) OutboundState(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID) (string, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return "", err
	}
	row, err := queries(tx).LockOutbound(ctx, dbgen.LockOutboundParams{TenantID: tenantID, ID: attemptID})
	if isNoRow(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return row.State, nil
}

// StatusDedupKey is the documented identity of a status observation.
// An external event id wins. Otherwise the key includes the message, kind,
// trusted time, billing category and billable flag.
func StatusDedupKey(eventID, messageID, kind string, at time.Time, category, billable string) string {
	eventID = strings.TrimSpace(eventID)
	if eventID != "" {
		return "event:" + eventID
	}
	stamp := ""
	if !at.IsZero() {
		stamp = strconv.FormatInt(at.UTC().UnixNano(), 10)
	}
	if billable == "" {
		billable = "unknown"
	}
	return strings.Join([]string{"status", strings.TrimSpace(messageID), kind, stamp, strings.TrimSpace(category), billable}, ":")
}

// RecordStatus stores one status entity. A repeated key returns the original.
// Unknown messages stay pending for reconciliation.
func (Repo) RecordStatus(ctx context.Context, tx pgx.Tx, in StatusInput) (StatusResult, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return StatusResult{}, err
	}
	switch in.Kind {
	case "accepted", "delivered", "read", "failed", "unknown":
	default:
		return StatusResult{}, ErrInvalid
	}
	if in.Billable == "" {
		in.Billable = "unknown"
	}
	if in.Billable != "unknown" && in.Billable != "true" && in.Billable != "false" {
		return StatusResult{}, ErrInvalid
	}
	if in.ConnectorID == uuid.Nil {
		return StatusResult{}, ErrInvalid
	}
	detail := strings.TrimSpace(in.Detail)
	if len(detail) > 500 {
		return StatusResult{}, ErrInvalid
	}
	external := strings.TrimSpace(in.ExternalMessageID)
	q := queries(tx)
	var messageID *uuid.UUID
	var conversationID uuid.UUID
	pending := true
	if external != "" {
		found, err := q.GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
			TenantID:          in.TenantID,
			ConnectorID:       in.ConnectorID,
			ExternalMessageID: &external,
		})
		if err == nil {
			messageID = &found.ID
			conversationID = found.ConversationID
			pending = false
		} else if !isNoRow(err) {
			return StatusResult{}, err
		}
	}
	key := StatusDedupKey(in.ExternalEventID, external, in.Kind, in.TrustedAt, in.BillingCategory, in.Billable)
	var category *string
	if strings.TrimSpace(in.BillingCategory) != "" {
		value := strings.TrimSpace(in.BillingCategory)
		category = &value
	}
	var externalPtr *string
	if external != "" {
		externalPtr = &external
	}
	row, err := q.InsertStatusEvent(ctx, dbgen.InsertStatusEventParams{
		TenantID:          in.TenantID,
		ID:                uuid.New(),
		ConnectorID:       in.ConnectorID,
		MessageID:         messageID,
		ExternalMessageID: externalPtr,
		DedupKey:          key,
		Kind:              in.Kind,
		TrustedAt:         ts(in.TrustedAt),
		BillingCategory:   category,
		Billable:          in.Billable,
		Pending:           pending,
		Detail:            detail,
	})
	if isNoRow(err) {
		existing, err := q.GetStatusByDedup(ctx, dbgen.GetStatusByDedupParams{
			TenantID:    in.TenantID,
			ConnectorID: in.ConnectorID,
			DedupKey:    key,
		})
		if err != nil {
			return StatusResult{}, err
		}
		return StatusResult{ID: existing.ID, MessageID: uuidVal(existing.MessageID), Pending: existing.Pending, Duplicate: true}, nil
	}
	if err != nil {
		return StatusResult{}, err
	}
	fields := map[string]any{
		"status_id": row.ID.String(),
		"kind":      in.Kind,
		"pending":   row.Pending,
	}
	aggregateType := "status"
	aggregateID := row.ID
	version := int64(1)
	if messageID != nil {
		conv, err := load(ctx, q, in.TenantID, conversationID)
		if err != nil {
			return StatusResult{}, err
		}
		aggregateType = "conversation"
		aggregateID = conv.ID
		version = conv.Version
		fields["conversation_id"] = conv.ID.String()
		fields["message_id"] = messageID.String()
	}
	if external != "" {
		fields["external_message_id"] = external
	}
	if err := publish(ctx, q, in.TenantID, "message.status", aggregateType, aggregateID, version, fields); err != nil {
		return StatusResult{}, err
	}
	return StatusResult{ID: row.ID, MessageID: uuidVal(row.MessageID), Pending: row.Pending}, nil
}

func emptyPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
