package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

type outboundHandler struct {
	dbStore            *db.Store
	attendance         service.Service
	material           MaterialResolver
	credentialReporter CredentialReporter
	driverFactory      DriverFactory
	repo               store.Repo
	triageInboxes      map[uuid.UUID]bool
}

// NewOutboundHandler constructs the River OutboundHandler for dispatching messages.
func NewOutboundHandler(
	database *db.Store,
	attendance service.Service,
	material MaterialResolver,
	reporter CredentialReporter,
	factory DriverFactory,
) jobs.OutboundHandler {
	return &outboundHandler{
		dbStore:            database,
		attendance:         attendance,
		material:           material,
		credentialReporter: reporter,
		driverFactory:      factory,
		repo:               store.Repo{},
	}
}

// DispatchOutbound implements jobs.OutboundHandler.
// It reserves the inbox lease, revalidates epoch/permission at the boundary,
// calls the driver outside any transaction, and reconciles the attempt state.
func (h *outboundHandler) DispatchOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) error {
	if h == nil || h.dbStore == nil || h.material == nil || h.credentialReporter == nil || h.driverFactory == nil {
		return ErrUnavailable
	}
	if tenantID == uuid.Nil || attemptID == uuid.Nil {
		return ErrInvalid
	}

	workerID := uuid.New()
	expiresAt := time.Now().Add(30 * time.Second)

	// Ensure PG tenant slot limit is compatible with execution capacity (2 workers total -> max 1 per tenant).
	_ = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_job_slots (tenant_id, queue_name, max_slots)
			VALUES ($1, 'outbound', 1)
			ON CONFLICT (tenant_id, queue_name) DO UPDATE
			SET max_slots = LEAST(tenant_job_slots.max_slots, 1)
		`, tenantID)
		return err
	})

	// 1. Reserve worker slot for outbound queue to enforce tenant concurrency limit.
	slot, err := h.attendance.ReserveWorker(ctx, tenantID, workerID, "outbound", expiresAt)
	if errors.Is(err, store.ErrCapacity) {
		return river.JobSnooze(2 * time.Second)
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = h.attendance.ReleaseWorker(context.Background(), tenantID, slot.ID, workerID, "outbound")
	}()

	// 2. Peek attempt details.
	var (
		state          string
		inboxID        uuid.UUID
		connectorID    uuid.UUID
		messageBody    string
		recipientPhone string
		versionText    string
		contentKind    string
		contentContext []byte
		replyProvider  string
	)

	err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT o.state, c.inbox_id, m.connector_id, m.body,
			       ci.external_id, cc.version, m.content_kind, m.content_context,
			       COALESCE(cited.external_message_id, '')
			FROM outbound_attempts o
			JOIN conversations c
			  ON c.tenant_id = o.tenant_id AND c.id = o.conversation_id
			JOIN messages m
			  ON m.tenant_id = o.tenant_id AND m.id = o.message_id
			JOIN contact_channel_identities ci
			  ON ci.tenant_id = c.tenant_id AND ci.id = c.contact_identity_id
			JOIN channel_connectors cc
			  ON cc.tenant_id = c.tenant_id AND cc.id = m.connector_id
			LEFT JOIN messages cited
			  ON cited.tenant_id = m.tenant_id AND cited.id = m.reply_to_message_id
			WHERE o.tenant_id = $1
			  AND o.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND o.id = $2
		`, tenantID, attemptID).Scan(
			&state, &inboxID, &connectorID, &messageBody,
			&recipientPhone, &versionText, &contentKind, &contentContext, &replyProvider,
		)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	if state == "accepted" || state == "uncertain" || state == "failed" || state == "cancelled" {
		return nil
	}

	if state == "sending" {
		// Crash recovery: process crashed or restarted after transitioning to sending.
		var (
			resID       uuid.UUID
			storedOwner uuid.UUID
			storedLease int64
		)
		err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT id, owner_id, lease_version
				FROM send_reservations
				WHERE tenant_id = $1
				  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
				  AND attempt_id = $2
			`, tenantID, attemptID).Scan(&resID, &storedOwner, &storedLease)
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if storedOwner != uuid.Nil && storedLease > 0 {
			_, err := h.attendance.RecoverCrashedDispatch(ctx, tenantID, attemptID, storedOwner, storedLease)
			if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrInvalid) && !errors.Is(err, store.ErrLeaseHeld) {
				return err
			}
			if resID != uuid.Nil {
				if err := h.attendance.ConsumeReservation(ctx, tenantID, resID, storedOwner); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
		}
		return nil
	}

	if state != "queued" && state != "retrying" {
		return nil
	}

	connVersion, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || connVersion < 1 {
		return ErrInvalid
	}

	// 3. Reserve dispatch and lock lease.
	reservation, err := h.attendance.ReserveDispatch(ctx, service.ReserveInput{
		TenantID:     tenantID,
		InboxID:      inboxID,
		AttemptID:    attemptID,
		OwnerID:      workerID,
		LeaseVersion: 0,
		ExpiresAt:    expiresAt,
		At:           time.Now(),
	})
	if errors.Is(err, service.ErrStaleEpoch) || errors.Is(err, service.ErrForbidden) {
		// Stale epoch or revoked permission: attempt was cancelled/failed by ReserveDispatch.
		return nil
	}
	if errors.Is(err, store.ErrUncertain) {
		return nil
	}
	if errors.Is(err, store.ErrLeaseHeld) {
		return err
	}
	if err != nil {
		return err
	}

	// 4. Preparation cannot outlive the reservation.
	prepCtx, cancelPrep := context.WithDeadline(ctx, expiresAt)
	defer cancelPrep()
	material, err := h.material.ResolveMaterial(prepCtx, tenantID, connectorID, connVersion)
	var driver whatsapp.TextDriver
	var preparationCode, preparationMessage string
	if prepCtx.Err() == nil {
		if errors.Is(err, configuration.ErrDegraded) || errors.Is(err, configuration.ErrStaleVersion) {
			preparationCode, preparationMessage = "canal_degradado", "canal degradado ou versão desatualizada"
		} else if err != nil {
			preparationCode, preparationMessage = "material_indisponivel", err.Error()
		} else {
			driver, err = h.driverFactory.DriverFor(prepCtx, tenantID, connectorID, material)
			if prepCtx.Err() == nil && err != nil {
				preparationCode, preparationMessage = "driver_indisponivel", err.Error()
			}
		}
	}

	// 5. Remote call OUTSIDE any database transaction.
	// At the dispatch boundary: atomically revalidate lease ownership and version,
	// renew expiration for the network send window, and strictly bind the call context
	// to the effective reservation deadline (fencing).
	var fencedExpiresAt time.Time
	var outboundKind, outboundTemplate, outboundLanguage string
	invalidated := false
	err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Tenant gate precedes lease and attendance locks, and remains held until
		// the boundary commits. SetTenantStatus uses the same transaction lock.
		if err := h.dbStore.LockTenant(ctx); err != nil {
			return err
		}

		// Consistent lock order: tenant gate advisory lock -> inbox_send_leases ->
		// channel_connectors -> contacts -> conversations -> messages and
		// outbound_attempts -> memberships -> send_reservations.
		// Acceptance and revocation take the connector, then the contact, then
		// the conversation. Those three are separate statements here. A join
		// that lists them together can lock the conversation before the
		// connector and deadlock with a new acceptance that already holds the
		// connector. The lease stays first, matching ReserveDispatch.
		var currentLeaseOwner uuid.UUID
		var currentLeaseVersion int64
		err = tx.QueryRow(ctx, `SELECT owner_id, lease_version FROM inbox_send_leases WHERE tenant_id=$1 AND inbox_id=$2 FOR UPDATE`,
			tenantID, inboxID).Scan(&currentLeaseOwner, &currentLeaseVersion)
		if err != nil {
			return err
		}

		var conversationID, contactID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT c.id, c.contact_id
			FROM outbound_attempts o
			JOIN conversations c ON c.tenant_id=o.tenant_id AND c.id=o.conversation_id
			WHERE o.tenant_id=$1 AND o.id=$2`, tenantID, attemptID).Scan(&conversationID, &contactID)
		if err != nil {
			return err
		}
		// Exclusive on the connector and the conversation: this boundary freezes
		// the rows it revalidates. The contact stays shared, as the blocked-contact
		// read. Acceptance takes the contact exclusively and the other two shared.
		if err = channelpolicy.LockConnector(ctx, tx, tenantID, connectorID, true); err != nil {
			return err
		}
		if err = channelpolicy.LockContact(ctx, tx, tenantID, contactID, false); err != nil {
			return err
		}
		if err = channelpolicy.LockConversation(ctx, tx, tenantID, conversationID, true); err != nil {
			return err
		}

		// Re-read mutable authorization and configuration under the locks above.
		// Messages and attempts are first locked here, after the conversation.
		// The farewell exception is bound to the exact conversation and epoch,
		// as in ReserveDispatch.
		var lockedConversation, lockedContact uuid.UUID
		var attemptEpoch, currentEpoch, attemptLeaseVersion int64
		var convStatus, origin, attemptState, tenantStatus, channelStatus, channelKind, channelVersion string
		var enabled bool
		var author *uuid.UUID
		err = tx.QueryRow(ctx, `SELECT c.id, o.controller_epoch, c.controller_epoch, c.status, c.contact_id,
            m.origin, m.author_membership_id, o.state, COALESCE(o.lease_version, 0), t.status, cc.status, cc.enabled, cc.connector_type, cc.version
            FROM outbound_attempts o
            JOIN conversations c ON c.tenant_id=o.tenant_id AND c.id=o.conversation_id
            JOIN messages m ON m.tenant_id=o.tenant_id AND m.id=o.message_id
            JOIN channel_connectors cc ON cc.tenant_id=m.tenant_id AND cc.id=m.connector_id
            JOIN inboxes i ON i.tenant_id=c.tenant_id AND i.id=c.inbox_id AND i.connector_id=cc.id
            JOIN tenants t ON t.id=o.tenant_id
            WHERE o.tenant_id=$1 AND o.id=$2 AND cc.id=$3 AND c.id=$4
            FOR UPDATE OF m, o`, tenantID, attemptID, connectorID, conversationID).Scan(
			&lockedConversation, &attemptEpoch, &currentEpoch, &convStatus, &lockedContact, &origin, &author,
			&attemptState, &attemptLeaseVersion, &tenantStatus, &channelStatus, &enabled, &channelKind, &channelVersion)
		if err != nil {
			return err
		}
		if lockedConversation != conversationID || lockedContact != contactID {
			return store.ErrInvalid
		}
		allowed := attemptState == "sending" && tenantStatus == "active" && enabled && channelStatus == "connected" && channelKind == "meta_cloud" && channelVersion == versionText
		automationOK, guardErr := automationAllowed(ctx, tx, tenantID, attemptID, h.triageInboxes)
		if guardErr != nil {
			return guardErr
		}
		allowed = allowed && automationOK
		blockedAll, blockErr := contactdirectory.IsBlocked(ctx, tx, tenantID, contactID)
		if blockErr != nil {
			return blockErr
		}
		allowed = allowed && !blockedAll
		if origin == "human" {
			blocked, err := contactdirectory.IsBlocked(ctx, tx, tenantID, contactID)
			if err != nil {
				return err
			}
			allowed = allowed && !blocked
			if author == nil {
				allowed = false
			} else {
				authorOK, authorErr := service.AuthorAllowed(ctx, tx, tenantID, *author, inboxID, conversationID)
				if authorErr != nil {
					return authorErr
				}
				allowed = allowed && authorOK
			}
		}
		if attemptEpoch != currentEpoch || convStatus == "closed" {
			var hash []byte
			err = tx.QueryRow(ctx, `SELECT request_hash FROM idempotency_records WHERE tenant_id=$1 AND operation='close_farewell' AND client_request_id=$2`, tenantID, attemptID.String()).Scan(&hash)
			if errors.Is(err, pgx.ErrNoRows) {
				allowed = false
			} else if err != nil {
				return err
			} else if origin != "human" || len(hash) != 32 || !bytes.Equal(hash[:16], conversationID[:]) || int64(binary.BigEndian.Uint64(hash[16:24])) != attemptEpoch {
				allowed = false
			}
		}

		// Verify own attempt's reservation fence. This permits a suspended tenant
		// or revoked sender's own undispatched attempt to be finalized locally even
		// if the shared inbox lease has expired or been acquired by another attempt.
		var ownedRes int
		err = tx.QueryRow(ctx, `SELECT 1 FROM send_reservations WHERE tenant_id=$1 AND id=$2 AND owner_id=$3 AND lease_version=$4 AND state='held' FOR UPDATE`,
			tenantID, reservation.ID, workerID, reservation.LeaseVersion).Scan(&ownedRes)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrLeaseHeld
		} else if err != nil {
			return err
		}
		// The attempt row is already locked. Re-read window, consent and template
		// approval after preparation and before the lease is renewed for the
		// network. A denial is committed with this lease; the driver runs only
		// after this transaction ends.
		if attemptState == "sending" && attemptLeaseVersion == reservation.LeaseVersion {
			decision, err := h.attendance.RevalidateDispatchFence(ctx, tx, tenantID, attemptID, reservation.LeaseVersion)
			if err != nil {
				return err
			}
			outboundKind = decision.Kind
			outboundTemplate = decision.TemplateID
			outboundLanguage = decision.Language
			if decision.Code != "" {
				preparationCode = decision.Code
				preparationMessage = decision.Message
			}
		}
		if !allowed || preparationCode != "" {
			if attemptState != "sending" || attemptLeaseVersion != reservation.LeaseVersion {
				return store.ErrLeaseHeld
			}
			code, message := preparationCode, preparationMessage
			if !allowed {
				code, message = "despacho_invalidado", "autorização ou configuração mudou antes do despacho"
			}
			_, err = h.repo.TransitionAttempt(ctx, tx, store.AttemptTransition{TenantID: tenantID, AttemptID: attemptID, To: "failed", ErrorCode: code, ErrorMessage: message, LeaseVersion: reservation.LeaseVersion})
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE send_reservations SET state='consumed' WHERE tenant_id=$1 AND id=$2 AND owner_id=$3 AND lease_version=$4 AND state='held'`, tenantID, reservation.ID, workerID, reservation.LeaseVersion)
			if err != nil {
				return err
			}
			invalidated = true
		}
		if invalidated {
			return nil
		}
		if prepCtx.Err() != nil {
			return store.ErrLeaseHeld
		}
		targetExpiresAt := time.Now().Add(30 * time.Second)
		var curVersion int64
		err := tx.QueryRow(ctx, `
			UPDATE inbox_send_leases
			SET expires_at = $5
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND inbox_id = $2
			  AND owner_id = $3
			  AND lease_version = $4
			  AND expires_at > clock_timestamp()
			RETURNING lease_version, expires_at
		`, tenantID, inboxID, workerID, reservation.LeaseVersion, targetExpiresAt).Scan(&curVersion, &fencedExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrLeaseHeld
		}
		if err != nil {
			return err
		}

		tag, err := tx.Exec(ctx, `
			UPDATE send_reservations
			SET expires_at = $3
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND id = $2
			  AND owner_id = $4
			  AND state = 'held' AND lease_version = $5 AND expires_at > clock_timestamp()
		`, tenantID, reservation.ID, fencedExpiresAt, workerID, reservation.LeaseVersion)
		if err != nil {
			return err
		}

		if tag.RowsAffected() != 1 {
			return store.ErrLeaseHeld
		}
		tag, err = tx.Exec(ctx, `
			UPDATE tenant_job_reservations
			SET expires_at = $3
			WHERE tenant_id = $1
			  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
			  AND id = $2
			  AND owner_id = $4 AND expires_at > clock_timestamp()
		`, tenantID, slot.ID, fencedExpiresAt, workerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return store.ErrLeaseHeld
		}

		return nil
	})
	if errors.Is(err, store.ErrLeaseHeld) {
		// Fencing revalidation failed: lease expired or acquired by another worker before network boundary.
		// Old owner must not dispatch remote network call.
		return err
	}
	if err != nil {
		return err
	}

	if invalidated {
		return nil
	}
	remaining := time.Until(fencedExpiresAt)
	if remaining <= 0 {
		return store.ErrLeaseHeld
	}
	callTimeout := remaining
	if callTimeout > 30*time.Second {
		callTimeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	var result whatsapp.Result
	if outboundKind == "template" {
		templateID := outboundTemplate
		if templateID == "" {
			templateID = messageBody
		}
		sender, ok := driver.(whatsapp.TemplateSender)
		if !ok || templateID == "" {
			result = whatsapp.Result{
				AttemptID:     whatsapp.AttemptID(attemptID.String()),
				Local:         whatsapp.StateFailed,
				RejectionCode: "template_not_authorized",
				Detail:        "template sem contrato de envio",
			}
		} else {
			result, _ = sender.SendTemplate(callCtx, whatsapp.TemplateRequest{
				AttemptID:     whatsapp.AttemptID(attemptID.String()),
				ConnectionID:  whatsapp.ConnectionID(connectorID.String()),
				ConnectorType: whatsapp.ConnectorMetaCloud,
				RecipientID:   whatsapp.RecipientID(recipientPhone),
				CallbackData:  attemptID.String(),
				TemplateID:    templateID,
				Language:      outboundLanguage,
			})
		}
	} else if mediaReq, media := outboundMediaRequest(attemptID, connectorID, recipientPhone, messageBody, contentKind, contentContext, replyProvider); media {
		sender, ok := driver.(whatsapp.MediaSender)
		if !ok || mediaReq.AttachmentID == "" {
			result = whatsapp.Result{
				AttemptID:     whatsapp.AttemptID(attemptID.String()),
				Local:         whatsapp.StateFailed,
				RejectionCode: "midia_sem_contrato",
				Detail:        "mídia sem contrato de envio",
			}
		} else if body, readErr := h.readOutboundMedia(callCtx, tenantID, mediaReq.AttachmentID, mediaReq.Kind); readErr != nil {
			result = whatsapp.Result{
				AttemptID:     whatsapp.AttemptID(attemptID.String()),
				Local:         whatsapp.StateFailed,
				RejectionCode: "midia_indisponivel",
				Detail:        "mídia sem bytes para envio",
			}
		} else {
			mediaReq.Body = body
			var sendErr error
			result, sendErr = sender.SendMedia(callCtx, mediaReq)
			if sendErr != nil {
				code, detail := "envio_recusado", "recusa local antes do despacho"
				if strings.Contains(sendErr.Error(), "legenda") {
					code, detail = "legenda_invalida", "legenda inválida"
				}
				result = whatsapp.Result{
					AttemptID:     whatsapp.AttemptID(attemptID.String()),
					Local:         whatsapp.StateFailed,
					RejectionCode: code,
					Detail:        detail,
				}
			}
		}
	} else {
		result, _ = driver.SendText(callCtx, whatsapp.TextRequest{
			AttemptID:     whatsapp.AttemptID(attemptID.String()),
			ConnectionID:  whatsapp.ConnectionID(connectorID.String()),
			ConnectorType: whatsapp.ConnectorMetaCloud,
			RecipientID:   whatsapp.RecipientID(recipientPhone),
			CallbackData:  attemptID.String(),
			Text:          messageBody,
		})
	}

	// 6. Handle dispatch results and receipts.
	if result.RejectionCode == metacloud.RejectionTokenInvalid || result.RejectionCode == "token_invalid" {
		if repErr := h.credentialReporter.ReportInvalidCredential(ctx, tenantID, connectorID, material.Version); repErr != nil {
			return repErr
		}
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: reservation.LeaseVersion,
			To:           "failed",
			ErrorCode:    "token_invalid",
			ErrorMessage: "token do canal inválido",
		}); recErr != nil {
			return recErr
		}
		if consErr := h.attendance.ConsumeReservation(ctx, tenantID, reservation.ID, workerID); consErr != nil {
			return consErr
		}
		return nil
	}

	switch result.Local {
	case whatsapp.StateAccepted:
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:          tenantID,
			AttemptID:         attemptID,
			OwnerID:           workerID,
			LeaseVersion:      reservation.LeaseVersion,
			To:                "accepted",
			ExternalMessageID: result.ProviderMessageID,
			Documented:        true,
			AcceptedAt:        time.Now(),
		}); recErr != nil {
			// Do not consume reservation on persistence error so recovery can run safely without sending twice.
			return recErr
		}
		if consErr := h.attendance.ConsumeReservation(ctx, tenantID, reservation.ID, workerID); consErr != nil {
			return consErr
		}
		return nil

	case whatsapp.StateFailed:
		errCode := result.RejectionCode
		if errCode == "" {
			errCode = "falha_envio"
		}
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: reservation.LeaseVersion,
			To:           "failed",
			ErrorCode:    errCode,
			ErrorMessage: result.Detail,
		}); recErr != nil {
			return recErr
		}
		if consErr := h.attendance.ConsumeReservation(ctx, tenantID, reservation.ID, workerID); consErr != nil {
			return consErr
		}
		return nil

	case whatsapp.StateRetrying:
		// Only not-dispatched proof allows retry.
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: reservation.LeaseVersion,
			To:           "retrying",
			ErrorCode:    "tentar_novamente",
			ErrorMessage: result.Detail,
		}); recErr != nil {
			return recErr
		}
		if relErr := h.attendance.ReleaseReservation(ctx, tenantID, reservation.ID, workerID); relErr != nil {
			return relErr
		}
		return errors.New("retrying outbound: " + result.Detail)

	case whatsapp.StateCancelled:
		// Attempt is already in 'sending' state. The database trigger outbound_attempts_guard_state
		// only allows transition from 'sending' to ('accepted', 'failed', 'retrying', 'uncertain').
		// Transitioning to 'cancelled' is forbidden by contract when in 'sending'.
		// Since StateCancelled proves cancellation occurred before remote dispatch, we record
		// the attempt as 'failed' with error_code 'cancelado' and consume the reservation.
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: reservation.LeaseVersion,
			To:           "failed",
			ErrorCode:    "cancelado",
			ErrorMessage: result.Detail,
		}); recErr != nil {
			return recErr
		}
		if consErr := h.attendance.ConsumeReservation(ctx, tenantID, reservation.ID, workerID); consErr != nil {
			return consErr
		}
		return nil

	case whatsapp.StateUncertain:
		fallthrough
	default:
		if _, recErr := h.attendance.RecordAttemptResult(ctx, service.AttemptResultInput{
			TenantID:     tenantID,
			AttemptID:    attemptID,
			OwnerID:      workerID,
			LeaseVersion: reservation.LeaseVersion,
			To:           "uncertain",
			ErrorCode:    "resultado_incerto",
			ErrorMessage: result.Detail,
		}); recErr != nil {
			return recErr
		}
		if consErr := h.attendance.ConsumeReservation(ctx, tenantID, reservation.ID, workerID); consErr != nil {
			return consErr
		}
		return nil
	}
}

func outboundMediaRequest(attemptID, connectorID uuid.UUID, recipient, caption, contentKind string, raw []byte, replyProvider string) (whatsapp.MediaRequest, bool) {
	switch contentKind {
	case "image", "document", "audio", "video":
	default:
		return whatsapp.MediaRequest{}, false
	}
	var doc struct {
		AttachmentID     string `json:"attachment_id"`
		SendAttachmentID string `json:"send_attachment_id"`
		MIME             string `json:"mime"`
		FileName         string `json:"file_name"`
	}
	_ = json.Unmarshal(raw, &doc)
	attachmentID := doc.SendAttachmentID
	if attachmentID == "" {
		attachmentID = doc.AttachmentID
	}
	mime := doc.MIME
	if contentKind == "audio" && (mime == "application/ogg" || mime == "video/ogg") {
		mime = "audio/ogg"
	}
	return whatsapp.MediaRequest{
		AttemptID:         whatsapp.AttemptID(attemptID.String()),
		ConnectionID:      whatsapp.ConnectionID(connectorID.String()),
		ConnectorType:     whatsapp.ConnectorMetaCloud,
		RecipientID:       whatsapp.RecipientID(recipient),
		CallbackData:      attemptID.String(),
		Kind:              whatsapp.MediaKind(contentKind),
		AttachmentID:      attachmentID,
		Caption:           caption,
		ReplyToProviderID: replyProvider,
		MIME:              mime,
		FileName:          doc.FileName,
	}, true
}

// readOutboundMedia loads the validated object when storage is configured.
// A nil store leaves the body empty: the simulation driver accepts that, and
// the official driver fails closed without calling Graph.
func (h *outboundHandler) readOutboundMedia(ctx context.Context, tenantID uuid.UUID, attachmentID string, kind whatsapp.MediaKind) ([]byte, error) {
	if h.attendance.Files == nil {
		return nil, nil
	}
	id, err := uuid.Parse(attachmentID)
	if err != nil {
		return nil, err
	}
	var key, version string
	err = h.dbStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT object_key, object_version
			  FROM attachments
			 WHERE tenant_id = $1 AND id = $2 AND state = 'validated' AND direction = 'outgoing'`,
			tenantID, id).Scan(&key, &version)
	})
	if err != nil {
		return nil, err
	}
	reader, err := h.attendance.Files.Read(ctx, key, version)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	limit := 16 << 20
	if kind == whatsapp.MediaDocument {
		limit = 100 << 20
	}
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil || len(body) == 0 || len(body) > limit {
		return nil, errors.New("mídia sem bytes para envio")
	}
	return body, nil
}
