package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
)

type contactBody struct {
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	ExternalRef string `json:"external_ref"`
}

type contactPatch struct {
	Name        *string `json:"name"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
	ExternalRef *string `json:"external_ref"`
}

var (
	phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	emailPattern = regexp.MustCompile(`^[^@[:space:]]+@[^@[:space:]]+$`)
	errErased    = errors.New("titular excluído")
)

type templateBody struct {
	TemplateID      string `json:"template_id"`
	Language        string `json:"language"`
	Purpose         string `json:"purpose"`
	ClientRequestID string `json:"client_request_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

type templateResult struct {
	MessageID uuid.UUID `json:"message_id"`
	AttemptID uuid.UUID `json:"attempt_id"`
	JobID     int64     `json:"job_id"`
	Replayed  bool      `json:"replayed"`
}

type agentView struct {
	ID     uuid.UUID `json:"id"`
	Role   string    `json:"role"`
	Status string    `json:"status"`
}

type sectorView struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

type inboxView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (h *handler) listContacts(w http.ResponseWriter, r *http.Request) {
	p, ok := h.admit(w, r, "contacts.read")
	if !ok {
		return
	}
	limit, err := messageLimit(r)
	if err != nil {
		writeAccess(w, err)
		return
	}
	var items []contactView
	err = h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, phone, email, blocked, external_ref
			FROM contacts
			WHERE tenant_id = $1
			ORDER BY created_at, id
			LIMIT $2
		`, p.TenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []contactView{}
		for rows.Next() {
			var item contactView
			if err := rows.Scan(&item.ID, &item.Name, &item.Phone, &item.Email, &item.Blocked, &item.ExternalRef); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": items})
}

func (h *handler) createContact(w http.ResponseWriter, r *http.Request) {
	p, ok := h.admit(w, r, "contacts.write")
	if !ok {
		return
	}
	var body contactBody
	if err := decodeJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "contato inválido")
		return
	}
	name := strings.TrimSpace(body.Name)
	phone := strings.TrimSpace(body.Phone)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	ref := strings.TrimSpace(body.ExternalRef)
	if name == "" || len(name) > 200 || len(ref) > 128 {
		writeProblem(w, http.StatusBadRequest, "contato inválido")
		return
	}
	var phoneArg, emailArg, refArg any
	if phone != "" {
		phoneArg = phone
	}
	if email != "" {
		emailArg = email
	}
	if ref != "" {
		refArg = ref
	}
	var view contactView
	var inserted bool
	err := h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO contacts (id, tenant_id, name, phone, email, external_ref)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, external_ref) WHERE external_ref IS NOT NULL
			DO UPDATE SET external_ref = contacts.external_ref
			RETURNING id, name, phone, email, blocked, external_ref, (xmax = 0)
		`, uuid.New(), p.TenantID, name, phoneArg, emailArg, refArg).Scan(
			&view.ID, &view.Name, &view.Phone, &view.Email, &view.Blocked, &view.ExternalRef, &inserted,
		)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23514" || pgErr.Code == "23505" || pgErr.Code == "22023") {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		}
		writeAccess(w, err)
		return
	}
	status := http.StatusCreated
	if !inserted {
		status = http.StatusOK
	}
	writeJSON(w, status, view)
}

func (h *handler) updateContact(w http.ResponseWriter, r *http.Request) {
	p, ok := h.admit(w, r, "contacts.write")
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "contato inválido")
		return
	}
	var body contactPatch
	if err := decodeJSON(r, &body); err != nil || (body.Name == nil && body.Phone == nil && body.Email == nil && body.ExternalRef == nil) {
		writeProblem(w, http.StatusBadRequest, "contato inválido")
		return
	}
	sets := make([]string, 0, 4)
	args := []any{p.TenantID, id}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" || len(name) > 200 {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		}
		add("name", name)
	}
	if body.Phone != nil {
		phone := strings.TrimSpace(*body.Phone)
		if phone == "" {
			add("phone", nil)
		} else if !phonePattern.MatchString(phone) {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		} else {
			add("phone", phone)
		}
	}
	if body.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*body.Email))
		if email == "" {
			add("email", nil)
		} else if len(email) < 3 || len(email) > 320 || !emailPattern.MatchString(email) {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		} else {
			add("email", email)
		}
	}
	if body.ExternalRef != nil {
		ref := strings.TrimSpace(*body.ExternalRef)
		if ref == "" {
			add("external_ref", nil)
		} else if len(ref) > 128 || strings.ContainsAny(ref, " \t\r\n") {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		} else {
			add("external_ref", ref)
		}
	}
	sets = append(sets, "updated_at = now()")
	var view contactView
	err = h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var erased *time.Time
		err := tx.QueryRow(ctx, `
			SELECT privacy_erased_at FROM contacts WHERE tenant_id = $1 AND id = $2 FOR UPDATE
		`, p.TenantID, id).Scan(&erased)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if erased != nil {
			return errErased
		}
		return tx.QueryRow(ctx, `
			UPDATE contacts SET `+strings.Join(sets, ", ")+`
			WHERE tenant_id = $1 AND id = $2
			RETURNING id, name, phone, email, blocked, external_ref
		`, args...).Scan(&view.ID, &view.Name, &view.Phone, &view.Email, &view.Blocked, &view.ExternalRef)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeProblem(w, http.StatusConflict, "referência externa já usada")
			return
		}
		if errors.Is(err, errErased) {
			writeProblem(w, http.StatusConflict, "titular excluído")
			return
		}
		if errors.Is(err, errNotFound) {
			writeProblem(w, http.StatusNotFound, "contato não encontrado")
			return
		}
		if errors.As(err, &pgErr) && (pgErr.Code == "23514" || pgErr.Code == "22023") {
			writeProblem(w, http.StatusBadRequest, "contato inválido")
			return
		}
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) listConversations(w http.ResponseWriter, r *http.Request) {
	p, ok := h.admit(w, r, "conversations.read")
	if !ok {
		return
	}
	limit, err := messageLimit(r)
	if err != nil {
		writeAccess(w, err)
		return
	}
	var items []conversationView
	err = h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, protocol, status, inbox_id, contact_id, sector_id, assignee_id,
			       controller, cycle_number, version, external_protocol, created_at, updated_at
			FROM conversations
			WHERE tenant_id = $1 AND inbox_id IS NOT NULL AND protocol IS NOT NULL
			ORDER BY updated_at DESC, id
			LIMIT $2
		`, p.TenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []conversationView{}
		for rows.Next() {
			var item conversationView
			var contact *uuid.UUID
			if err := rows.Scan(&item.ID, &item.Protocol, &item.Status, &item.InboxID, &contact, &item.SectorID, &item.AssigneeID, &item.Controller, &item.CycleNumber, &item.Version, &item.ExternalProtocol, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			if contact != nil {
				item.ContactID = *contact
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (h *handler) sendTemplate(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r, "templates.send")
	if !ok {
		return
	}
	if h.attendance.DB == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	var body templateBody
	if err := decodeJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "template inválido")
		return
	}
	purpose := strings.TrimSpace(body.Purpose)
	if purpose == "" {
		purpose = "atendimento"
	}
	var inbox, convID uuid.UUID
	err := h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadConversation(ctx, tx, p.TenantID, ref)
		if err != nil {
			return err
		}
		convID = row.ID
		inbox = row.InboxID
		return nil
	})
	if err != nil {
		writeCommand(w, err)
		return
	}
	requestID := strings.TrimSpace(body.ClientRequestID)
	templateID := strings.TrimSpace(body.TemplateID)
	language := strings.TrimSpace(body.Language)
	if requestID == "" || len(requestID) > 200 || templateID == "" || len(templateID) > 256 || language == "" || len(language) > 40 || len(purpose) > 80 {
		writeProblem(w, http.StatusBadRequest, "template inválido")
		return
	}
	sum := templatePayloadHash(convID, templateID, language, purpose)
	for try := 0; try < 5; try++ {
		claim, err := h.claimTemplate(r.Context(), p, convID, inbox, requestID, sum, body.ExpectedVersion)
		if err != nil {
			writeCommand(w, err)
			return
		}
		if claim.done {
			writeJSON(w, http.StatusOK, claim.result)
			return
		}
		sent, err := h.attendance.SendTemplate(withTemplateSlot(r.Context(), templateSlot{
			token: p.TokenID, request: requestID, hash: sum,
		}), attendanceService.Principal{
			UserID: claim.userID, MembershipID: claim.membership, TenantID: p.TenantID, Role: claim.role,
		}, attendanceService.TemplateSendInput{
			InboxID: inbox, ConversationID: convID, ExpectedVersion: body.ExpectedVersion,
			ClientRequestID: attendanceRequestID(p.TokenID, requestID), TemplateID: templateID, Language: language,
			Purpose: purpose, At: time.Now().UTC(),
		})
		if err != nil {
			writeCommand(w, err)
			return
		}
		finished, err := h.finishTemplate(r.Context(), p, requestID, sum, sent)
		if err != nil {
			writeCommand(w, err)
			return
		}
		if finished.done {
			code := http.StatusCreated
			if finished.result.Replayed {
				code = http.StatusOK
			}
			writeJSON(w, code, finished.result)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
}

func (h *handler) listAgents(w http.ResponseWriter, r *http.Request) {
	h.listDirectory(w, r, func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) (any, error) {
		rows, err := tx.Query(ctx, `
			SELECT id, role, status
			FROM memberships
			WHERE tenant_id = $1
			ORDER BY created_at, id
			LIMIT $2
		`, tenant, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []agentView{}
		for rows.Next() {
			var item agentView
			if err := rows.Scan(&item.ID, &item.Role, &item.Status); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return map[string]any{"agents": items}, rows.Err()
	})
}

func (h *handler) listSectors(w http.ResponseWriter, r *http.Request) {
	h.listDirectory(w, r, func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) (any, error) {
		rows, err := tx.Query(ctx, `
			SELECT id, name, active
			FROM sectors
			WHERE tenant_id = $1
			ORDER BY name, id
			LIMIT $2
		`, tenant, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []sectorView{}
		for rows.Next() {
			var item sectorView
			if err := rows.Scan(&item.ID, &item.Name, &item.Active); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return map[string]any{"sectors": items}, rows.Err()
	})
}

func (h *handler) listInboxes(w http.ResponseWriter, r *http.Request) {
	h.listDirectory(w, r, func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) (any, error) {
		rows, err := tx.Query(ctx, `
			SELECT id, name
			FROM inboxes
			WHERE tenant_id = $1
			ORDER BY name, id
			LIMIT $2
		`, tenant, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []inboxView{}
		for rows.Next() {
			var item inboxView
			if err := rows.Scan(&item.ID, &item.Name); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return map[string]any{"inboxes": items}, rows.Err()
	})
}

func (h *handler) listDirectory(w http.ResponseWriter, r *http.Request, load func(context.Context, pgx.Tx, uuid.UUID, int) (any, error)) {
	p, ok := h.admit(w, r, "directory.read")
	if !ok {
		return
	}
	limit, err := messageLimit(r)
	if err != nil {
		writeAccess(w, err)
		return
	}
	var body any
	err = h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		body, err = load(ctx, tx, p.TenantID, limit)
		return err
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func decodeJSON(r *http.Request, dest any) error {
	return decodeJSONLimit(r, dest, 8192)
}

func decodeJSONLimit(r *http.Request, dest any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("corpo JSON inválido")
	}
	return nil
}

var errNoAssignee = errors.New("conversa sem atendente")

func writeCommand(w http.ResponseWriter, err error) {
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
	case errors.Is(err, attendanceService.ErrForbidden), errors.Is(err, attendanceService.ErrInboxForbidden), errors.Is(err, attendanceService.ErrTenantInactive):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, store.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "template inválido")
	case errors.Is(err, store.ErrStaleVersion), errors.Is(err, store.ErrClosed), errors.Is(err, store.ErrIdempotencyConflict), errors.Is(err, errPayloadConflict):
		writeProblem(w, http.StatusConflict, err.Error())
	case errors.Is(err, attendanceService.ErrJobRequired), errors.Is(err, attendanceService.ErrChannelUnavailable), errors.Is(err, errTemplateBind):
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	default:
		writeAccess(w, err)
	}
}
