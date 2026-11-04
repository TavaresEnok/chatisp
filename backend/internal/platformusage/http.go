package platformusage

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// NewHandler serves GET /api/platform/usage outside the generated OpenAPI document.
// Nil auth or store answers 503. Provider roles receive 403.
func NewHandler(identity *auth.Service, store *db.Store) http.Handler {
	svc := New(store)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeProblem(w, http.StatusMethodNotAllowed, "método não permitido para este caminho")
			return
		}
		if identity == nil || store == nil {
			writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
			return
		}
		cookie, _ := r.Cookie(auth.SessionCookieName)
		token := ""
		if cookie != nil {
			token = cookie.Value
		}
		actor, err := identity.Authenticate(r.Context(), token, r.Header.Get(auth.CSRFHeader), false)
		if err != nil {
			writeAuth(w, err)
			return
		}
		if actor.EnrollmentOnly || !actor.IsSuperadmin {
			writeProblem(w, http.StatusForbidden, "acesso negado")
			return
		}
		from, to, err := period(r)
		if err != nil {
			writeProblem(w, http.StatusUnprocessableEntity, "período inválido")
			return
		}
		snap, err := svc.Read(r.Context(), actor, from, to)
		if err != nil {
			writeAuth(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(snap)
	})
}

func period(r *http.Request) (time.Time, time.Time, error) {
	rawFrom := r.URL.Query().Get("from")
	rawTo := r.URL.Query().Get("to")
	if len(rawFrom) == 0 || len(rawFrom) > 40 || len(rawTo) == 0 || len(rawTo) > 40 {
		return time.Time{}, time.Time{}, ErrInvalidPeriod
	}
	from, err := time.Parse(time.RFC3339, rawFrom)
	if err != nil {
		return time.Time{}, time.Time{}, ErrInvalidPeriod
	}
	to, err := time.Parse(time.RFC3339, rawTo)
	if err != nil {
		return time.Time{}, time.Time{}, ErrInvalidPeriod
	}
	return from, to, nil
}

func writeAuth(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProblem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, ErrInvalidPeriod):
		writeProblem(w, http.StatusUnprocessableEntity, "período inválido")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	type problem struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	titles := map[int]string{
		http.StatusUnauthorized:        "Não autenticado",
		http.StatusForbidden:           "Acesso negado",
		http.StatusMethodNotAllowed:    "Método não permitido",
		http.StatusUnprocessableEntity: "Dados inválidos",
		http.StatusServiceUnavailable:  "Serviço indisponível",
	}
	types := map[int]string{
		http.StatusUnauthorized:        "urn:chatisp:problem:unauthorized",
		http.StatusForbidden:           "urn:chatisp:problem:forbidden",
		http.StatusMethodNotAllowed:    "urn:chatisp:problem:method-not-allowed",
		http.StatusUnprocessableEntity: "urn:chatisp:problem:invalid",
		http.StatusServiceUnavailable:  "urn:chatisp:problem:unavailable",
	}
	title := titles[status]
	if title == "" {
		title = "Erro interno"
	}
	kind := types[status]
	if kind == "" {
		kind = "urn:chatisp:problem:internal"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: kind, Title: title, Status: status, Detail: detail})
}
