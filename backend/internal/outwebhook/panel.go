package outwebhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/urlguard"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PanelAuth is the panel session check. *auth.Service implements it.
type PanelAuth interface {
	Authenticate(ctx context.Context, rawToken, csrf string, requireCSRF bool) (auth.Principal, error)
}

// PanelDeps registers subscription maintenance on the panel router.
type PanelDeps struct {
	Auth           PanelAuth
	Store          *db.Store
	Ring           *crypto.Ring
	Policy         *urlguard.Policy
	Mode           string
	TrustedOrigins []string
}

// MountPanel registers create, list, disable and delivery status routes.
func MountPanel(r chi.Router, deps PanelDeps) {
	h := panelHandler{deps: deps}
	r.Method(http.MethodPost, "/api/panel/outbound-webhooks", http.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/api/panel/outbound-webhooks", http.HandlerFunc(h.list))
	r.Method(http.MethodPost, "/api/panel/outbound-webhooks/{id}/disable", http.HandlerFunc(h.disable))
	r.Method(http.MethodGet, "/api/panel/outbound-webhooks/{id}/deliveries", http.HandlerFunc(h.deliveries))
}

type panelHandler struct{ deps PanelDeps }

type subscriptionBody struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

type subscriptionView struct {
	ID      uuid.UUID `json:"id"`
	URL     string    `json:"url"`
	Events  []string  `json:"events"`
	Enabled bool      `json:"enabled"`
	Secret  string    `json:"signing_secret,omitempty"`
}

type deliveryView struct {
	ID         uuid.UUID `json:"id"`
	Type       string    `json:"type"`
	SourceID   uuid.UUID `json:"source_id"`
	Status     string    `json:"status"`
	Attempt    int       `json:"attempt"`
	LastStatus int       `json:"last_status"`
	NextAt     time.Time `json:"next_attempt_at"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (h panelHandler) create(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, true)
	if err != nil {
		writePanel(w, err)
		return
	}
	var body subscriptionBody
	if err = decodePanel(r, &body); err != nil {
		writePanel(w, err)
		return
	}
	events := map[string]bool{}
	names := make([]string, 0, len(body.Events))
	for _, name := range body.Events {
		name = strings.TrimSpace(name)
		if !knownEvent(name) || events[name] {
			writePanel(w, ErrEvent)
			return
		}
		events[name] = true
		names = append(names, name)
	}
	policy := h.deps.Policy
	if policy == nil {
		policy, err = urlguard.NewPolicy()
		if err != nil {
			writePanel(w, err)
			return
		}
	}
	if err = AllowDestination(policy, h.deps.Mode, strings.TrimSpace(body.URL)); err != nil {
		writePanel(w, err)
		return
	}
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		writePanel(w, errors.New("serviço indisponível"))
		return
	}
	shown := hex.EncodeToString(buf)
	sub := Subscription{
		ID: uuid.New(), TenantID: principal.TenantID, URL: strings.TrimSpace(body.URL),
		Secret: []byte(shown), Events: events, Enabled: true,
	}
	stored, err := SealSubscription(h.deps.Ring, sub)
	if err != nil {
		writePanel(w, err)
		return
	}
	err = h.deps.Store.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return InsertSubscription(ctx, tx, stored)
	})
	if err != nil {
		writePanel(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, subscriptionView{
		ID: sub.ID, URL: sub.URL, Events: names, Enabled: true, Secret: shown,
	})
}

func (h panelHandler) list(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, false)
	if err != nil {
		writePanel(w, err)
		return
	}
	var views []subscriptionView
	err = h.deps.Store.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := ListSubscriptions(ctx, tx, principal.TenantID)
		if err != nil {
			return err
		}
		views = make([]subscriptionView, 0, len(rows))
		for _, row := range rows {
			names := make([]string, 0, len(row.Events))
			for name, on := range row.Events {
				if on {
					names = append(names, name)
				}
			}
			views = append(views, subscriptionView{ID: row.ID, URL: row.URL, Events: names, Enabled: row.Enabled})
		}
		return nil
	})
	if err != nil {
		writePanel(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": views})
}

func (h panelHandler) disable(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, true)
	if err != nil {
		writePanel(w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writePanel(w, ErrEvent)
		return
	}
	err = h.deps.Store.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return SetEnabled(ctx, tx, principal.TenantID, id, false)
	})
	if err != nil {
		writePanel(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": false})
}

func (h panelHandler) deliveries(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, false)
	if err != nil {
		writePanel(w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writePanel(w, ErrEvent)
		return
	}
	var views []deliveryView
	err = h.deps.Store.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := LoadSubscription(ctx, tx, principal.TenantID, id); err != nil {
			return err
		}
		rows, err := ListDeliveries(ctx, tx, principal.TenantID, id)
		if err != nil {
			return err
		}
		views = make([]deliveryView, 0, len(rows))
		for _, row := range rows {
			views = append(views, deliveryView{
				ID: row.ID, Type: row.Type, SourceID: row.SourceID, Status: row.Status,
				Attempt: row.Attempt, LastStatus: row.LastStatus, NextAt: row.NextAt, OccurredAt: row.OccurredAt,
			})
		}
		return nil
	})
	if err != nil {
		writePanel(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": views})
}

func (h panelHandler) principal(r *http.Request, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Store == nil || h.deps.Ring == nil {
		return auth.Principal{}, errPanelUnavailable
	}
	if write && !originAllowed(h.deps.TrustedOrigins, r.Header.Get("Origin")) {
		return auth.Principal{}, auth.ErrForbidden
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	raw := ""
	if err == nil {
		raw = cookie.Value
	}
	if raw == "" {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	principal, err := h.deps.Auth.Authenticate(r.Context(), raw, r.Header.Get(auth.CSRFHeader), write)
	if err != nil {
		return auth.Principal{}, err
	}
	if principal.Role != "admin" || principal.TenantID == uuid.Nil || principal.IsSuperadmin || principal.EnrollmentOnly {
		return auth.Principal{}, auth.ErrForbidden
	}
	return principal, nil
}

func originAllowed(trusted []string, origin string) bool {
	for _, item := range trusted {
		if item != "" && item == origin {
			return true
		}
	}
	return false
}

func decodePanel(r *http.Request, dest any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return ErrEvent
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ErrEvent
	}
	return nil
}

var errPanelUnavailable = errors.New("serviço indisponível")

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writePanel(w http.ResponseWriter, err error) {
	status, detail := http.StatusInternalServerError, "ocorreu um erro interno"
	switch {
	case errors.Is(err, errPanelUnavailable):
		status, detail = http.StatusServiceUnavailable, "serviço indisponível"
	case errors.Is(err, auth.ErrUnauthenticated):
		status, detail = http.StatusUnauthorized, "não autenticado"
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		status, detail = http.StatusForbidden, "acesso negado"
	case errors.Is(err, ErrStored):
		status, detail = http.StatusNotFound, "webhook não encontrado"
	case errors.Is(err, ErrEvent), errors.Is(err, urlguard.ErrDestination), errors.Is(err, urlguard.ErrScheme), errors.Is(err, urlguard.ErrPort), errors.Is(err, urlguard.ErrUserinfo):
		status, detail = http.StatusUnprocessableEntity, "destino ou evento inválido"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": detail, "status": status, "detail": detail})
}
