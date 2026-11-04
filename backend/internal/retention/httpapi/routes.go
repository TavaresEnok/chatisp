// Package httpapi exposes retention and demand export on the panel router.
// These routes stay outside the generated OpenAPI document.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/retention"
)

// Authenticator is the panel session check already used by the process.
type Authenticator interface {
	Authenticate(ctx context.Context, rawToken, csrf string, requireCSRF bool) (auth.Principal, error)
}

// Dependencies wires the existing session service and the application store.
type Dependencies struct {
	Auth           Authenticator
	Store          *db.Store
	TrustedOrigins []string
}

type handler struct {
	d Dependencies
	s retention.Service
}

// Register mounts the panel routes. It does not change the OpenAPI document.
func Register(r chi.Router, d Dependencies) {
	h := handler{d: d, s: retention.Service{DB: d.Store}}
	r.Get("/api/panel/retention", h.getPolicy)
	r.Put("/api/panel/retention", h.putPolicy)
	r.Post("/api/panel/retention/purge", h.purge)
	r.Get("/api/panel/plan-limits", h.limits)
	r.Get("/api/panel/contacts/{contact_id}/demands", h.export)
}

func (h handler) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p.Role == "agent" {
		writeProblem(w, http.StatusForbidden, "acesso negado")
		return
	}
	body, err := h.s.GetPolicy(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h handler) putPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p.Role != "admin" {
		writeProblem(w, http.StatusForbidden, "acesso negado")
		return
	}
	var body retention.Policy
	if err := decode(r, &body); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "dados inválidos")
		return
	}
	saved, err := h.s.SavePolicy(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h handler) purge(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.s.RunPurge(r.Context(), p)
	if errors.Is(err, retention.ErrPurgeDisabled) {
		writeJSON(w, http.StatusConflict, result)
		return
	}
	writeErr(w, err)
}

func (h handler) limits(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p.Role == "agent" {
		writeProblem(w, http.StatusForbidden, "acesso negado")
		return
	}
	body, err := h.s.PlanUsage(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h handler) export(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && !strings.EqualFold(format, "json") && !strings.EqualFold(format, "csv") {
		writeProblem(w, http.StatusUnprocessableEntity, "formato inválido")
		return
	}
	contact, err := uuid.Parse(chi.URLParam(r, "contact_id"))
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "contato inválido")
		return
	}
	doc, err := h.s.ExportDemands(r.Context(), p, contact)
	if err != nil {
		writeErr(w, err)
		return
	}
	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="demandas.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, retention.FormatCSV(doc))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h handler) principal(r *http.Request, write bool) (retention.Principal, error) {
	if h.d.Auth == nil || h.d.Store == nil {
		return retention.Principal{}, retention.ErrUnavailable
	}
	if write && !originOK(r, h.d.TrustedOrigins) {
		return retention.Principal{}, retention.ErrForbidden
	}
	cookie, err := r.Cookie("__Host-session")
	token := ""
	if err == nil {
		token = cookie.Value
	}
	actor, err := h.d.Auth.Authenticate(r.Context(), token, r.Header.Get("X-CSRF-Token"), write)
	if err != nil {
		return retention.Principal{}, err
	}
	if actor.IsSuperadmin || actor.EnrollmentOnly || actor.TenantID == uuid.Nil || actor.MembershipID == uuid.Nil {
		return retention.Principal{}, retention.ErrForbidden
	}
	return retention.Principal{
		TenantID: actor.TenantID, UserID: actor.UserID, MembershipID: actor.MembershipID,
		Role: actor.Role, IsSuperadmin: actor.IsSuperadmin,
	}, nil
}

func originOK(r *http.Request, trusted []string) bool {
	origin := r.Header.Get("Origin")
	for _, allowed := range trusted {
		if allowed != "" && origin == allowed {
			return true
		}
	}
	return false
}

func decode(r *http.Request, dest any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProblem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, retention.ErrForbidden), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, retention.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "contato não encontrado")
	case errors.Is(err, retention.ErrInvalid):
		writeProblem(w, http.StatusUnprocessableEntity, "dados inválidos")
	case errors.Is(err, retention.ErrPurgeDisabled):
		writeProblem(w, http.StatusConflict, "expurgo automático desligado")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{
		"type":   "urn:chatisp:problem:retention",
		"title":  detail,
		"status": status,
		"detail": detail,
	})
}
