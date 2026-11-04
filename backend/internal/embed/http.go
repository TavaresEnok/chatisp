package embed

import (
	"encoding/json"
	"errors"
	"net/http"

	attendanceStore "chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/urlguard"
	"chatisp/internal/sectors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	Panels         Service
	TrustedOrigins []string
}

type handler struct{ d Dependencies }

func Mount(r chi.Router, d Dependencies) {
	h := handler{d}
	r.Get("/api/panel/embed", h.get)
	r.Put("/api/panel/embed", h.put)
	r.Get("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/embed", h.view)
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Panels.Settings(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) put(w http.ResponseWriter, r *http.Request) {
	if !originAllowed(h.d.TrustedOrigins, r.Header.Get("Origin")) {
		problem(w, http.StatusForbidden, "acesso negado")
		return
	}
	var draft Draft
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&draft); err != nil {
		problem(w, http.StatusUnprocessableEntity, "configuração inválida")
		return
	}
	p, err := h.principal(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Panels.Save(r.Context(), p, draft)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) view(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	inbox, err := uuid.Parse(chi.URLParam(r, "inbox_id"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "configuração inválida")
		return
	}
	conversation, err := uuid.Parse(chi.URLParam(r, "conversation_id"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "configuração inválida")
		return
	}
	out, err := h.d.Panels.Conversation(r.Context(), p, inbox, conversation)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func originAllowed(trusted []string, origin string) bool {
	for _, item := range trusted {
		if item != "" && item == origin {
			return true
		}
	}
	return false
}

func (h handler) principal(r *http.Request, write bool) (Principal, error) {
	if write && !originAllowed(h.d.TrustedOrigins, r.Header.Get("Origin")) {
		return Principal{}, auth.ErrForbidden
	}
	if h.d.Auth == nil || h.d.Panels.DB == nil || h.d.Panels.Policy == nil {
		return Principal{}, ErrUnavailable
	}
	session := ""
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		session = cookie.Value
	}
	p, err := h.d.Auth.Authenticate(r.Context(), session, r.Header.Get("X-CSRF-Token"), write)
	if err != nil {
		return Principal{}, err
	}
	if p.IsSuperadmin || p.EnrollmentOnly {
		return Principal{}, auth.ErrForbidden
	}
	return Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, nil
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		problem(w, http.StatusUnauthorized, "não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, sectors.ErrForbidden):
		problem(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, sectors.ErrNotFound), errors.Is(err, attendanceStore.ErrNotFound):
		problem(w, http.StatusNotFound, "conversa não encontrada")
	case errors.Is(err, ErrOrigin):
		problem(w, http.StatusUnprocessableEntity, "origem do painel recusada")
	case errors.Is(err, ErrURL), errors.Is(err, urlguard.ErrDestination), errors.Is(err, urlguard.ErrScheme), errors.Is(err, urlguard.ErrPort), errors.Is(err, urlguard.ErrUserinfo):
		problem(w, http.StatusUnprocessableEntity, "url do painel recusada")
	case errors.Is(err, ErrInvalid):
		problem(w, http.StatusUnprocessableEntity, "configuração inválida")
	default:
		problem(w, http.StatusServiceUnavailable, "serviço indisponível")
	}
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "urn:chatisp:problem:embed",
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
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
