package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db/dbgen"
)

func (s Service) reopenForInbound(ctx context.Context, tx pgx.Tx, p Principal, in ReceiveInput) (bool, error) {
	if !in.WithinReopenWindow {
		return false, nil
	}
	var id uuid.UUID
	var status string
	var version int64
	err := tx.QueryRow(ctx, `
		SELECT id, status, version
		FROM conversations
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND inbox_id = $2
		  AND contact_identity_id = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, p.TenantID, in.InboxID, in.IdentityID).Scan(&id, &status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != store.StatusClosed {
		return false, nil
	}
	conv, err := lockConversation(ctx, tx, p.TenantID, id)
	if err != nil {
		return false, err
	}
	if conv.Status != store.StatusClosed {
		return false, nil
	}
	active, err := otherActive(ctx, tx, conv)
	if err != nil {
		return false, err
	}
	if active {
		return false, nil
	}
	if _, err := reopenRow(ctx, tx, conv, uuid.Nil, conv.Version, in.TrustedAt); err != nil {
		if errors.Is(err, store.ErrStaleVersion) || errors.Is(err, ErrActiveConversation) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s Service) attachHistorical(ctx context.Context, tx pgx.Tx, p Principal, in ReceiveInput, external string) (store.InboundResult, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM conversations
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND inbox_id = $2
		  AND contact_identity_id = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, p.TenantID, in.InboxID, in.IdentityID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.InboundResult{}, store.ErrNotFound
	}
	if err != nil {
		return store.InboundResult{}, err
	}
	conv, err := lockConversation(ctx, tx, p.TenantID, id)
	if err != nil {
		return store.InboundResult{}, err
	}
	body := strings.TrimSpace(in.Body)
	if len(body) > 16000 {
		return store.InboundResult{}, store.ErrInvalid
	}
	if err := savepoint(ctx, tx, "svc_hist"); err != nil {
		return store.InboundResult{}, err
	}
	state := "received"
	messageID := uuid.New()
	_, err = queries(tx).InsertMessage(ctx, dbgen.InsertMessageParams{
		TenantID: p.TenantID, ID: messageID, ConversationID: conv.ID, ConnectorID: in.ConnectorID,
		ExternalMessageID: &external, Direction: "inbound", Origin: "customer", Visibility: "public",
		Body: body, LocalState: &state, TrustedAt: ts(in.TrustedAt), Historical: true,
	})
	if uniqueViolation(err) {
		rollbackSavepoint(ctx, tx, "svc_hist")
		got, err := s.Repo.AcceptInbound(ctx, tx, inbound(p, in))
		if err != nil {
			return store.InboundResult{}, err
		}
		return got, nil
	}
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_hist")
		return store.InboundResult{}, err
	}
	if err := releaseSavepoint(ctx, tx, "svc_hist"); err != nil {
		return store.InboundResult{}, err
	}
	if err := publish(ctx, queries(tx), p.TenantID, "conversation.message", conv.ID, conv.Version, map[string]any{
		"conversation_id": conv.ID.String(), "message_id": messageID.String(),
		"external_message_id": external, "historical": true, "clock_moved": false,
	}); err != nil {
		return store.InboundResult{}, err
	}
	return store.InboundResult{Conversation: conv, MessageID: messageID}, nil
}

func otherActive(ctx context.Context, tx pgx.Tx, conv store.Conversation) (bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM conversations
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND inbox_id = $2
		  AND contact_identity_id = $3
		  AND status <> 'closed'
		  AND id <> $4
		LIMIT 1
	`, conv.TenantID, conv.InboxID, conv.IdentityID, conv.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func reopenRow(ctx context.Context, tx pgx.Tx, conv store.Conversation, actor uuid.UUID, expectedVersion int64, at time.Time) (store.Conversation, error) {
	if err := savepoint(ctx, tx, "svc_reopen"); err != nil {
		return store.Conversation{}, err
	}
	var version, epoch int64
	var cycle int32
	var protocol, status string
	err := tx.QueryRow(ctx, `
		UPDATE conversations
		SET status = 'open',
		    controller = 'menu',
		    assignee_id = NULL,
		    controller_epoch = controller_epoch + 1,
		    version = version + 1,
		    cycle_number = cycle_number + 1,
		    waiting_since = NULL,
		    updated_at = now()
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
		  AND version = $3
		  AND status = 'closed'
		RETURNING version, controller_epoch, cycle_number, protocol, status
	`, conv.TenantID, conv.ID, expectedVersion).Scan(&version, &epoch, &cycle, &protocol, &status)
	if uniqueViolation(err) {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, ErrActiveConversation
	}
	if errors.Is(err, pgx.ErrNoRows) {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, store.ErrStaleVersion
	}
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, err
	}
	q := queries(tx)
	if err := q.InsertCycle(ctx, dbgen.InsertCycleParams{
		TenantID: conv.TenantID, ID: uuid.New(), ConversationID: conv.ID, CycleNumber: cycle, OpenedAt: ts(at),
	}); err != nil {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, err
	}
	if err := cancelEpoch(ctx, q, conv.TenantID, conv.ID, conv.ControllerEpoch, version); err != nil {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, err
	}
	if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
		TenantID: conv.TenantID, ID: uuid.New(), ConversationID: conv.ID, CycleNumber: cycle,
		Kind: "reopened", ActorID: uuidPtr(actor), At: ts(at), Version: version,
		ControllerEpoch: epoch, Controller: store.ControllerMenu,
	}); err != nil {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, err
	}
	if err := publish(ctx, q, conv.TenantID, "conversation.reopened", conv.ID, version, map[string]any{
		"conversation_id": conv.ID.String(), "protocol": protocol,
	}); err != nil {
		rollbackSavepoint(ctx, tx, "svc_reopen")
		return store.Conversation{}, err
	}
	if err := releaseSavepoint(ctx, tx, "svc_reopen"); err != nil {
		return store.Conversation{}, err
	}
	return loadConversation(ctx, tx, conv.TenantID, conv.ID)
}

func cancelEpoch(ctx context.Context, q *dbgen.Queries, tenantID, conversationID uuid.UUID, epoch, version int64) error {
	if _, err := q.LockQueuedAutomaticMessages(ctx, dbgen.LockQueuedAutomaticMessagesParams{
		TenantID: tenantID, ConversationID: conversationID, ControllerEpoch: epoch,
	}); err != nil {
		return err
	}
	rows, err := q.CancelQueuedAutomatic(ctx, dbgen.CancelQueuedAutomaticParams{
		TenantID: tenantID, ConversationID: conversationID, ControllerEpoch: epoch,
	})
	if err != nil {
		return err
	}
	state := "cancelled"
	for _, row := range rows {
		n, err := q.UpdateMessageState(ctx, dbgen.UpdateMessageStateParams{
			LocalState: &state, TenantID: tenantID, ID: row.MessageID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrNotFound
		}
		if err := publish(ctx, q, tenantID, "message.attempt", conversationID, version, map[string]any{
			"conversation_id": conversationID.String(), "message_id": row.MessageID.String(),
			"attempt_id": row.ID.String(), "state": state,
		}); err != nil {
			return err
		}
	}
	return nil
}
