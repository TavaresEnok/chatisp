package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/conversation"
	"chatisp/internal/platform/db/dbgen"
)

// ReserveDispatch stores the coordinator fence and moves a queued attempt to
// sending. The epoch is read under the conversation lock after the lease lock.
// A stale queued attempt is cancelled in the committed transaction.
func (s Service) ReserveDispatch(ctx context.Context, in ReserveInput) (Reservation, error) {
	var out Reservation
	var stale bool
	var revoked bool
	var policyDenied bool
	err := s.tx(ctx, in.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Match the dispatch boundary and sector policy: acquire the tenant
		// gate before the lease and attendance rows, and hold it through commit.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, in.TenantID.String()); err != nil {
			return err
		}
		if err := s.tenantActive(ctx); err != nil {
			return err
		}
		if in.OwnerID == uuid.Nil || in.AttemptID == uuid.Nil || in.InboxID == uuid.Nil || in.ExpiresAt.IsZero() || in.At.IsZero() {
			return store.ErrInvalid
		}
		if err := s.connectorReady(ctx, tx, in.TenantID, in.InboxID); err != nil {
			return err
		}
		var lease store.Lease
		var err error
		if in.LeaseVersion > 0 {
			lease, err = s.Repo.RenewLease(ctx, tx, in.TenantID, in.InboxID, in.OwnerID, in.LeaseVersion, in.ExpiresAt)
		} else {
			lease, err = s.Repo.AcquireLease(ctx, tx, in.TenantID, in.InboxID, in.OwnerID, in.ExpiresAt)
		}
		if err != nil {
			return err
		}
		peek, err := queries(tx).PeekOutbound(ctx, dbgen.PeekOutboundParams{TenantID: in.TenantID, ID: in.AttemptID})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		conv, err := lockConversation(ctx, tx, in.TenantID, peek.ConversationID)
		if err != nil {
			return err
		}
		if conv.InboxID != in.InboxID {
			return store.ErrInvalid
		}
		out = Reservation{OwnerID: lease.OwnerID, LeaseVersion: lease.LeaseVersion, ExpiresAt: lease.ExpiresAt, State: peek.State, Epoch: conv.ControllerEpoch}
		// A close commits the epoch bump in the same transaction as the goodbye.
		// The goodbye stays dispatchable. Every other queued attempt of an old
		// epoch, including a human text that is not that goodbye, is stale.
		goodbye, err := farewellAuthorized(ctx, tx, in.TenantID, in.AttemptID, conv.ID, peek.ControllerEpoch)
		if err != nil {
			return err
		}
		if !goodbye && (peek.ControllerEpoch != conv.ControllerEpoch || conv.Status == store.StatusClosed) {
			stale = true
			if peek.State == "queued" {
				if _, err := s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
					TenantID: in.TenantID, AttemptID: in.AttemptID, To: storeStateCancelled,
				}); err != nil {
					return err
				}
				out.State = storeStateCancelled
			} else if peek.State == "retrying" {
				if _, err := s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
					TenantID: in.TenantID, AttemptID: in.AttemptID, To: "failed", ErrorCode: "epoch_obsoleta", ErrorMessage: "posse encerrada antes do despacho",
				}); err != nil {
					return err
				}
				out.State = "failed"
			}
			return nil
		}
		if peek.State == "uncertain" {
			return store.ErrUncertain
		}
		if peek.State != "queued" && peek.State != "retrying" {
			return store.ErrInvalid
		}
		allowed, err := s.authorAllowed(ctx, tx, in.TenantID, peek.MessageID)
		if err != nil {
			return err
		}
		if !allowed {
			revoked = true
			to := storeStateCancelled
			if peek.State == "retrying" {
				to = "failed"
			}
			state, err := s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
				TenantID: in.TenantID, AttemptID: in.AttemptID, To: to,
				ErrorCode: "permissao_revogada", ErrorMessage: "acesso revogado antes do despacho",
			})
			if err != nil {
				return err
			}
			out.State = state
			return nil
		}
		if peek.State == "queued" || peek.State == "retrying" {
			code, message, err := s.EvaluateAttemptPolicy(ctx, tx, in.TenantID, in.AttemptID, lease.LeaseVersion)
			if err != nil {
				return err
			}
			if code != "" {
				state, err := s.refuseQueued(ctx, tx, in.TenantID, in.AttemptID, peek.State, code, message)
				if err != nil {
					return err
				}
				out.State = state
				policyDenied = true
				return nil
			}
		}
		id, err := reserveSendSlot(ctx, tx, s.Repo, in.TenantID, in.InboxID, in.AttemptID, in.OwnerID, lease.LeaseVersion, in.ExpiresAt)
		if err != nil {
			return err
		}
		state, err := s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
			TenantID: in.TenantID, AttemptID: in.AttemptID, To: "sending", DispatchedAt: in.At, LeaseVersion: lease.LeaseVersion,
		})
		if err != nil {
			return err
		}
		out.ID = id
		out.State = state
		out.Epoch = peek.ControllerEpoch
		return nil
	})
	if err != nil {
		return Reservation{}, err
	}
	if revoked {
		return out, ErrForbidden
	}
	if policyDenied {
		// The existing dispatch fence stops before any driver when it sees
		// ErrForbidden. Joining the policy sentinel keeps the stored reason
		// distinct and still uses that fence without a remote call or a retry.
		return out, errors.Join(ErrPolicyDenied, ErrForbidden)
	}
	if stale {
		return out, ErrStaleEpoch
	}
	return out, nil
}

// RecordAttemptResult stores accepted, failed, uncertain or retrying.
// Documented human acceptance starts waiting only for the current epoch,
// and only when the customer has not written after that acceptance.
func (s Service) RecordAttemptResult(ctx context.Context, in AttemptResultInput) (AttemptResult, error) {
	var out AttemptResult
	err := s.tx(ctx, in.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.tenantActive(ctx); err != nil {
			return err
		}
		if in.To == "accepted" && (!in.Documented || in.AcceptedAt.IsZero() || strings.TrimSpace(in.ExternalMessageID) == "") {
			return store.ErrInvalid
		}
		peek, err := queries(tx).PeekOutbound(ctx, dbgen.PeekOutboundParams{TenantID: in.TenantID, ID: in.AttemptID})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := lockConversation(ctx, tx, in.TenantID, peek.ConversationID); err != nil {
			return err
		}
		leaseVersion := in.LeaseVersion
		if in.Observation {
			if in.LeaseVersion != 0 || in.OwnerID != uuid.Nil || (in.To != "accepted" && in.To != "failed") {
				return store.ErrInvalid
			}
			leaseVersion = 0
		} else if err := expectFence(ctx, tx, in.TenantID, peek.MessageID, in.AttemptID, in.OwnerID, in.LeaseVersion); err != nil {
			return err
		}
		state, err := s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
			TenantID: in.TenantID, AttemptID: in.AttemptID, To: in.To, DriverRef: in.DriverRef,
			ErrorCode: in.ErrorCode, ErrorMessage: in.ErrorMessage, ExternalMessageID: in.ExternalMessageID,
			LeaseVersion: leaseVersion,
		})
		if err != nil {
			return err
		}
		out.State = state
		waiting, ignored, err := s.settlePendingReceipts(ctx, tx, in.TenantID, peek.MessageID, in.ExternalMessageID)
		if err != nil {
			return err
		}
		out.Waiting = waiting
		out.IgnoredLate = ignored
		if in.To != "accepted" {
			return nil
		}
		waiting, ignored, err = s.noteHumanAcceptance(ctx, tx, in.TenantID, peek.MessageID, peek.ControllerEpoch, in.AcceptedAt, in.Documented)
		if err != nil {
			return err
		}
		out.Waiting = out.Waiting || waiting
		if out.Waiting {
			out.IgnoredLate = false
		} else if ignored {
			out.IgnoredLate = true
		}
		return nil
	})
	return out, err
}

// RecoverCrashedDispatch marks a sending attempt uncertain. The schema does
// not allow a return to queued, so a crash is never dispatched again by this
// method. A later receipt may still accept or fail the same attempt.
// ownerID and leaseVersion are the fence stored by ReserveDispatch. An older
// fence does not mark a newer sending attempt uncertain.
func (s Service) RecoverCrashedDispatch(ctx context.Context, tenantID, attemptID, ownerID uuid.UUID, leaseVersion int64) (string, error) {
	var state string
	err := s.tx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.tenantActive(ctx); err != nil {
			return err
		}
		peek, err := queries(tx).PeekOutbound(ctx, dbgen.PeekOutboundParams{TenantID: tenantID, ID: attemptID})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if peek.State != "sending" {
			return store.ErrInvalid
		}
		if err := expectFence(ctx, tx, tenantID, peek.MessageID, attemptID, ownerID, leaseVersion); err != nil {
			return err
		}
		state, err = s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
			TenantID: tenantID, AttemptID: attemptID, To: "uncertain", ErrorCode: "processo_interrompido",
			ErrorMessage: "resultado remoto desconhecido", LeaseVersion: leaseVersion,
		})
		return err
	})
	return state, err
}

// RecordReceipt stores one remote observation. A status that names a message
// already stored is linked before the dedup read, so a repeated key observes
// the row after LinkPendingStatus. Acceptance of an old epoch does not move clocks.
func (s Service) RecordReceipt(ctx context.Context, in store.StatusInput) (store.StatusResult, bool, error) {
	return s.observeReceipt(ctx, in)
}

// ReconcileReceipt is the worker entry for a status that may still be pending.
// It is the same transaction as RecordReceipt: link, then store, then the
// eligible acceptance effect of every receipt now attached to the message.
func (s Service) ReconcileReceipt(ctx context.Context, in store.StatusInput) (store.StatusResult, bool, error) {
	return s.observeReceipt(ctx, in)
}

func (s Service) observeReceipt(ctx context.Context, in store.StatusInput) (store.StatusResult, bool, error) {
	var result store.StatusResult
	var ignored bool
	err := s.tx(ctx, in.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.tenantActive(ctx); err != nil {
			return err
		}
		if err := lockReceiptConversation(ctx, tx, in); err != nil {
			return err
		}
		if err := linkReceiptMessage(ctx, tx, in); err != nil {
			return err
		}
		got, err := s.Repo.RecordStatus(ctx, tx, in)
		if err != nil {
			return err
		}
		result = got
		if got.Pending || got.MessageID == uuid.Nil {
			return nil
		}
		waiting, late, err := s.applyAcceptedReceipts(ctx, tx, in.TenantID, got.MessageID, in.ConnectorID, in.ExternalMessageID)
		if err != nil {
			return err
		}
		if in.Kind != "accepted" {
			return nil
		}
		if got.Duplicate {
			ignored = false
		} else {
			ignored = late && !waiting
		}
		return nil
	})
	return result, ignored, err
}

// ReserveWorker and ReleaseWorker are the tenant concurrency slots.
func (s Service) ReserveWorker(ctx context.Context, tenantID, ownerID uuid.UUID, queue string, expiresAt time.Time) (store.JobReservation, error) {
	var out store.JobReservation
	err := s.tx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.tenantActive(ctx); err != nil {
			return err
		}
		got, err := s.Repo.AcquireJob(ctx, tx, tenantID, ownerID, queue, expiresAt)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// ReleaseWorker frees a slot owned by the same worker.
func (s Service) ReleaseWorker(ctx context.Context, tenantID, reservationID, ownerID uuid.UUID, queue string) error {
	return s.tx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return s.Repo.ReleaseJob(ctx, tx, tenantID, reservationID, ownerID, queue)
	})
}

// ConsumeReservation and ReleaseReservation finish a send fence.
func (s Service) ConsumeReservation(ctx context.Context, tenantID, reservationID, ownerID uuid.UUID) error {
	return s.tx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return s.Repo.ConsumeSend(ctx, tx, tenantID, reservationID, ownerID)
	})
}

// ReleaseReservation returns a held send slot without sending.
func (s Service) ReleaseReservation(ctx context.Context, tenantID, reservationID, ownerID uuid.UUID) error {
	return s.tx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return s.Repo.ReleaseSend(ctx, tx, tenantID, reservationID, ownerID)
	})
}

// settlePendingReceipts attaches every status stored for this external id
// once the message has that id, then applies eligible acceptance effects.
// An empty external id leaves receipts untouched. Delivery, read and failure
// stop being pending and do not move clocks.
func (s Service) settlePendingReceipts(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, external string) (bool, bool, error) {
	external = strings.TrimSpace(external)
	if external == "" || messageID == uuid.Nil {
		return false, false, nil
	}
	var connectorID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT connector_id
		FROM messages
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
	`, tenantID, messageID).Scan(&connectorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, store.ErrNotFound
	}
	if err != nil {
		return false, false, err
	}
	if _, err := queries(tx).LinkPendingStatus(ctx, dbgen.LinkPendingStatusParams{
		MessageID: &messageID, TenantID: tenantID, ConnectorID: connectorID, ExternalMessageID: &external,
	}); err != nil {
		return false, false, err
	}
	return s.applyAcceptedReceipts(ctx, tx, tenantID, messageID, connectorID, external)
}

// linkReceiptMessage binds pending rows when the message already carries this
// external id. RecordStatus can then return the updated row for the same key.
func linkReceiptMessage(ctx context.Context, tx pgx.Tx, in store.StatusInput) error {
	external := strings.TrimSpace(in.ExternalMessageID)
	if external == "" || in.ConnectorID == uuid.Nil {
		return nil
	}
	found, err := queries(tx).GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
		TenantID: in.TenantID, ConnectorID: in.ConnectorID, ExternalMessageID: &external,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = queries(tx).LinkPendingStatus(ctx, dbgen.LinkPendingStatusParams{
		MessageID: &found.ID, TenantID: in.TenantID, ConnectorID: in.ConnectorID, ExternalMessageID: &external,
	})
	return err
}

// applyAcceptedReceipts runs the waiting effect for each linked acceptance.
// Successive eligible acceptances advance waiting_since and last_eligible_human_reply_at
// and publish realtime updates without duplicating the wait_started timeline event.
// Replay or out-of-order acceptance does not move clocks.
func (s Service) applyAcceptedReceipts(ctx context.Context, tx pgx.Tx, tenantID, messageID, connectorID uuid.UUID, external string) (bool, bool, error) {
	external = strings.TrimSpace(external)
	if external == "" || messageID == uuid.Nil || connectorID == uuid.Nil {
		return false, false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT trusted_at
		FROM message_status_events
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND connector_id = $2
		  AND external_message_id = $3
		  AND message_id = $4
		  AND pending = false
		  AND kind = 'accepted'
		ORDER BY trusted_at
	`, tenantID, connectorID, external, messageID)
	if err != nil {
		return false, false, err
	}
	var stamps []time.Time
	for rows.Next() {
		var trusted *time.Time
		if err := rows.Scan(&trusted); err != nil {
			rows.Close()
			return false, false, err
		}
		if trusted == nil || trusted.IsZero() {
			continue
		}
		stamps = append(stamps, trusted.UTC())
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, false, err
	}
	rows.Close()
	var waiting, ignored bool
	for _, trusted := range stamps {
		started, late, err := s.noteHumanAcceptance(ctx, tx, tenantID, messageID, 0, trusted, true)
		if err != nil {
			return false, false, err
		}
		if started {
			waiting = true
			ignored = false
			continue
		}
		if late && !waiting {
			ignored = true
		}
	}
	return waiting, ignored, nil
}

func lockReceiptConversation(ctx context.Context, tx pgx.Tx, in store.StatusInput) error {
	external := strings.TrimSpace(in.ExternalMessageID)
	if external == "" || in.ConnectorID == uuid.Nil {
		return nil
	}
	found, err := queries(tx).GetMessageByExternal(ctx, dbgen.GetMessageByExternalParams{
		TenantID: in.TenantID, ConnectorID: in.ConnectorID, ExternalMessageID: &external,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = lockConversation(ctx, tx, in.TenantID, found.ConversationID)
	return err
}

func (s Service) connectorReady(ctx context.Context, tx pgx.Tx, tenantID, inboxID uuid.UUID) error {
	var status, kind string
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT c.status, c.enabled, c.connector_type
		FROM inboxes AS i
		JOIN channel_connectors AS c
		  ON c.tenant_id = i.tenant_id AND c.id = i.connector_id
		WHERE i.tenant_id = $1 AND i.id = $2
	`, tenantID, inboxID).Scan(&status, &enabled, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !enabled || status != "connected" || kind != "meta_cloud" {
		return ErrChannelUnavailable
	}
	return nil
}

func (s Service) noteHumanAcceptance(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, attemptEpoch int64, acceptedAt time.Time, documented bool) (bool, bool, error) {
	var origin, visibility string
	var epoch *int64
	var conversationID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT origin, visibility, controller_epoch, conversation_id
		FROM messages
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
	`, tenantID, messageID).Scan(&origin, &visibility, &epoch, &conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, store.ErrNotFound
	}
	if err != nil {
		return false, false, err
	}
	conv, err := lockConversation(ctx, tx, tenantID, conversationID)
	if err != nil {
		return false, false, err
	}
	messageEpoch := attemptEpoch
	if epoch != nil {
		messageEpoch = *epoch
	}
	outbound := conversation.OutboundMessage{
		Origin: conversation.Origin(origin), Visibility: conversation.VisibilityPublic,
		LocalState: conversation.SendAccepted, ControllerEpoch: messageEpoch,
		AcceptanceDocumented: documented, Remote: conversation.RemoteFacts{AcceptedAt: acceptedAt},
	}
	if visibility != "public" {
		outbound.Visibility = conversation.VisibilityInternal
	}
	if messageEpoch != conv.ControllerEpoch || conv.Status == store.StatusClosed {
		return false, true, nil
	}
	if !conversation.StartsCustomerWait(outbound, conv.LastCustomerMessageAt) {
		return false, true, nil
	}
	if conv.Status == store.StatusWaitingCustomer && !conv.WaitingSince.IsZero() && !acceptedAt.After(conv.WaitingSince) {
		return false, true, nil
	}
	next, err := s.Repo.NoteAcceptedReply(ctx, tx, tenantID, conv.ID, conv.Version, acceptedAt)
	if errors.Is(err, store.ErrClosed) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if next.Version == conv.Version || next.Status != store.StatusWaitingCustomer {
		return false, true, nil
	}
	return true, false, nil
}

const storeStateCancelled = "cancelled"

// reserveSendSlot inserts the send fence. A retry of the same attempt already
// has a row, so the unique violation is rolled back to the savepoint and the
// existing row takes the lease just acquired for this inbox.
func reserveSendSlot(ctx context.Context, tx pgx.Tx, repo store.Repo, tenantID, inboxID, attemptID, ownerID uuid.UUID, version int64, expiresAt time.Time) (uuid.UUID, error) {
	if err := savepoint(ctx, tx, "svc_reserve"); err != nil {
		return uuid.Nil, err
	}
	id, err := repo.ReserveSend(ctx, tx, tenantID, inboxID, attemptID, ownerID, version, expiresAt)
	if uniqueViolation(err) {
		rollbackSavepoint(ctx, tx, "svc_reserve")
		id, err = retakeSendReservation(ctx, tx, tenantID, inboxID, attemptID, ownerID, version, expiresAt)
	}
	if err != nil {
		rollbackSavepoint(ctx, tx, "svc_reserve")
		return uuid.Nil, err
	}
	if err := releaseSavepoint(ctx, tx, "svc_reserve"); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func retakeSendReservation(ctx context.Context, tx pgx.Tx, tenantID, inboxID, attemptID, ownerID uuid.UUID, version int64, expiresAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE send_reservations
		SET owner_id = $3,
		    lease_version = $4,
		    expires_at = $5,
		    state = 'held'
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND attempt_id = $2
		  AND inbox_id = $6
		RETURNING id
	`, tenantID, attemptID, ownerID, version, expiresAt, inboxID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, store.ErrLeaseHeld
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// expectFence locks the message, the attempt and the reservation, in that order.
// The stored lease must be the caller's. Zero is not a fence. A mismatch does
// not write the attempt. Remote observations do not call this.
func expectFence(ctx context.Context, tx pgx.Tx, tenantID, messageID, attemptID, ownerID uuid.UUID, version int64) error {
	if version < 1 || ownerID == uuid.Nil {
		return store.ErrInvalid
	}
	if _, err := queries(tx).LockMessage(ctx, dbgen.LockMessageParams{TenantID: tenantID, ID: messageID}); errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	} else if err != nil {
		return err
	}
	var lease *int64
	err := tx.QueryRow(ctx, `
		SELECT lease_version
		FROM outbound_attempts
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
		FOR UPDATE
	`, tenantID, attemptID).Scan(&lease)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	var gotOwner uuid.UUID
	var gotLease int64
	err = tx.QueryRow(ctx, `
		SELECT owner_id, lease_version
		FROM send_reservations
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND attempt_id = $2
		FOR UPDATE
	`, tenantID, attemptID).Scan(&gotOwner, &gotLease)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrLeaseHeld
	}
	if err != nil {
		return err
	}
	if lease == nil || *lease != version || gotLease != version || gotOwner != ownerID {
		return store.ErrLeaseHeld
	}
	return nil
}
