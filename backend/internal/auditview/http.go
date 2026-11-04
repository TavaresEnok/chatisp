package auditview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// Path is the provider audit read. It is not part of the generated OpenAPI document.
const Path = "/api/panel/audit"

// Dependencies wires session checks and the tenant database.
type Dependencies struct {
	Auth  *auth.Service
	Store *db.Store
}

// Mount registers GET Path on the panel router.
func Mount(router chi.Router, deps Dependencies) {
	if router == nil {
		return
	}
	router.Get(Path, serve(deps))
}

func serve(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Auth == nil || deps.Store == nil {
			writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
			return
		}
		principal, err := principalFrom(r, deps.Auth)
		if err != nil {
			writeAuth(w, err)
			return
		}
		if !allowed(principal) {
			writeProblem(w, http.StatusForbidden, "acesso negado")
			return
		}
		filter, err := filterFrom(r)
		if err != nil {
			writeProblem(w, http.StatusUnprocessableEntity, "período inválido")
			return
		}
		var page Page
		err = deps.Store.WithTenantTx(r.Context(), principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var queryErr error
			page, queryErr = List(ctx, tx, Actor{TenantID: principal.TenantID, Role: principal.Role}, filter)
			return queryErr
		})
		if err != nil {
			writeList(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(page)
	}
}

func principalFrom(r *http.Request, service *auth.Service) (auth.Principal, error) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return service.Authenticate(r.Context(), cookie.Value, "", false)
}

func allowed(principal auth.Principal) bool {
	if principal.IsSuperadmin || principal.EnrollmentOnly || principal.TenantID == uuid.Nil || principal.MembershipID == uuid.Nil {
		return false
	}
	return principal.Role == "admin"
}

func filterFrom(r *http.Request) (Filter, error) {
	query := r.URL.Query()
	filter := Filter{From: query.Get("from"), To: query.Get("to")}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return Filter{}, ErrInvalid
		}
		filter.Limit = limit
	}
	filter.Cursor = query.Get("cursor")
	return filter, nil
}

func writeAuth(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProblem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeList(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, ErrInvalid):
		writeProblem(w, http.StatusUnprocessableEntity, "período inválido")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	title := "Requisição inválida"
	kind := "about:blank"
	switch status {
	case http.StatusUnauthorized:
		title, kind = "Não autenticado", "urn:chatisp:problem:unauthorized"
	case http.StatusForbidden:
		title, kind = "Acesso negado", "urn:chatisp:problem:forbidden"
	case http.StatusUnprocessableEntity:
		title, kind = "Dados inválidos", "urn:chatisp:problem:invalid"
	case http.StatusServiceUnavailable:
		title, kind = "Serviço indisponível", "urn:chatisp:problem:unavailable"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": kind, "title": title, "status": status, "detail": detail,
	})
}
