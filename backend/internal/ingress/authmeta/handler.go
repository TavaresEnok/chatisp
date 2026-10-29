package authmeta

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const (
	// DefaultMaxBodyBytes is the raw POST limit when the binding does not set one.
	DefaultMaxBodyBytes int64 = 1 << 20
	maxBodyCap          int64 = 32 << 20

	queryMode      = "hub.mode"
	queryToken     = "hub.verify_token"
	queryChallenge = "hub.challenge"
)

// App is the trusted Meta binding for one endpoint.
// Values come from configuration. They are never read from the payload.
type App struct {
	ID                string
	Secret            string
	VerificationToken string
	// MaxBodyBytes limits the raw POST body. Zero selects DefaultMaxBodyBytes.
	MaxBodyBytes int64
}

// Envelope is the authenticated delivery before tenant routing.
// Raw is the original body, which may contain several phone numbers.
// AppID is the trusted binding. There is no tenant field.
type Envelope struct {
	AppID string
	Raw   []byte
}

// Persister stores one authenticated envelope.
// HTTP 200 is returned only after Persist returns nil and the request
// context is still active. This package does not implement storage.
type Persister interface {
	Persist(ctx context.Context, envelope Envelope) error
}

// PersistFunc adapts a callback to Persister.
type PersistFunc func(ctx context.Context, envelope Envelope) error

// Persist calls the callback. A nil callback fails closed.
func (f PersistFunc) Persist(ctx context.Context, envelope Envelope) error {
	if f == nil {
		return errors.New("persistência ausente")
	}
	return f(ctx, envelope)
}

// Handler is a library HTTP handler for one trusted Meta binding.
// It is not registered on the process router.
type Handler struct {
	app     App
	persist Persister
	logger  *slog.Logger
}

// New returns a handler for app. logger may be nil.
// persist is required to acknowledge a POST. A nil persist never returns 200.
func New(app App, persist Persister, logger *slog.Logger) *Handler {
	return &Handler{app: app, persist: persist, logger: logger}
}

// ServeHTTP verifies a subscription challenge or an event delivery.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		writeText(w, http.StatusInternalServerError, "entrega não confirmada")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.verify(w, r)
	case http.MethodPost:
		h.receive(w, r)
	default:
		h.reject(w, http.StatusMethodNotAllowed, "pedido inválido", "method_not_allowed")
	}
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	if h.rejectCanceled(w, r) {
		return
	}
	if h.app.VerificationToken == "" || tokenEqual([]byte(h.app.Secret), []byte(h.app.VerificationToken)) {
		h.reject(w, http.StatusUnauthorized, "não autorizado", "verify_rejected")
		return
	}
	query := r.URL.Query()
	echo, err := VerifyChallenge(
		[]byte(h.app.VerificationToken),
		query.Get(queryMode),
		query.Get(queryToken),
		query.Get(queryChallenge),
	)
	if err != nil {
		h.reject(w, http.StatusUnauthorized, "não autorizado", "verify_rejected")
		return
	}
	h.logResult("challenge_ok")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, echo)
}

func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	if h.rejectCanceled(w, r) {
		return
	}
	appID := strings.TrimSpace(h.app.ID)
	if appID == "" || h.app.Secret == "" {
		h.reject(w, http.StatusInternalServerError, "entrega não confirmada", "binding_invalid")
		return
	}
	max, err := h.bodyLimit()
	if err != nil {
		h.reject(w, http.StatusRequestEntityTooLarge, "corpo acima do limite", "body_too_large")
		return
	}
	raw, err := readLimited(r.Body, max)
	if err != nil {
		if errors.Is(err, ErrBodyTooLarge) {
			h.reject(w, http.StatusRequestEntityTooLarge, "corpo acima do limite", "body_too_large")
			return
		}
		if h.rejectCanceled(w, r) {
			return
		}
		h.reject(w, http.StatusBadRequest, "pedido inválido", "body_unreadable")
		return
	}
	if h.rejectCanceled(w, r) {
		return
	}
	if err := VerifySignature([]byte(h.app.Secret), raw, r.Header.Get(SignatureHeader)); err != nil {
		h.reject(w, http.StatusUnauthorized, "não autorizado", signatureResult(err))
		return
	}
	if h.rejectCanceled(w, r) {
		return
	}
	if h.persist == nil {
		h.reject(w, http.StatusInternalServerError, "entrega não confirmada", "persist_missing")
		return
	}
	err = h.persist.Persist(r.Context(), Envelope{AppID: appID, Raw: raw})
	switch {
	case r.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		h.reject(w, http.StatusBadRequest, "operação cancelada", "canceled")
		return
	case err != nil:
		h.reject(w, http.StatusInternalServerError, "entrega não confirmada", "persist_failed")
		return
	}
	h.logResult("persisted")
	writeText(w, http.StatusOK, "")
}

func (h *Handler) bodyLimit() (int64, error) {
	max := h.app.MaxBodyBytes
	if max == 0 {
		max = DefaultMaxBodyBytes
	}
	if max < 1 || max > maxBodyCap {
		return 0, ErrBodyTooLarge
	}
	return max, nil
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	if r == nil {
		return []byte{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, ErrBodyTooLarge
	}
	return body, nil
}

func (h *Handler) rejectCanceled(w http.ResponseWriter, r *http.Request) bool {
	if r.Context().Err() == nil {
		return false
	}
	h.reject(w, http.StatusBadRequest, "operação cancelada", "canceled")
	return true
}

func (h *Handler) reject(w http.ResponseWriter, status int, public, result string) {
	h.logResult(result)
	writeText(w, status, public)
}

func (h *Handler) logResult(result string) {
	if h == nil || h.logger == nil {
		return
	}
	h.logger.Info("meta webhook", "result", result, "app_id", strings.TrimSpace(h.app.ID))
}

func signatureResult(err error) string {
	switch {
	case errors.Is(err, ErrSignatureMissing):
		return "signature_missing"
	case errors.Is(err, ErrSignatureMalformed):
		return "signature_malformed"
	default:
		return "signature_invalid"
	}
}

func writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if msg == "" {
		return
	}
	_, _ = io.WriteString(w, msg)
}
