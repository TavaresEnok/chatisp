package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/conversation"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/db/dbgen"
	"chatisp/internal/sectors"
)

// Service orchestrates attendance commands. Repo is stateless.
type Service struct {
	DB   *db.Store
	Repo store.Repo
	Jobs Queuer
}

// New binds the platform store and the outbound job inserter.
func New(database *db.Store, queuer Queuer) Service {
	return Service{DB: database, Repo: store.Repo{}, Jobs: queuer}
}

// List returns the inbox queue and the cursor of the examined range.
func (s Service) List(ctx context.Context, p Principal, inboxID uuid.UUID) (store.Snapshot, error) {
	return s.Snapshot(ctx, p, store.SnapshotInput{TenantID: p.TenantID, InboxID: inboxID})
}

// Snapshot reads the queue and, when set, one conversation's messages.
func (s Service) Snapshot(ctx context.Context, p Principal, in store.SnapshotInput) (store.Snapshot, error) {
	var out store.Snapshot
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, false); err != nil {
			return err
		}
		ceiling, err := store.CursorCeiling(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if in.ConversationID != uuid.Nil {
			if err := requireConversation(ctx, tx, p, in.InboxID, in.ConversationID); err != nil {
				return err
			}
			conv, err := loadConversation(ctx, tx, p.TenantID, in.ConversationID)
			if err != nil {
				return err
			}
			if conv.InboxID != in.InboxID {
				return store.ErrNotFound
			}
		}
		ids, err := sectors.AuthorizedConversationIDs(ctx, tx, sectorPrincipal(p), in.InboxID)
		if err != nil {
			return err
		}
		snap, err := s.Repo.Snapshot(ctx, tx, store.SnapshotInput{
			TenantID: p.TenantID, InboxID: in.InboxID, ConversationID: in.ConversationID, AuthorizedIDs: ids, CursorCeiling: &ceiling,
		})
		if err != nil {
			return err
		}
		out = snap
		return nil
	})
	return out, err
}

// History returns messages and the authored timeline for one conversation.
func (s Service) History(ctx context.Context, p Principal, inboxID, conversationID uuid.UUID) (History, error) {
	var out History
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inboxID, false); err != nil {
			return err
		}
		ceiling, err := store.CursorCeiling(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inboxID, conversationID); err != nil {
			return err
		}
		ids, err := sectors.AuthorizedConversationIDs(ctx, tx, sectorPrincipal(p), inboxID)
		if err != nil {
			return err
		}
		conv, err := loadConversation(ctx, tx, p.TenantID, conversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != inboxID {
			return store.ErrNotFound
		}
		snap, err := s.Repo.Snapshot(ctx, tx, store.SnapshotInput{
			TenantID: p.TenantID, InboxID: inboxID, ConversationID: conversationID, AuthorizedIDs: ids, CursorCeiling: &ceiling,
		})
		if err != nil {
			return err
		}
		rows, err := queries(tx).ListTimeline(ctx, dbgen.ListTimelineParams{
			TenantID: p.TenantID, ConversationID: conversationID,
		})
		if err != nil {
			return err
		}
		out = History{NextCursor: snap.NextCursor, Messages: snap.Messages, Timeline: timelineFrom(rows)}
		for _, head := range snap.Conversations {
			if head.ID == conversationID {
				out.Head = head
				break
			}
		}
		return nil
	})
	return out, err
}

// Events returns the authorized realtime page after the cursor.
func (s Service) Events(ctx context.Context, p Principal, inboxID uuid.UUID, after int64) (store.EventPage, error) {
	var page store.EventPage
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inboxID, false); err != nil {
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
		got, err := s.Repo.ReadAuthorizedEvents(ctx, tx, p.TenantID, after, ids, ceiling)
		if err != nil {
			return err
		}
		page = got
		return nil
	})
	return page, err
}

// ReceiveText stores one customer text. The same external id returns the original
// message. Two concurrent first texts share one active conversation and protocol.
func (s Service) ReceiveText(ctx context.Context, p Principal, in ReceiveInput) (ReceiveResult, error) {
	var out ReceiveResult
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, false); err != nil {
			return err
		}
		external := strings.TrimSpace(in.ExternalMessageID)
		if external == "" || in.ConnectorID == uuid.Nil || in.TrustedAt.IsZero() {
			return store.ErrInvalid
		}
		q := queries(tx)
		if _, err := q.GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
			TenantID: p.TenantID, ConnectorID: in.ConnectorID, ExternalMessageID: &external,
		}); err == nil {
			got, err := s.Repo.AcceptInbound(ctx, tx, inbound(p, in))
			if err != nil {
				return err
			}
			if err := requireConversation(ctx, tx, p, in.InboxID, got.Conversation.ID); err != nil {
				return err
			}
			out.InboundResult = got
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.Historical {
			got, err := s.attachHistorical(ctx, tx, p, in, external)
			if err != nil {
				return err
			}
			if err := requireConversation(ctx, tx, p, in.InboxID, got.Conversation.ID); err != nil {
				return err
			}
			out.InboundResult = got
			return nil
		}
		var contactID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM contacts WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, in.ContactID).Scan(&contactID); err != nil {
			return err
		}
		blocked, err := contactdirectory.IsBlocked(ctx, tx, p.TenantID, contactID)
		if err != nil {
			return err
		}
		if blocked {
			return ErrForbidden
		}
		reopened, err := s.reopenForInbound(ctx, tx, p, in)
		if err != nil {
			return err
		}
		got, err := s.Repo.AcceptInbound(ctx, tx, inbound(p, in))
		if err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, in.InboxID, got.Conversation.ID); err != nil {
			return err
		}
		out = ReceiveResult{InboundResult: got, Reopened: reopened && !got.Duplicate}
		return nil
	})
	return out, err
}

func inbound(p Principal, in ReceiveInput) store.InboundInput {
	return store.InboundInput{
		TenantID: p.TenantID, InboxID: in.InboxID, ContactID: in.ContactID, IdentityID: in.IdentityID,
		ConnectorID: in.ConnectorID, ExternalMessageID: in.ExternalMessageID, Body: in.Body,
		Period: in.Period, TrustedAt: in.TrustedAt, OpenedAt: in.OpenedAt, Historical: in.Historical,
	}
}

// Assume hands the current version to the principal. The loser gets ErrStaleVersion.
func (s Service) Assume(ctx context.Context, p Principal, inboxID, conversationID uuid.UUID, expectedVersion int64, at time.Time) (store.Conversation, error) {
	var out store.Conversation
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inboxID, false); err != nil {
			return err
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, inboxID, conversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != inboxID {
			return store.ErrNotFound
		}
		if conv.Status == store.StatusClosed {
			return store.ErrClosed
		}
		userID, err := queries(tx).GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: p.TenantID, ID: p.MembershipID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if _, _, _, err := conversation.Assume(modelFrom(conv), nil, conversation.AssumeCommand{
			ExpectedVersion: expectedVersion, AssigneeID: p.MembershipID, ActorID: userID, At: at,
		}); err != nil {
			return mapModel(err)
		}
		out, err = s.Repo.Assume(ctx, tx, store.AssumeInput{
			TenantID: p.TenantID, ConversationID: conversationID, MembershipID: p.MembershipID,
			ExpectedVersion: expectedVersion, At: at,
		})
		return err
	})
	return out, err
}

// Send enqueues one human text, its outbox row and its River job in this transaction.
func (s Service) Send(ctx context.Context, p Principal, in SendInput) (SendResult, error) {
	var out SendResult
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, true); err != nil {
			return err
		}
		body := strings.TrimSpace(in.Body)
		requestID := strings.TrimSpace(in.ClientRequestID)
		if body == "" || requestID == "" || in.At.IsZero() || in.ExpectedVersion < 1 {
			return store.ErrInvalid
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != in.InboxID {
			return store.ErrNotFound
		}
		connector, err := queries(tx).GetInboxConnector(ctx, dbgen.GetInboxConnectorParams{TenantID: p.TenantID, ID: conv.InboxID})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		payload, err := canonicalSend(conv.ID.String(), connector.String(), body)
		if err != nil {
			return err
		}
		replay, err := queuedAlready(ctx, queries(tx), p, requestID)
		if err != nil {
			return err
		}
		if !replay {
			if conv.Version != in.ExpectedVersion {
				return store.ErrStaleVersion
			}
			if conv.Status == store.StatusClosed {
				return store.ErrClosed
			}
			if conv.Controller != store.ControllerHuman || conv.AssigneeID != p.MembershipID {
				return ErrForbidden
			}
		}
		sent, err := s.Repo.EnqueueText(ctx, tx, store.OutboundInput{
			TenantID: p.TenantID, ConversationID: conv.ID, ConnectorID: connector, PrincipalID: p.MembershipID,
			Operation: OpSendText, ClientRequestID: requestID, Payload: payload, Body: body, At: in.At,
		})
		if err != nil {
			return err
		}
		out.OutboundResult = sent
		if !sent.Replayed {
			id, err := enqueueJob(ctx, s.Jobs, tx, p.TenantID, sent.AttemptID)
			if err != nil {
				return err
			}
			out.JobID = id
		}
		return nil
	})
	return out, err
}

func queuedAlready(ctx context.Context, q *dbgen.Queries, p Principal, requestID string) (bool, error) {
	_, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: p.TenantID, PrincipalID: p.MembershipID, Operation: OpSendText, ClientRequestID: requestID,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// Close stores the outcome and, when asked, a human farewell on the outbox.
// The farewell uses a savepoint: its failure is visible and the close remains.
// Replay returns the cycle this key closed, not a later cycle. The farewell
// is marked so a worker can reserve it after the epoch bump.
func (s Service) Close(ctx context.Context, p Principal, in CloseInput) (CloseResult, error) {
	var out CloseResult
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, false); err != nil {
			return err
		}
		in.Reason = strings.TrimSpace(in.Reason)
		in.Result = strings.TrimSpace(in.Result)
		in.Summary = strings.TrimSpace(in.Summary)
		in.ClientRequestID = strings.TrimSpace(in.ClientRequestID)
		in.FarewellBody = strings.TrimSpace(in.FarewellBody)
		in.FarewellRequestID = strings.TrimSpace(in.FarewellRequestID)
		if in.ClientRequestID == "" || in.At.IsZero() || in.ExpectedVersion < 1 {
			return store.ErrInvalid
		}
		if in.FarewellBody != "" && in.FarewellRequestID == "" {
			return store.ErrInvalid
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != in.InboxID {
			return store.ErrNotFound
		}
		replay, err := claim(ctx, queries(tx), p.TenantID, p.MembershipID, OpClose, in.ClientRequestID, closeHash(in))
		if err != nil {
			return err
		}
		if replay {
			cycle, version, epoch, err := readCycle(ctx, queries(tx), p, opCloseCycle, in.ClientRequestID, in.ConversationID)
			if err != nil {
				return err
			}
			closure, err := queries(tx).GetCycleClosure(ctx, dbgen.GetCycleClosureParams{
				TenantID: p.TenantID, ConversationID: in.ConversationID, CycleNumber: cycle,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			if err != nil {
				return err
			}
			out.Closure = closureFrom(closure)
			out.Conversation, err = projectCycle(ctx, tx, p.TenantID, in.ConversationID, cycle, version, epoch, "closed", out.Closure.Summary)
			if err != nil {
				return err
			}
			if in.FarewellRequestID != "" {
				if farewell, err := s.replayFarewell(ctx, tx, p, conv, in); err != nil {
					return err
				} else if farewell != nil {
					out.Farewell = farewell
				}
			}
			return nil
		}
		if conv.Status == store.StatusClosed {
			return store.ErrClosed
		}
		userID, err := queries(tx).GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: p.TenantID, ID: p.MembershipID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if _, _, _, err := conversation.Close(modelFrom(conv), nil, conversation.CloseCommand{
			ExpectedVersion: in.ExpectedVersion, ActorID: userID, ActorKind: conversation.ControllerHuman,
			Reason: in.Reason, Result: conversation.CloseResult(in.Result), Summary: in.Summary, At: in.At,
		}); err != nil {
			return mapModel(err)
		}
		if in.FarewellBody != "" {
			if conv.Controller != store.ControllerHuman || conv.AssigneeID != p.MembershipID {
				return ErrForbidden
			}
			if err := s.connectorReady(ctx, tx, p.TenantID, conv.InboxID); err != nil {
				out.FarewellErr = err
			} else {
				farewell, ferr := s.queueFarewell(ctx, tx, p, conv, in)
				out.Farewell = farewell
				out.FarewellErr = ferr
			}
		}
		closed, closure, err := s.Repo.Close(ctx, tx, store.CloseInput{
			TenantID: p.TenantID, ConversationID: in.ConversationID, MembershipID: p.MembershipID,
			ExpectedVersion: in.ExpectedVersion, Reason: in.Reason, Result: in.Result, Summary: in.Summary,
			AuthorKind: store.ControllerHuman, At: in.At,
		})
		if err != nil {
			return err
		}
		out.Conversation = closed
		out.Closure = closure
		return bindCycle(ctx, queries(tx), p, opCloseCycle, in.ClientRequestID, closed.ID, closure.CycleNumber, closed.Version, closed.ControllerEpoch)
	})
	return out, err
}

// Reopen keeps the protocol and appends a cycle when no other active attendance exists.
// Replay returns the cycle this key opened, even if a later reopen moved the row.
func (s Service) Reopen(ctx context.Context, p Principal, in ReopenInput) (store.Conversation, error) {
	var out store.Conversation
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, false); err != nil {
			return err
		}
		in.ClientRequestID = strings.TrimSpace(in.ClientRequestID)
		if in.ClientRequestID == "" || in.At.IsZero() || in.ExpectedVersion < 1 {
			return store.ErrInvalid
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != in.InboxID {
			return store.ErrNotFound
		}
		replay, err := claim(ctx, queries(tx), p.TenantID, p.MembershipID, OpReopen, in.ClientRequestID, reopenHash(in))
		if err != nil {
			return err
		}
		if replay {
			cycle, version, epoch, err := readCycle(ctx, queries(tx), p, opReopenCycle, in.ClientRequestID, in.ConversationID)
			if err != nil {
				return err
			}
			out, err = projectCycle(ctx, tx, p.TenantID, in.ConversationID, cycle, version, epoch, "reopened", "")
			return err
		}
		userID, err := queries(tx).GetMembershipUser(ctx, dbgen.GetMembershipUserParams{TenantID: p.TenantID, ID: p.MembershipID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		active, err := otherActive(ctx, tx, conv)
		if err != nil {
			return err
		}
		if _, _, err := conversation.Reopen(modelFrom(conv), conversation.ReopenCommand{
			ExpectedVersion: in.ExpectedVersion, ActorID: userID, At: in.At, OtherActiveExists: active,
		}); err != nil {
			return mapModel(err)
		}
		out, err = reopenRow(ctx, tx, conv, userID, in.ExpectedVersion, in.At)
		if err != nil {
			return err
		}
		return bindCycle(ctx, queries(tx), p, opReopenCycle, in.ClientRequestID, out.ID, out.CycleNumber, out.Version, out.ControllerEpoch)
	})
	return out, err
}

func (s Service) queueFarewell(ctx context.Context, tx pgx.Tx, p Principal, conv store.Conversation, in CloseInput) (*Farewell, error) {
	if err := savepoint(ctx, tx, "svc_farewell"); err != nil {
		return nil, err
	}
	connector, err := queries(tx).GetInboxConnector(ctx, dbgen.GetInboxConnectorParams{TenantID: p.TenantID, ID: conv.InboxID})
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_farewell")
		return nil, err
	}
	payload, err := canonicalSend(conv.ID.String(), connector.String(), in.FarewellBody)
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_farewell")
		return nil, err
	}
	sent, err := s.Repo.EnqueueText(ctx, tx, store.OutboundInput{
		TenantID: p.TenantID, ConversationID: conv.ID, ConnectorID: connector, PrincipalID: p.MembershipID,
		Operation: OpSendText, ClientRequestID: in.FarewellRequestID, Payload: payload, Body: in.FarewellBody, At: in.At,
	})
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_farewell")
		return nil, err
	}
	var jobID int64
	if !sent.Replayed {
		id, err := enqueueJob(ctx, s.Jobs, tx, p.TenantID, sent.AttemptID)
		if err != nil {
			rollbackSavepoint(ctx, tx, "svc_farewell")
			return nil, err
		}
		jobID = id
	}
	if err := bindFarewell(ctx, queries(tx), p, conv, sent.AttemptID); err != nil {
		rollbackSavepoint(ctx, tx, "svc_farewell")
		return nil, err
	}
	if err := releaseSavepoint(ctx, tx, "svc_farewell"); err != nil {
		return nil, err
	}
	return &Farewell{OutboundResult: sent, JobID: jobID}, nil
}

func (s Service) replayFarewell(ctx context.Context, tx pgx.Tx, p Principal, conv store.Conversation, in CloseInput) (*Farewell, error) {
	_, err := queries(tx).GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: p.TenantID, PrincipalID: p.MembershipID, Operation: OpSendText, ClientRequestID: in.FarewellRequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	connector, err := queries(tx).GetInboxConnector(ctx, dbgen.GetInboxConnectorParams{TenantID: p.TenantID, ID: conv.InboxID})
	if err != nil {
		return nil, err
	}
	payload, err := canonicalSend(conv.ID.String(), connector.String(), in.FarewellBody)
	if err != nil {
		return nil, err
	}
	sent, err := s.Repo.EnqueueText(ctx, tx, store.OutboundInput{
		TenantID: p.TenantID, ConversationID: conv.ID, ConnectorID: connector, PrincipalID: p.MembershipID,
		Operation: OpSendText, ClientRequestID: in.FarewellRequestID, Payload: payload, Body: in.FarewellBody, At: in.At,
	})
	if err != nil {
		return nil, err
	}
	return &Farewell{OutboundResult: sent}, nil
}

func mapModel(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, conversation.ErrStaleVersion):
		return store.ErrStaleVersion
	case errors.Is(err, conversation.ErrStaleEpoch):
		return ErrStaleEpoch
	case errors.Is(err, conversation.ErrActiveConversation):
		return ErrActiveConversation
	case errors.Is(err, conversation.ErrInvalidTransition):
		return store.ErrInvalid
	case errors.Is(err, conversation.ErrClosureIncomplete), errors.Is(err, conversation.ErrInvalidCloseResult):
		return store.ErrInvalid
	default:
		return err
	}
}

func closureFrom(row dbgen.GetCycleClosureRow) store.Closure {
	return store.Closure{
		CycleNumber: row.CycleNumber, Reason: strVal(row.CloseReason), Result: strVal(row.CloseResult),
		Summary: strVal(row.CloseSummary), AuthorID: uuidVal(row.CloseAuthorID), AuthorKind: strVal(row.CloseAuthorKind),
		At: timeOf(row.ClosedAt), ExternalReference: strVal(row.ExternalReference),
	}
}

func timelineFrom(rows []dbgen.ListTimelineRow) []TimelineEvent {
	out := make([]TimelineEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, TimelineEvent{
			ID: row.ID, Kind: row.Kind, ActorID: uuidVal(row.ActorID), At: timeOf(row.At),
			Version: row.Version, ControllerEpoch: row.ControllerEpoch, CycleNumber: row.CycleNumber,
			Controller: row.Controller, AssigneeID: uuidVal(row.AssigneeID),
		})
	}
	return out
}
