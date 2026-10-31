package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// Open reuses the active attendance for the identity or inserts one.
// The protocol counter and the conversation lock follow the documented order.
// A concurrent insert loses on the unique index and returns the winner.
func (Repo) Open(ctx context.Context, tx pgx.Tx, in OpenInput) (OpenResult, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return OpenResult{}, err
	}
	if in.InboxID == uuid.Nil || in.ContactID == uuid.Nil || in.IdentityID == uuid.Nil || in.OpenedAt.IsZero() || !validPeriod(in.Period) {
		return OpenResult{}, ErrInvalid
	}
	if in.Status == "" {
		in.Status = StatusOpen
	}
	if in.Status != StatusOpen && in.Status != StatusTriage {
		return OpenResult{}, ErrInvalid
	}
	q := queries(tx)
	existing, err := q.LockActiveConversation(ctx, dbgen.LockActiveConversationParams{
		TenantID:          in.TenantID,
		InboxID:           uuidPtr(in.InboxID),
		ContactIdentityID: uuidPtr(in.IdentityID),
	})
	if err == nil {
		conv, err := load(ctx, q, in.TenantID, existing)
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Conversation: conv}, nil
	}
	if !isNoRow(err) {
		return OpenResult{}, err
	}
	if err := savepoint(ctx, tx, "store_open"); err != nil {
		return OpenResult{}, err
	}
	n, err := q.AllocateProtocol(ctx, dbgen.AllocateProtocolParams{TenantID: in.TenantID, Period: in.Period})
	if err != nil {
		rollbackSavepoint(ctx, tx, "store_open")
		return OpenResult{}, err
	}
	protocol := formatProtocol(in.Period, n)
	inserted, err := q.InsertOperationalConversation(ctx, dbgen.InsertOperationalConversationParams{
		ID:                uuid.New(),
		TenantID:          in.TenantID,
		InboxID:           uuidPtr(in.InboxID),
		ContactID:         uuidPtr(in.ContactID),
		ContactIdentityID: uuidPtr(in.IdentityID),
		Status:            in.Status,
		Protocol:          &protocol,
		Period:            &in.Period,
	})
	if isUnique(err) {
		rollbackSavepoint(ctx, tx, "store_open")
		existing, err = q.LockActiveConversation(ctx, dbgen.LockActiveConversationParams{
			TenantID:          in.TenantID,
			InboxID:           uuidPtr(in.InboxID),
			ContactIdentityID: uuidPtr(in.IdentityID),
		})
		if err != nil {
			if isNoRow(err) {
				return OpenResult{}, ErrNotFound
			}
			return OpenResult{}, err
		}
		conv, err := load(ctx, q, in.TenantID, existing)
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Conversation: conv}, nil
	}
	if err != nil {
		rollbackSavepoint(ctx, tx, "store_open")
		if incompatibleBinding(err) {
			return OpenResult{}, ErrInvalid
		}
		return OpenResult{}, err
	}
	if err := q.InsertCycle(ctx, dbgen.InsertCycleParams{
		TenantID:       in.TenantID,
		ID:             uuid.New(),
		ConversationID: inserted.ID,
		CycleNumber:    1,
		OpenedAt:       ts(in.OpenedAt),
	}); err != nil {
		rollbackSavepoint(ctx, tx, "store_open")
		return OpenResult{}, err
	}
	if err := releaseSavepoint(ctx, tx, "store_open"); err != nil {
		return OpenResult{}, err
	}
	conv, err := load(ctx, q, in.TenantID, inserted.ID)
	if err != nil {
		return OpenResult{}, err
	}
	if err := publish(ctx, q, in.TenantID, "conversation.opened", "conversation", conv.ID, conv.Version, map[string]any{
		"conversation_id": conv.ID.String(),
	}); err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Conversation: conv, Created: true}, nil
}

// Assume moves the current version to a human. The loser of the version check
// gets ErrStaleVersion. Queued bot or menu sends of the previous epoch are cancelled.
func (Repo) Assume(ctx context.Context, tx pgx.Tx, in AssumeInput) (Conversation, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return Conversation{}, err
	}
	if in.ConversationID == uuid.Nil || in.MembershipID == uuid.Nil || in.At.IsZero() || in.ExpectedVersion < 1 {
		return Conversation{}, ErrInvalid
	}
	q := queries(tx)
	current, err := lock(ctx, q, in.TenantID, in.ConversationID)
	if err != nil {
		return Conversation{}, err
	}
	if current.Version != in.ExpectedVersion {
		return Conversation{}, ErrStaleVersion
	}
	if current.Status == StatusClosed {
		return Conversation{}, ErrClosed
	}
	if current.Controller == ControllerHuman && current.AssigneeID == in.MembershipID && current.Status != StatusTriage {
		return current, nil
	}
	actor, err := q.GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: in.TenantID, ID: in.MembershipID})
	if isNoRow(err) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	// The conversation is already locked. Serialize every increase in this
	// member's active assignments with transfers and capacity changes.
	if current.AssigneeID != in.MembershipID || (current.Status != StatusOpen && current.Status != StatusWaitingCustomer) {
		if err := LockAssignmentCapacity(ctx, tx, in.TenantID, in.MembershipID, in.ConversationID); err != nil {
			return Conversation{}, err
		}
	}
	prevEpoch := current.ControllerEpoch
	row, err := q.AssumeConversation(ctx, dbgen.AssumeConversationParams{
		AssigneeID:      uuidPtr(in.MembershipID),
		TenantID:        in.TenantID,
		ID:              in.ConversationID,
		ExpectedVersion: in.ExpectedVersion,
	})
	if isNoRow(err) {
		return Conversation{}, ErrStaleVersion
	}
	if err != nil {
		return Conversation{}, err
	}
	if row.ControllerEpoch != prevEpoch {
		if err := cancelQueuedAutomatic(ctx, q, in.TenantID, in.ConversationID, prevEpoch, row.Version); err != nil {
			return Conversation{}, err
		}
	}
	if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
		TenantID:        in.TenantID,
		ID:              uuid.New(),
		ConversationID:  in.ConversationID,
		CycleNumber:     current.CycleNumber,
		Kind:            "assumed",
		ActorID:         &actor,
		At:              ts(in.At),
		Version:         row.Version,
		ControllerEpoch: row.ControllerEpoch,
		Controller:      ControllerHuman,
		AssigneeID:      uuidPtr(in.MembershipID),
	}); err != nil {
		return Conversation{}, err
	}
	if err := publish(ctx, q, in.TenantID, "conversation.assumed", "conversation", in.ConversationID, row.Version, map[string]any{
		"conversation_id": in.ConversationID.String(),
		"membership_id":   in.MembershipID.String(),
	}); err != nil {
		return Conversation{}, err
	}
	return load(ctx, q, in.TenantID, in.ConversationID)
}

// Close stores reason, result, summary, author and the cycle. Customer clocks stay.
// no_response is stored as itself and is not a confirmed solution.
func (Repo) Close(ctx context.Context, tx pgx.Tx, in CloseInput) (Conversation, Closure, error) {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return Conversation{}, Closure{}, err
	}
	reason, ok := text(in.Reason, 500)
	if !ok || in.At.IsZero() || in.ExpectedVersion < 1 || in.MembershipID == uuid.Nil {
		return Conversation{}, Closure{}, ErrInvalid
	}
	summary, ok := text(in.Summary, 4000)
	if !ok || !validResult(in.Result) || (in.AuthorKind != ControllerHuman && in.AuthorKind != ControllerBot) {
		return Conversation{}, Closure{}, ErrInvalid
	}
	q := queries(tx)
	current, err := lock(ctx, q, in.TenantID, in.ConversationID)
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	if current.Version != in.ExpectedVersion {
		return Conversation{}, Closure{}, ErrStaleVersion
	}
	if current.Status == StatusClosed {
		return Conversation{}, Closure{}, ErrClosed
	}
	actor, err := q.GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: in.TenantID, ID: in.MembershipID})
	if isNoRow(err) {
		return Conversation{}, Closure{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	prevEpoch := current.ControllerEpoch
	closed, err := q.CloseConversation(ctx, dbgen.CloseConversationParams{
		Summary:         summary,
		TenantID:        in.TenantID,
		ID:              in.ConversationID,
		ExpectedVersion: in.ExpectedVersion,
	})
	if isNoRow(err) {
		return Conversation{}, Closure{}, ErrStaleVersion
	}
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	var external *string
	if current.ExternalProtocol != "" {
		external = &current.ExternalProtocol
	}
	n, err := q.CloseCycle(ctx, dbgen.CloseCycleParams{
		ClosedAt:          ts(in.At),
		Reason:            &reason,
		Result:            &in.Result,
		Summary:           &summary,
		AuthorID:          &actor,
		MembershipID:      uuidPtr(in.MembershipID),
		AuthorKind:        &in.AuthorKind,
		ExternalReference: external,
		TenantID:          in.TenantID,
		ConversationID:    in.ConversationID,
		CycleNumber:       closed.CycleNumber,
	})
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	if n != 1 {
		return Conversation{}, Closure{}, ErrInvalid
	}
	if err := cancelQueuedAutomatic(ctx, q, in.TenantID, in.ConversationID, prevEpoch, closed.Version); err != nil {
		return Conversation{}, Closure{}, err
	}
	if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
		TenantID:        in.TenantID,
		ID:              uuid.New(),
		ConversationID:  in.ConversationID,
		CycleNumber:     closed.CycleNumber,
		Kind:            "closed",
		ActorID:         &actor,
		At:              ts(in.At),
		Version:         closed.Version,
		ControllerEpoch: closed.ControllerEpoch,
		Controller:      current.Controller,
		AssigneeID:      uuidPtr(current.AssigneeID),
	}); err != nil {
		return Conversation{}, Closure{}, err
	}
	if err := publish(ctx, q, in.TenantID, "conversation.closed", "conversation", in.ConversationID, closed.Version, map[string]any{
		"conversation_id": in.ConversationID.String(),
		"result":          in.Result,
	}); err != nil {
		return Conversation{}, Closure{}, err
	}
	conv, err := load(ctx, q, in.TenantID, in.ConversationID)
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	closure, err := q.GetCycleClosure(ctx, dbgen.GetCycleClosureParams{
		TenantID:       in.TenantID,
		ConversationID: in.ConversationID,
		CycleNumber:    closed.CycleNumber,
	})
	if err != nil {
		return Conversation{}, Closure{}, err
	}
	return conv, Closure{
		CycleNumber:       closure.CycleNumber,
		Reason:            strVal(closure.CloseReason),
		Result:            strVal(closure.CloseResult),
		Summary:           strVal(closure.CloseSummary),
		AuthorID:          uuidVal(closure.CloseAuthorID),
		AuthorKind:        strVal(closure.CloseAuthorKind),
		At:                timeOf(closure.ClosedAt),
		ExternalReference: strVal(closure.ExternalReference),
	}, nil
}

// AttachExternalProtocol stores one ERP reference. A different value is rejected.
func (Repo) AttachExternalProtocol(ctx context.Context, tx pgx.Tx, tenantID, conversationID uuid.UUID, expectedVersion int64, ref string) (Conversation, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Conversation{}, err
	}
	ref, ok := text(ref, 128)
	if !ok || expectedVersion < 1 {
		return Conversation{}, ErrInvalid
	}
	q := queries(tx)
	current, err := lock(ctx, q, tenantID, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if current.Version != expectedVersion {
		return Conversation{}, ErrStaleVersion
	}
	if current.ExternalProtocol == ref {
		return current, nil
	}
	if current.ExternalProtocol != "" {
		return Conversation{}, ErrInvalid
	}
	attached, err := q.AttachExternalProtocol(ctx, dbgen.AttachExternalProtocolParams{
		ExternalProtocol: &ref,
		TenantID:         tenantID,
		ID:               conversationID,
		ExpectedVersion:  expectedVersion,
	})
	if isNoRow(err) {
		return Conversation{}, ErrStaleVersion
	}
	if err != nil {
		return Conversation{}, err
	}
	if err := publish(ctx, q, tenantID, "conversation.external_protocol", "conversation", conversationID, attached.Version, map[string]any{
		"conversation_id": conversationID.String(),
	}); err != nil {
		return Conversation{}, err
	}
	return load(ctx, q, tenantID, conversationID)
}

// NoteAcceptedReply starts waiting_customer for a documented human acceptance.
// A customer message after that acceptance leaves the attendance open.
func (Repo) NoteAcceptedReply(ctx context.Context, tx pgx.Tx, tenantID, conversationID uuid.UUID, expectedVersion int64, acceptedAt time.Time) (Conversation, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Conversation{}, err
	}
	if acceptedAt.IsZero() || expectedVersion < 1 {
		return Conversation{}, ErrInvalid
	}
	q := queries(tx)
	current, err := lock(ctx, q, tenantID, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if current.Version != expectedVersion {
		return Conversation{}, ErrStaleVersion
	}
	if current.Status == StatusClosed {
		return Conversation{}, ErrClosed
	}
	if current.Status != StatusOpen && current.Status != StatusWaitingCustomer {
		return current, nil
	}
	if current.Status == StatusWaitingCustomer && !current.WaitingSince.IsZero() && !acceptedAt.After(current.WaitingSince) {
		return current, nil
	}
	if !current.LastCustomerMessageAt.IsZero() && acceptedAt.Before(current.LastCustomerMessageAt) {
		return current, nil
	}
	row, err := q.MarkWaitingCustomer(ctx, dbgen.MarkWaitingCustomerParams{
		AcceptedAt:      ts(acceptedAt),
		TenantID:        tenantID,
		ID:              conversationID,
		ExpectedVersion: expectedVersion,
	})
	if isNoRow(err) {
		return current, nil
	}
	if err != nil {
		return Conversation{}, err
	}
	if current.Status != StatusWaitingCustomer {
		if err := q.InsertTimeline(ctx, dbgen.InsertTimelineParams{
			TenantID:        tenantID,
			ID:              uuid.New(),
			ConversationID:  conversationID,
			CycleNumber:     current.CycleNumber,
			Kind:            "wait_started",
			At:              ts(acceptedAt),
			Version:         row.Version,
			ControllerEpoch: current.ControllerEpoch,
			Controller:      current.Controller,
			AssigneeID:      uuidPtr(current.AssigneeID),
		}); err != nil {
			return Conversation{}, err
		}
	}
	if err := publish(ctx, q, tenantID, "conversation.waiting", "conversation", conversationID, row.Version, map[string]any{
		"conversation_id": conversationID.String(),
	}); err != nil {
		return Conversation{}, err
	}
	return load(ctx, q, tenantID, conversationID)
}

// cancelQueuedAutomatic drops queued bot and menu sends from one epoch.
// Messages are locked before attempts. Each cancelled attempt updates the
// message and publishes message.attempt before the caller publishes its own event.
func cancelQueuedAutomatic(ctx context.Context, q *dbgen.Queries, tenantID, conversationID uuid.UUID, epoch, version int64) error {
	if _, err := q.LockQueuedAutomaticMessages(ctx, dbgen.LockQueuedAutomaticMessagesParams{
		TenantID:        tenantID,
		ConversationID:  conversationID,
		ControllerEpoch: epoch,
	}); err != nil {
		return err
	}
	rows, err := q.CancelQueuedAutomatic(ctx, dbgen.CancelQueuedAutomaticParams{
		TenantID:        tenantID,
		ConversationID:  conversationID,
		ControllerEpoch: epoch,
	})
	if err != nil {
		return err
	}
	state := "cancelled"
	for _, row := range rows {
		n, err := q.UpdateMessageState(ctx, dbgen.UpdateMessageStateParams{
			LocalState: &state,
			TenantID:   tenantID,
			ID:         row.MessageID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		if err := publish(ctx, q, tenantID, "message.attempt", "conversation", conversationID, version, map[string]any{
			"conversation_id": conversationID.String(),
			"message_id":      row.MessageID.String(),
			"attempt_id":      row.ID.String(),
			"state":           state,
		}); err != nil {
			return err
		}
	}
	return nil
}

func lock(ctx context.Context, q *dbgen.Queries, tenantID, id uuid.UUID) (Conversation, error) {
	if _, err := q.LockConversation(ctx, dbgen.LockConversationParams{TenantID: tenantID, ID: id}); isNoRow(err) {
		return Conversation{}, ErrNotFound
	} else if err != nil {
		return Conversation{}, err
	}
	return load(ctx, q, tenantID, id)
}

func load(ctx context.Context, q *dbgen.Queries, tenantID, id uuid.UUID) (Conversation, error) {
	row, err := q.GetConversation(ctx, dbgen.GetConversationParams{TenantID: tenantID, ID: id})
	if isNoRow(err) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	return conversationFrom(row), nil
}

func publish(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, eventType, aggregateType string, aggregate uuid.UUID, version int64, fields map[string]any) error {
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
		TenantID:      tenantID,
		Seq:           seq,
		ID:            uuid.New(),
		EventType:     eventType,
		AggregateType: aggregateType,
		AggregateID:   aggregate,
		Version:       version,
		Payload:       payload,
	})
}
