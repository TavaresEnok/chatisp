package publicapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
)

const publicTextLimit = 16000

type textBody struct {
	Body            string `json:"body"`
	ClientRequestID string `json:"client_request_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

type assignmentBody struct {
	AssigneeID      uuid.UUID `json:"assignee_id"`
	ClientRequestID string    `json:"client_request_id"`
	ExpectedVersion int64     `json:"expected_version"`
}

type stateBody struct {
	Status          string `json:"status"`
	Reason          string `json:"reason"`
	Result          string `json:"result"`
	Summary         string `json:"summary"`
	ClientRequestID string `json:"client_request_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

var errNoOperator = errors.New("nenhum administrador ativo")

func (h *handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r, "conversations.write")
	if !ok {
		return
	}
	if h.attendance.DB == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	var body textBody
	if err := decodeJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "mensagem inválida")
		return
	}
	text := strings.TrimSpace(body.Body)
	requestID := strings.TrimSpace(body.ClientRequestID)
	if text == "" || utf8.RuneCountInString(text) > publicTextLimit || !validCommandKey(requestID) || body.ExpectedVersion < 1 {
		writeProblem(w, http.StatusBadRequest, "mensagem inválida")
		return
	}
	conv, err := h.lookupConversation(r.Context(), p.TenantID, ref)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	key := attendanceRequestID(p.TokenID, requestID)
	actor, err := h.sendActor(r.Context(), p.TenantID, conv, key)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	sent, err := h.attendance.Send(r.Context(), actor, attendanceService.SendInput{
		InboxID: conv.InboxID, ConversationID: conv.ID, ExpectedVersion: body.ExpectedVersion,
		ClientRequestID: key, Body: text, At: h.now().UTC(),
	})
	if err != nil {
		writeAttendance(w, err)
		return
	}
	if sent.JobID < 1 {
		if job, ok := h.linkedJob(r.Context(), p.TenantID, sent.AttemptID); ok {
			sent.JobID = job
		}
	}
	if sent.MessageID == uuid.Nil || sent.AttemptID == uuid.Nil || sent.JobID < 1 {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	code := http.StatusCreated
	if sent.Replayed {
		code = http.StatusOK
	}
	writeJSON(w, code, templateResult{
		MessageID: sent.MessageID, AttemptID: sent.AttemptID, JobID: sent.JobID, Replayed: sent.Replayed,
	})
}

func (h *handler) assignConversation(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r, "conversations.write")
	if !ok {
		return
	}
	if h.attendance.DB == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	var body assignmentBody
	if err := decodeJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "atribuição inválida")
		return
	}
	requestID := strings.TrimSpace(body.ClientRequestID)
	if body.AssigneeID == uuid.Nil || !validCommandKey(requestID) || body.ExpectedVersion < 1 {
		writeProblem(w, http.StatusBadRequest, "atribuição inválida")
		return
	}
	conv, err := h.lookupConversation(r.Context(), p.TenantID, ref)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	key := attendanceRequestID(p.TokenID, requestID)
	actor, err := h.assignmentActor(r.Context(), p.TenantID, conv.ID, key)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	if _, err = h.attendance.TransferToMember(r.Context(), actor, attendanceService.AssignmentInput{
		InboxID: conv.InboxID, ConversationID: conv.ID, TargetID: body.AssigneeID,
		ExpectedVersion: body.ExpectedVersion, ClientRequestID: key, At: h.now().UTC(),
	}); err != nil {
		writeAttendance(w, err)
		return
	}
	h.writeConversation(w, r, p.TenantID, ref)
}

func (h *handler) setConversationState(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r, "conversations.write")
	if !ok {
		return
	}
	if h.attendance.DB == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	var body stateBody
	if err := decodeJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "estado inválido")
		return
	}
	body.Status = strings.TrimSpace(body.Status)
	body.Reason = strings.TrimSpace(body.Reason)
	body.Result = strings.TrimSpace(body.Result)
	body.Summary = strings.TrimSpace(body.Summary)
	requestID := strings.TrimSpace(body.ClientRequestID)
	if !validCommandKey(requestID) || body.ExpectedVersion < 1 || !validState(body) {
		writeProblem(w, http.StatusBadRequest, "estado inválido")
		return
	}
	conv, err := h.lookupConversation(r.Context(), p.TenantID, ref)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	key := attendanceRequestID(p.TokenID, requestID)
	operation := attendanceService.OpReopen
	if body.Status == "closed" {
		operation = attendanceService.OpClose
	}
	actor, err := h.recordedActor(r.Context(), p.TenantID, operation, key)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	at := h.now().UTC()
	switch body.Status {
	case "closed":
		_, err = h.attendance.Close(r.Context(), actor, attendanceService.CloseInput{
			InboxID: conv.InboxID, ConversationID: conv.ID, ExpectedVersion: body.ExpectedVersion,
			ClientRequestID: key, Reason: body.Reason, Result: body.Result, Summary: body.Summary, At: at,
		})
	default:
		_, err = h.attendance.Reopen(r.Context(), actor, attendanceService.ReopenInput{
			InboxID: conv.InboxID, ConversationID: conv.ID, ExpectedVersion: body.ExpectedVersion,
			ClientRequestID: key, At: at,
		})
	}
	if err != nil {
		writeAttendance(w, err)
		return
	}
	h.writeConversation(w, r, p.TenantID, ref)
}

func validCommandKey(value string) bool {
	return value != "" && len(value) <= 200 && !strings.ContainsAny(value, "\r\n")
}

func validState(body stateBody) bool {
	switch body.Status {
	case "closed":
		return body.Reason != "" && utf8.RuneCountInString(body.Reason) <= 500 &&
			validCloseResult(body.Result) && body.Summary != "" && utf8.RuneCountInString(body.Summary) <= 4000
	case "open":
		return body.Reason == "" && body.Result == "" && body.Summary == ""
	default:
		return false
	}
}

func validCloseResult(value string) bool {
	switch value {
	case "resolved", "no_response", "referred_external", "spam":
		return true
	default:
		return false
	}
}

func (h *handler) lookupConversation(ctx context.Context, tenant uuid.UUID, ref string) (conversationView, error) {
	var view conversationView
	err := h.store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadConversation(ctx, tx, tenant, ref)
		if err != nil {
			return err
		}
		view = row
		return nil
	})
	return view, err
}

func (h *handler) writeConversation(w http.ResponseWriter, r *http.Request, tenant uuid.UUID, ref string) {
	view, err := h.lookupConversation(r.Context(), tenant, ref)
	if err != nil {
		writeAttendance(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) sendActor(ctx context.Context, tenant uuid.UUID, conv conversationView, key string) (attendanceService.Principal, error) {
	var actor attendanceService.Principal
	err := h.store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		prior, ok, err := priorOperationPrincipal(ctx, tx, tenant, attendanceService.OpSendText, key)
		if err != nil {
			return err
		}
		if !ok {
			if conv.AssigneeID == nil || *conv.AssigneeID == uuid.Nil {
				return errNoAssignee
			}
			prior = *conv.AssigneeID
		}
		actor, err = membershipActor(ctx, tx, tenant, prior)
		return err
	})
	return actor, err
}

func (h *handler) assignmentActor(ctx context.Context, tenant, conversation uuid.UUID, key string) (attendanceService.Principal, error) {
	var actor attendanceService.Principal
	err := h.store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var prior uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT principal_id
			FROM assignment_requests
			WHERE tenant_id = $1 AND conversation_id = $2 AND client_request_id = $3
			ORDER BY principal_id
			LIMIT 1
		`, tenant, conversation, key).Scan(&prior)
		if errors.Is(err, pgx.ErrNoRows) {
			prior, err = oldestAdmin(ctx, tx, tenant)
		}
		if err != nil {
			return err
		}
		actor, err = membershipActor(ctx, tx, tenant, prior)
		return err
	})
	return actor, err
}

func (h *handler) recordedActor(ctx context.Context, tenant uuid.UUID, operation, key string) (attendanceService.Principal, error) {
	var actor attendanceService.Principal
	err := h.store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		prior, ok, err := priorOperationPrincipal(ctx, tx, tenant, operation, key)
		if err != nil {
			return err
		}
		if !ok {
			prior, err = oldestAdmin(ctx, tx, tenant)
			if err != nil {
				return err
			}
		}
		actor, err = membershipActor(ctx, tx, tenant, prior)
		return err
	})
	return actor, err
}

func priorOperationPrincipal(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, operation, key string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT principal_id
		FROM idempotency_records
		WHERE tenant_id = $1 AND operation = $2 AND client_request_id = $3
		ORDER BY created_at
		LIMIT 1
	`, tenant, operation, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func oldestAdmin(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM memberships
		WHERE tenant_id = $1 AND status = 'active' AND role = 'admin'
		ORDER BY created_at, id
		LIMIT 1
	`, tenant).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errNoOperator
	}
	return id, err
}

func membershipActor(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (attendanceService.Principal, error) {
	var actor attendanceService.Principal
	var status string
	err := tx.QueryRow(ctx, `
		SELECT user_id, role, status FROM memberships WHERE tenant_id = $1 AND id = $2
	`, tenant, id).Scan(&actor.UserID, &actor.Role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return attendanceService.Principal{}, attendanceService.ErrForbidden
	}
	if err != nil {
		return attendanceService.Principal{}, err
	}
	if status != "active" || (actor.Role != "admin" && actor.Role != "supervisor" && actor.Role != "agent") {
		return attendanceService.Principal{}, attendanceService.ErrForbidden
	}
	actor.MembershipID = id
	actor.TenantID = tenant
	return actor, nil
}

func (h *handler) linkedJob(ctx context.Context, tenant, attempt uuid.UUID) (int64, bool) {
	var job int64
	err := h.store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT job_id FROM outbound_job_links WHERE tenant_id = $1 AND attempt_id = $2
		`, tenant, attempt).Scan(&job)
	})
	return job, err == nil && job > 0
}

func writeAttendance(w http.ResponseWriter, err error) {
	var denied *attendanceService.PolicyDenied
	if errors.As(err, &denied) {
		writeProblem(w, http.StatusUnprocessableEntity, denied.Reason)
		return
	}
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "conversa não encontrada")
	case errors.Is(err, errNoAssignee):
		writeProblem(w, http.StatusUnprocessableEntity, errNoAssignee.Error())
	case errors.Is(err, errNoOperator), errors.Is(err, attendanceService.ErrForbidden), errors.Is(err, attendanceService.ErrInboxForbidden), errors.Is(err, attendanceService.ErrTenantInactive):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, store.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "comando inválido")
	case errors.Is(err, store.ErrCapacityFull):
		writeProblem(w, http.StatusConflict, "capacidade esgotada")
	case errors.Is(err, store.ErrStaleVersion), errors.Is(err, store.ErrClosed), errors.Is(err, store.ErrIdempotencyConflict), errors.Is(err, attendanceService.ErrActiveConversation):
		writeProblem(w, http.StatusConflict, err.Error())
	case errors.Is(err, attendanceService.ErrJobRequired), errors.Is(err, attendanceService.ErrChannelUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	default:
		writeAccess(w, err)
	}
}
