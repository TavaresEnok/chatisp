// Package httpapi exposes the bot hook on an existing chi router.
// Routes are plain JSON so the committed OpenAPI document stays unchanged.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"chatisp/internal/bothook"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Dependencies are the panel session and the hook service.
type Dependencies struct {
	Auth           *auth.Service
	Hooks          bothook.Service
	TrustedOrigins []string
}

type handler struct{ d Dependencies }

// Mount registers panel and bearer routes. A nil service answers 503.
func Mount(r chi.Router, d Dependencies) {
	h := handler{d}
	r.Method(http.MethodPut, "/api/panel/bot-hook/inboxes/{inbox_id}", http.HandlerFunc(h.install))
	r.Method(http.MethodPost, "/api/panel/bot-hook/inboxes/{inbox_id}/token", http.HandlerFunc(h.rotate))
	r.Method(http.MethodPut, "/api/panel/bot-hook/inboxes/{inbox_id}/switch", http.HandlerFunc(h.switchHook))
	r.Method(http.MethodGet, "/api/panel/bot-hook/inboxes/{inbox_id}", http.HandlerFunc(h.get))
	r.Method(http.MethodGet, "/api/panel/bot-hook/notices", http.HandlerFunc(h.notices))
	r.Method(http.MethodPost, "/api/bot-hook/conversations/{conversation_id}/reply", http.HandlerFunc(h.reply))
	r.Method(http.MethodPost, "/api/bot-hook/conversations/{conversation_id}/handoff", http.HandlerFunc(h.handoff))
}

func (h handler) install(w http.ResponseWriter, r *http.Request) {
	p, inbox, err := h.panel(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cmd bothook.InstallCommand
	if err = decode(r, &cmd); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.Install(r.Context(), p, inbox, cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) rotate(w http.ResponseWriter, r *http.Request) {
	p, inbox, err := h.panel(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cmd bothook.RotateCommand
	if err = decode(r, &cmd); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.Rotate(r.Context(), p, inbox, cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) switchHook(w http.ResponseWriter, r *http.Request) {
	p, inbox, err := h.panel(r, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cmd bothook.SwitchCommand
	if err = decode(r, &cmd); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.SetSwitch(r.Context(), p, inbox, cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	p, inbox, err := h.panel(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.Get(r.Context(), p, inbox)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) notices(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	inbox, err := uuid.Parse(r.URL.Query().Get("inbox_id"))
	if err != nil {
		writeErr(w, bothook.ErrInvalid)
		return
	}
	out, err := h.d.Hooks.Notices(r.Context(), p, inbox)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) reply(w http.ResponseWriter, r *http.Request) {
	token, conversation, err := bearer(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cmd bothook.ReplyCommand
	if err = decode(r, &cmd); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.Reply(r.Context(), token, conversation, cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) handoff(w http.ResponseWriter, r *http.Request) {
	token, conversation, err := bearer(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cmd bothook.HandoffCommand
	if err = decode(r, &cmd); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.d.Hooks.Handoff(r.Context(), token, conversation, cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handler) panel(r *http.Request, write bool) (bothook.Principal, uuid.UUID, error) {
	p, err := h.principal(r, write)
	if err != nil {
		return bothook.Principal{}, uuid.Nil, err
	}
	inbox, err := uuid.Parse(chi.URLParam(r, "inbox_id"))
	if err != nil {
		return bothook.Principal{}, uuid.Nil, bothook.ErrInvalid
	}
	return p, inbox, nil
}

func (h handler) principal(r *http.Request, write bool) (bothook.Principal, error) {
	if h.d.Auth == nil || h.d.Hooks.DB == nil {
		return bothook.Principal{}, bothook.ErrUnavailable
	}
	if write {
		allowed := false
		for _, origin := range h.d.TrustedOrigins {
			if origin != "" && r.Header.Get("Origin") == origin {
				allowed = true
				break
			}
		}
		if !allowed {
			return bothook.Principal{}, bothook.ErrForbidden
		}
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	raw := ""
	if err == nil {
		raw = cookie.Value
	}
	p, err := h.d.Auth.Authenticate(r.Context(), raw, r.Header.Get(auth.CSRFHeader), write)
	if err != nil {
		return bothook.Principal{}, err
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return bothook.Principal{}, bothook.ErrForbidden
	}
	return bothook.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}, nil
}

func bearer(r *http.Request) (string, uuid.UUID, error) {
	value := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(value, "Bearer ")
	if !ok || token == "" || strings.Contains(token, " ") {
		return "", uuid.Nil, bothook.ErrUnauthenticated
	}
	id, err := uuid.Parse(chi.URLParam(r, "conversation_id"))
	if err != nil {
		return "", uuid.Nil, bothook.ErrInvalid
	}
	return token, id, nil
}

func decode(r *http.Request, dest any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		return bothook.ErrInvalid
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	status, detail := http.StatusServiceUnavailable, "serviço indisponível"
	switch {
	case errors.Is(err, bothook.ErrUnauthenticated), errors.Is(err, auth.ErrUnauthenticated):
		status, detail = http.StatusUnauthorized, "não autenticado"
	case errors.Is(err, bothook.ErrForbidden), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, sectors.ErrForbidden):
		status, detail = http.StatusForbidden, "acesso negado"
	case errors.Is(err, bothook.ErrNotFound), errors.Is(err, sectors.ErrNotFound):
		status, detail = http.StatusNotFound, "gancho não encontrado"
	case errors.Is(err, bothook.ErrConflict), errors.Is(err, bothook.ErrEpoch), errors.Is(err, bothook.ErrDisabled):
		status, detail = http.StatusConflict, "conflito de posse ou comando"
	case errors.Is(err, bothook.ErrInvalid):
		status, detail = http.StatusUnprocessableEntity, "comando inválido"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": detail, "status": status, "detail": detail})
}
