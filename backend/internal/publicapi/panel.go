package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// TokenPanelAuth is the panel session check. *auth.Service implements it.
type TokenPanelAuth interface {
	Authenticate(ctx context.Context, rawToken, csrf string, requireCSRF bool) (auth.Principal, error)
}

// TokenPanelDeps registers API token maintenance on the panel router.
type TokenPanelDeps struct {
	Auth           TokenPanelAuth
	Store          *db.Store
	TrustedOrigins []string
}

// MountTokenPanel registers create, list and revoke routes for API tokens.
func MountTokenPanel(router chi.Router, deps TokenPanelDeps) {
	h := tokenPanel{deps: deps}
	router.Method(http.MethodPost, "/api/panel/api-tokens", http.HandlerFunc(h.create))
	router.Method(http.MethodGet, "/api/panel/api-tokens", http.HandlerFunc(h.list))
	router.Method(http.MethodPost, "/api/panel/api-tokens/{id}/revoke", http.HandlerFunc(h.revoke))
}

type tokenPanel struct{ deps TokenPanelDeps }

type tokenBody struct {
	Scopes       []string `json:"scopes"`
	RequestLimit int      `json:"request_limit"`
}

type tokenView struct {
	ID           uuid.UUID `json:"id"`
	Scopes       []string  `json:"scopes"`
	RequestLimit int       `json:"request_limit"`
	Revoked      bool      `json:"revoked"`
	CreatedAt    time.Time `json:"created_at"`
	Secret       string    `json:"secret,omitempty"`
}

func (h tokenPanel) create(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, true)
	if err != nil {
		h.writeAuth(w, err)
		return
	}
	var body tokenBody
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&body); err != nil {
		writeTokenProblem(w, http.StatusUnprocessableEntity, "token inválido")
		return
	}
	raw, id, err := IssueScoped(r.Context(), h.deps.Store, principal.TenantID, body.Scopes, body.RequestLimit)
	if err != nil {
		status, detail := tokenStatus(err)
		writeTokenProblem(w, status, detail)
		return
	}
	writeJSON(w, http.StatusCreated, tokenView{
		ID: id, Scopes: body.Scopes, RequestLimit: body.RequestLimit, Secret: raw, CreatedAt: time.Now().UTC(),
	})
}

func (h tokenPanel) list(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, false)
	if err != nil {
		h.writeAuth(w, err)
		return
	}
	rows, err := listScoped(r.Context(), h.deps.Store, principal.TenantID)
	if err != nil {
		status, detail := tokenStatus(err)
		writeTokenProblem(w, status, detail)
		return
	}
	views := make([]tokenView, 0, len(rows))
	for _, row := range rows {
		views = append(views, tokenView{
			ID: row.ID, Scopes: row.Scopes, RequestLimit: row.RequestLimit, Revoked: row.Revoked, CreatedAt: row.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": views})
}

func (h tokenPanel) revoke(w http.ResponseWriter, r *http.Request) {
	principal, err := h.principal(r, true)
	if err != nil {
		h.writeAuth(w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeTokenProblem(w, http.StatusUnprocessableEntity, "token inválido")
		return
	}
	if err = RevokeScoped(r.Context(), h.deps.Store, principal.TenantID, id); err != nil {
		status, detail := tokenStatus(err)
		writeTokenProblem(w, status, detail)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "revoked": true})
}

func (h tokenPanel) writeAuth(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTokenUnavailable):
		writeTokenProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		writeTokenProblem(w, http.StatusForbidden, "acesso negado")
	default:
		writeTokenProblem(w, http.StatusUnauthorized, "não autenticado")
	}
}

func (h tokenPanel) principal(r *http.Request, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Store == nil {
		return auth.Principal{}, errTokenUnavailable
	}
	if write && !tokenOriginAllowed(h.deps.TrustedOrigins, r.Header.Get("Origin")) {
		return auth.Principal{}, auth.ErrForbidden
	}
	raw := ""
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
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

func tokenOriginAllowed(trusted []string, origin string) bool {
	for _, item := range trusted {
		if item != "" && item == origin {
			return true
		}
	}
	return false
}

var errTokenUnavailable = errors.New("serviço indisponível")

func writeTokenProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": detail, "status": status, "detail": detail})
}
