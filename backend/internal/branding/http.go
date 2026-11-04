package branding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
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
	s Service
}

// Mount registers the panel routes on the process router.
// The routes stay outside the generated OpenAPI document.
func Mount(r chi.Router, d Dependencies) {
	h := handler{d: d, s: Service{DB: d.Store}}
	r.Get("/api/panel/branding", h.get)
	r.Put("/api/panel/branding", h.putName)
	r.Get("/api/panel/branding/logo", h.getLogo)
	r.Put("/api/panel/branding/logo", h.putLogo)
	r.Delete("/api/panel/branding/logo", h.deleteLogo)
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := h.s.Get(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h handler) putName(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := decode(r, &body); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "dados inválidos")
		return
	}
	saved, err := h.s.SaveName(r.Context(), p, body.DisplayName)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h handler) putLogo(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxLogoBytes+1))
	if err != nil || len(raw) > MaxLogoBytes {
		writeProblem(w, http.StatusUnprocessableEntity, "dados inválidos")
		return
	}
	saved, err := h.s.SaveLogo(r.Context(), p, r.Header.Get("Content-Type"), raw)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h handler) deleteLogo(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	saved, err := h.s.RemoveLogo(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h handler) getLogo(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	img, err := h.s.Logo(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !Matches(img.ContentType, img.Bytes) {
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img.Bytes)
}

func (h handler) principal(r *http.Request, write bool) (Principal, error) {
	if h.d.Auth == nil || h.d.Store == nil {
		return Principal{}, ErrUnavailable
	}
	if write && !originOK(r, h.d.TrustedOrigins) {
		return Principal{}, ErrForbidden
	}
	token := ""
	if cookie, err := r.Cookie("__Host-session"); err == nil {
		token = cookie.Value
	}
	actor, err := h.d.Auth.Authenticate(r.Context(), token, r.Header.Get("X-CSRF-Token"), write)
	if err != nil {
		return Principal{}, err
	}
	if actor.IsSuperadmin || actor.TenantID == uuid.Nil || actor.MembershipID == uuid.Nil {
		return Principal{}, ErrForbidden
	}
	if write && (actor.Role != "admin" || actor.EnrollmentOnly) {
		return Principal{}, ErrForbidden
	}
	return Principal{
		TenantID: actor.TenantID, UserID: actor.UserID, MembershipID: actor.MembershipID,
		Role: actor.Role, IsSuperadmin: actor.IsSuperadmin, EnrollmentOnly: actor.EnrollmentOnly,
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
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("corpo extra")
	}
	return nil
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProblem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, ErrForbidden), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF):
		writeProblem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "logo não encontrado")
	case errors.Is(err, ErrInvalid):
		writeProblem(w, http.StatusUnprocessableEntity, "dados inválidos")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{
		"type":   "urn:chatisp:problem:branding",
		"title":  detail,
		"status": status,
		"detail": detail,
	})
}
