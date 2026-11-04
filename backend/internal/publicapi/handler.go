package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// NewHandler serves the public read routes under /v1.
// Mount it at /api/public. A nil store answers 503 and does not read data.
func NewHandler(store *db.Store) http.Handler {
	h := &handler{store: store, limit: newLimiter(), now: time.Now}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
		})
	})
	router.Get("/v1/conversations/{ref}", h.conversation)
	router.Get("/v1/conversations/{ref}/messages", h.messages)
	router.Get("/v1/conversations/{ref}/contact", h.contact)
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusNotFound, "o caminho solicitado não existe")
	})
	return router
}

type handler struct {
	store *db.Store
	limit *limiter
	now   func() time.Time
}

type principal struct {
	TokenID      uuid.UUID
	TenantID     uuid.UUID
	Scope        string
	Revoked      bool
	RequestLimit int
	TenantStatus string
}

type limitError struct {
	detail string
	retry  int
}

func (e limitError) Error() string { return e.detail }

type conversationView struct {
	ID               uuid.UUID  `json:"id"`
	Protocol         string     `json:"protocol"`
	Status           string     `json:"status"`
	InboxID          uuid.UUID  `json:"inbox_id"`
	ContactID        uuid.UUID  `json:"contact_id"`
	SectorID         *uuid.UUID `json:"sector_id"`
	AssigneeID       *uuid.UUID `json:"assignee_id"`
	Controller       string     `json:"controller"`
	CycleNumber      int        `json:"cycle_number"`
	ExternalProtocol *string    `json:"external_protocol"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type messageView struct {
	ID         uuid.UUID  `json:"id"`
	Direction  string     `json:"direction"`
	Origin     string     `json:"origin"`
	Body       string     `json:"body"`
	IngestedAt time.Time  `json:"ingested_at"`
	TrustedAt  *time.Time `json:"trusted_at"`
}

type messageList struct {
	ConversationID uuid.UUID     `json:"conversation_id"`
	Messages       []messageView `json:"messages"`
}

type contactView struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Phone   *string   `json:"phone"`
	Email   *string   `json:"email"`
	Blocked bool      `json:"blocked"`
}

func (h *handler) conversation(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r)
	if !ok {
		return
	}
	var view conversationView
	err := h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadConversation(ctx, tx, p.TenantID, ref)
		if err != nil {
			return err
		}
		view = row
		return nil
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) messages(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r)
	if !ok {
		return
	}
	limit, err := messageLimit(r)
	if err != nil {
		writeAccess(w, err)
		return
	}
	var list messageList
	err = h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadConversation(ctx, tx, p.TenantID, ref)
		if err != nil {
			return err
		}
		items, err := loadMessages(ctx, tx, p.TenantID, row.ID, limit)
		if err != nil {
			return err
		}
		list = messageList{ConversationID: row.ID, Messages: items}
		return nil
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *handler) contact(w http.ResponseWriter, r *http.Request) {
	p, ref, ok := h.gate(w, r)
	if !ok {
		return
	}
	var view contactView
	err := h.store.WithTenantTx(r.Context(), p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadConversation(ctx, tx, p.TenantID, ref)
		if err != nil {
			return err
		}
		if row.ContactID == uuid.Nil {
			return errNoContact
		}
		item, err := loadContact(ctx, tx, p.TenantID, row.ContactID)
		if err != nil {
			return err
		}
		view = item
		return nil
	})
	if err != nil {
		writeAccess(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) gate(w http.ResponseWriter, r *http.Request) (principal, string, bool) {
	if h.store == nil {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return principal{}, "", false
	}
	ref := chi.URLParam(r, "ref")
	if !validRef(ref) {
		writeProblem(w, http.StatusBadRequest, "identificador inválido")
		return principal{}, "", false
	}
	p, err := h.authenticate(r)
	if err != nil {
		writeAccess(w, err)
		return principal{}, "", false
	}
	return p, ref, true
}

func (h *handler) authenticate(r *http.Request) (principal, error) {
	raw, err := bearerToken(r)
	if err != nil {
		return principal{}, err
	}
	var p principal
	err = h.store.WithPublicRead(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT token_id, tenant_id, scope, revoked, request_limit, tenant_status
			FROM public_read_resolve($1)
		`, auth.HashSecret(raw)).Scan(
			&p.TokenID, &p.TenantID, &p.Scope, &p.Revoked, &p.RequestLimit, &p.TenantStatus,
		)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return principal{}, errInvalid
	}
	if err != nil {
		return principal{}, err
	}
	if p.Revoked {
		return principal{}, errRevoked
	}
	if p.Scope != "read" {
		return principal{}, errScope
	}
	if p.TenantStatus != "active" {
		return principal{}, errSuspended
	}
	now := h.now()
	if ok, retry := h.limit.allow("token:"+p.TokenID.String(), p.RequestLimit, now); !ok {
		return principal{}, limitError{detail: "limite do token excedido", retry: retry}
	}
	if ok, retry := h.limit.allow("tenant:"+p.TenantID.String(), tenantWindowLimit, now); !ok {
		return principal{}, limitError{detail: "limite do provedor excedido", retry: retry}
	}
	return p, nil
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errMissing
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errInvalid
	}
	token := header[len(prefix):]
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t") {
		return "", errInvalid
	}
	return token, nil
}

func validRef(ref string) bool {
	if ref == "" || len(ref) > 64 || strings.TrimSpace(ref) != ref {
		return false
	}
	return !strings.ContainsAny(ref, " \t\r\n")
}

func messageLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, errBadLimit
	}
	return n, nil
}

func loadConversation(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (conversationView, error) {
	column := "protocol = $2"
	var arg any = ref
	if id, err := uuid.Parse(ref); err == nil {
		column = "id = $2"
		arg = id
	}
	var view conversationView
	var contact *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, protocol, status, inbox_id, contact_id, sector_id, assignee_id,
		       controller, cycle_number, external_protocol, created_at, updated_at
		FROM conversations
		WHERE tenant_id = $1 AND `+column, tenant, arg).Scan(
		&view.ID, &view.Protocol, &view.Status, &view.InboxID, &contact, &view.SectorID,
		&view.AssigneeID, &view.Controller, &view.CycleNumber, &view.ExternalProtocol,
		&view.CreatedAt, &view.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return conversationView{}, errNotFound
	}
	if err != nil {
		return conversationView{}, err
	}
	if contact != nil {
		view.ContactID = *contact
	}
	return view, nil
}

func loadMessages(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, limit int) ([]messageView, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, direction, origin, body, ingested_at, trusted_at
		FROM messages
		WHERE tenant_id = $1 AND conversation_id = $2 AND visibility = 'public'
		ORDER BY ingested_at, id
		LIMIT $3
	`, tenant, conversation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []messageView{}
	for rows.Next() {
		var item messageView
		if err := rows.Scan(&item.ID, &item.Direction, &item.Origin, &item.Body, &item.IngestedAt, &item.TrustedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadContact(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) (contactView, error) {
	var view contactView
	err := tx.QueryRow(ctx, `
		SELECT id, name, phone, email, blocked
		FROM contacts
		WHERE tenant_id = $1 AND id = $2
	`, tenant, contact).Scan(&view.ID, &view.Name, &view.Phone, &view.Email, &view.Blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return contactView{}, errNoContact
	}
	if err != nil {
		return contactView{}, err
	}
	return view, nil
}

var (
	errMissing   = errors.New("credencial ausente")
	errInvalid   = errors.New("token inválido")
	errRevoked   = errors.New("token revogado")
	errScope     = errors.New("escopo insuficiente")
	errSuspended = errors.New("provedor suspenso")
	errNotFound  = errors.New("conversa não encontrada")
	errNoContact = errors.New("contato não vinculado")
	errBadLimit  = errors.New("limite inválido")
)

func writeAccess(w http.ResponseWriter, err error) {
	var limited limitError
	if errors.As(err, &limited) {
		w.Header().Set("Retry-After", strconv.Itoa(limited.retry))
		writeProblem(w, http.StatusTooManyRequests, limited.detail)
		return
	}
	switch {
	case errors.Is(err, errMissing), errors.Is(err, errInvalid), errors.Is(err, errRevoked):
		writeProblem(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, errScope), errors.Is(err, errSuspended):
		writeProblem(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errNotFound), errors.Is(err, errNoContact):
		writeProblem(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errBadLimit):
		writeProblem(w, http.StatusBadRequest, err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "ocorreu um erro interno")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   problemType(status),
		"title":  problemTitle(status),
		"status": status,
		"detail": detail,
	})
}

func problemType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "urn:chatisp:problem:bad-request"
	case http.StatusUnauthorized:
		return "urn:chatisp:problem:unauthorized"
	case http.StatusForbidden:
		return "urn:chatisp:problem:forbidden"
	case http.StatusNotFound:
		return "urn:chatisp:problem:not-found"
	case http.StatusTooManyRequests:
		return "urn:chatisp:problem:too-many-requests"
	case http.StatusServiceUnavailable:
		return "urn:chatisp:problem:unavailable"
	default:
		return "urn:chatisp:problem:internal"
	}
}

func problemTitle(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Requisição inválida"
	case http.StatusUnauthorized:
		return "Não autenticado"
	case http.StatusForbidden:
		return "Acesso negado"
	case http.StatusNotFound:
		return "Recurso não encontrado"
	case http.StatusTooManyRequests:
		return "Muitas tentativas"
	case http.StatusServiceUnavailable:
		return "Serviço indisponível"
	default:
		return "Erro interno"
	}
}
