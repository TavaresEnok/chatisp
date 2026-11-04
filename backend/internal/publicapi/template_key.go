package publicapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
)

var errPayloadConflict = errors.New("a chave já foi usada com outro conteúdo")
var errTemplateBind = errors.New("resultado público não confirmado")

type templateSlot struct {
	token   uuid.UUID
	request string
	hash    []byte
}

type slotKey struct{}

func withTemplateSlot(ctx context.Context, slot templateSlot) context.Context {
	return context.WithValue(ctx, slotKey{}, slot)
}

func templateSlotFrom(ctx context.Context) (templateSlot, bool) {
	slot, ok := ctx.Value(slotKey{}).(templateSlot)
	return slot, ok && slot.token != uuid.Nil && slot.request != "" && len(slot.hash) == 32
}

// attendanceRequestID is the idempotency key handed to attendance. It carries
// the public API namespace and the token, and it stays within 200 characters.
// The caller's client_request_id is unchanged on the public row.
func attendanceRequestID(token uuid.UUID, external string) string {
	sum := sha256.Sum256([]byte(external))
	return "pubapi/" + token.String() + "/" + hex.EncodeToString(sum[:])
}

// bindingQueuer stores the job id, and the public result when this request
// reserved one, on the same transaction that accepts the outbox row.
type bindingQueuer struct {
	inner attendanceService.Queuer
}

func (q bindingQueuer) EnqueueOutbound(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID) (int64, error) {
	if q.inner == nil {
		return 0, attendanceService.ErrJobRequired
	}
	jobID, err := q.inner.EnqueueOutbound(ctx, tx, tenantID, attemptID)
	if err != nil {
		return 0, err
	}
	if jobID < 1 {
		return 0, attendanceService.ErrJobRequired
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO outbound_job_links (tenant_id, attempt_id, job_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, attempt_id) DO NOTHING
	`, tenantID, attemptID, jobID); err != nil {
		return 0, err
	}
	slot, ok := templateSlotFrom(ctx)
	if !ok {
		return jobID, nil
	}
	var messageID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT message_id FROM outbound_attempts WHERE tenant_id = $1 AND id = $2
	`, tenantID, attemptID).Scan(&messageID)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE api_template_requests
		SET message_id = $4, attempt_id = $5, job_id = $6, completed_at = COALESCE(completed_at, now())
		WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3
		  AND payload_hash = $7
		  AND (
		    message_id IS NULL
		    OR (message_id = $4 AND attempt_id = $5 AND job_id = $6)
		  )
	`, tenantID, slot.token, slot.request, messageID, attemptID, jobID, slot.hash)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() != 1 {
		return 0, errTemplateBind
	}
	return jobID, nil
}

type templateClaim struct {
	done       bool
	result     templateResult
	membership uuid.UUID
	userID     uuid.UUID
	role       string
}

func templatePayloadHash(conversation uuid.UUID, templateID, language, purpose string) []byte {
	sum := sha256.Sum256([]byte(conversation.String() + "\n" + templateID + "\n" + language + "\n" + purpose))
	return sum[:]
}

func templateLockKey(tenant, token uuid.UUID, requestID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tenant.String()))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(token.String()))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(requestID))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

func (h *handler) claimTemplate(ctx context.Context, p principal, conversation, inbox uuid.UUID, requestID string, hash []byte, expectedVersion int64) (templateClaim, error) {
	var claim templateClaim
	err := h.store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, templateLockKey(p.TenantID, p.TokenID, requestID)); err != nil {
			return err
		}
		var stored []byte
		var storedConversation, membership uuid.UUID
		var messageID, attemptID *uuid.UUID
		var jobID *int64
		err := tx.QueryRow(ctx, `
			SELECT payload_hash, conversation_id, membership_id, message_id, attempt_id, job_id
			FROM api_template_requests
			WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3
		`, p.TenantID, p.TokenID, requestID).Scan(&stored, &storedConversation, &membership, &messageID, &attemptID, &jobID)
		if err == nil {
			if !bytesEqual(stored, hash) || storedConversation != conversation {
				return errPayloadConflict
			}
			if messageID != nil && attemptID != nil && jobID != nil {
				claim.done = true
				claim.result = templateResult{
					MessageID: *messageID, AttemptID: *attemptID, JobID: *jobID, Replayed: true,
				}
				return nil
			}
			found, ok, err := findCommittedTemplate(ctx, tx, p.TenantID, membership, attendanceRequestID(p.TokenID, requestID))
			if err != nil {
				return err
			}
			if ok {
				if err = storeTemplateResult(ctx, tx, p, requestID, hash, found, true); err != nil {
					return err
				}
				claim.done = true
				claim.result = templateResult{
					MessageID: found.message, AttemptID: found.attempt, JobID: found.job, Replayed: true,
				}
				return nil
			}
			claim.membership = membership
			if err = useCurrentAssignee(ctx, tx, p.TenantID, conversation, inbox, &claim); err != nil {
				return err
			}
			if claim.membership != membership {
				if _, err = tx.Exec(ctx, `
					UPDATE api_template_requests
					SET membership_id = $4
					WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3 AND message_id IS NULL
				`, p.TenantID, p.TokenID, requestID, claim.membership); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if expectedVersion < 1 {
			return store.ErrInvalid
		}
		var assignee *uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT assignee_id FROM conversations WHERE tenant_id = $1 AND id = $2 AND inbox_id = $3
		`, p.TenantID, conversation, inbox).Scan(&assignee)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if assignee == nil {
			return errNoAssignee
		}
		claim.membership = *assignee
		if err = loadTemplateActor(ctx, tx, p.TenantID, &claim); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO api_template_requests (
				tenant_id, token_id, client_request_id, conversation_id, payload_hash, membership_id
			) VALUES ($1,$2,$3,$4,$5,$6)
		`, p.TenantID, p.TokenID, requestID, conversation, hash, claim.membership)
		return err
	})
	return claim, err
}

func loadTemplateActor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, claim *templateClaim) error {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT user_id, role, status FROM memberships WHERE tenant_id = $1 AND id = $2
	`, tenant, claim.membership).Scan(&claim.userID, &claim.role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return attendanceService.ErrForbidden
	}
	if err != nil {
		return err
	}
	if status != "active" || (claim.role != "admin" && claim.role != "supervisor" && claim.role != "agent") {
		return attendanceService.ErrForbidden
	}
	return nil
}

func (h *handler) finishTemplate(ctx context.Context, p principal, requestID string, hash []byte, sent attendanceService.SendResult) (templateClaim, error) {
	var claim templateClaim
	err := h.store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, templateLockKey(p.TenantID, p.TokenID, requestID)); err != nil {
			return err
		}
		var messageID, attemptID *uuid.UUID
		var jobID *int64
		err := tx.QueryRow(ctx, `
			SELECT message_id, attempt_id, job_id
			FROM api_template_requests
			WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3 AND payload_hash = $4
			FOR UPDATE
		`, p.TenantID, p.TokenID, requestID, hash).Scan(&messageID, &attemptID, &jobID)
		if err != nil {
			return err
		}
		if messageID != nil && attemptID != nil && jobID != nil {
			replayed := true
			if sent.MessageID == *messageID && sent.AttemptID == *attemptID && sent.JobID == *jobID {
				replayed = sent.Replayed
			}
			claim.done = true
			claim.result = templateResult{MessageID: *messageID, AttemptID: *attemptID, JobID: *jobID, Replayed: replayed}
			return nil
		}
		found := committedTemplate{message: sent.MessageID, attempt: sent.AttemptID, job: sent.JobID}
		replayed := sent.Replayed
		if sent.JobID < 1 || sent.MessageID == uuid.Nil || sent.AttemptID == uuid.Nil {
			var membership uuid.UUID
			err = tx.QueryRow(ctx, `
				SELECT membership_id FROM api_template_requests
				WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3 AND payload_hash = $4
			`, p.TenantID, p.TokenID, requestID, hash).Scan(&membership)
			if err != nil {
				return err
			}
			loaded, ok, err := findCommittedTemplate(ctx, tx, p.TenantID, membership, attendanceRequestID(p.TokenID, requestID))
			if err != nil || !ok {
				return err
			}
			found = loaded
			replayed = true
		}
		if err = storeTemplateResult(ctx, tx, p, requestID, hash, found, replayed); err != nil {
			return err
		}
		claim.done = true
		claim.result = templateResult{MessageID: found.message, AttemptID: found.attempt, JobID: found.job, Replayed: replayed}
		return nil
	})
	return claim, err
}

type committedTemplate struct {
	message uuid.UUID
	attempt uuid.UUID
	job     int64
}

func findCommittedTemplate(ctx context.Context, tx pgx.Tx, tenant, principal uuid.UUID, internalKey string) (committedTemplate, bool, error) {
	var idem uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM idempotency_records
		WHERE tenant_id = $1 AND principal_id = $2 AND operation = $3 AND client_request_id = $4
	`, tenant, principal, attendanceService.OpSendTemplate, internalKey).Scan(&idem)
	if errors.Is(err, pgx.ErrNoRows) {
		return committedTemplate{}, false, nil
	}
	if err != nil {
		return committedTemplate{}, false, err
	}
	var found committedTemplate
	err = tx.QueryRow(ctx, `
		SELECT m.id, o.id
		FROM messages m
		JOIN outbound_attempts o ON o.tenant_id = m.tenant_id AND o.idempotency_id = m.idempotency_id
		WHERE m.tenant_id = $1 AND m.idempotency_id = $2
		ORDER BY o.attempt_no
		LIMIT 1
	`, tenant, idem).Scan(&found.message, &found.attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return committedTemplate{}, false, nil
	}
	if err != nil {
		return committedTemplate{}, false, err
	}
	err = tx.QueryRow(ctx, `
		SELECT job_id FROM outbound_job_links WHERE tenant_id = $1 AND attempt_id = $2
	`, tenant, found.attempt).Scan(&found.job)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && found.job < 1) {
		return committedTemplate{}, false, nil
	}
	if err != nil {
		return committedTemplate{}, false, err
	}
	return found, true, nil
}

func storeTemplateResult(ctx context.Context, tx pgx.Tx, p principal, requestID string, hash []byte, found committedTemplate, _ bool) error {
	tag, err := tx.Exec(ctx, `
		UPDATE api_template_requests
		SET message_id = $4, attempt_id = $5, job_id = $6, completed_at = COALESCE(completed_at, now())
		WHERE tenant_id = $1 AND token_id = $2 AND client_request_id = $3
		  AND payload_hash = $7
		  AND (
		    message_id IS NULL
		    OR (message_id = $4 AND attempt_id = $5 AND job_id = $6)
		  )
	`, p.TenantID, p.TokenID, requestID, found.message, found.attempt, found.job, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errTemplateBind
	}
	return nil
}

// useCurrentAssignee keeps a fresh send on the conversation's assignee. A
// committed send is recovered before this runs, so a disabled actor does not
// block that result and does not force a second send.
func useCurrentAssignee(ctx context.Context, tx pgx.Tx, tenant, conversation, inbox uuid.UUID, claim *templateClaim) error {
	if err := loadTemplateActor(ctx, tx, tenant, claim); err == nil {
		var assignee *uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT assignee_id FROM conversations WHERE tenant_id = $1 AND id = $2 AND inbox_id = $3
		`, tenant, conversation, inbox).Scan(&assignee)
		if err != nil {
			return err
		}
		if assignee != nil && *assignee == claim.membership {
			return nil
		}
	}
	var assignee *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT assignee_id FROM conversations WHERE tenant_id = $1 AND id = $2 AND inbox_id = $3
	`, tenant, conversation, inbox).Scan(&assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if assignee == nil {
		return errNoAssignee
	}
	claim.membership = *assignee
	return loadTemplateActor(ctx, tx, tenant, claim)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
