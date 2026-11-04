package realtime

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// Handler implements http.Handler for the panel realtime websocket endpoint.
type Handler struct {
	opts   Options
	logger *slog.Logger
}

// NewHandler creates a new realtime WebSocket HTTP handler.
func NewHandler(opts Options) *Handler {
	if opts.DB == nil {
		if provider, ok := opts.Authenticator.(interface{ DB() *db.Store }); ok {
			opts.DB = provider.DB()
		}
		if opts.DB == nil {
			if svc, ok := opts.Attendance.(attendanceService.Service); ok {
				opts.DB = svc.DB
			}
		}
		if opts.DB == nil {
			if provider, ok := opts.InboxResolver.(interface{ DB() *db.Store }); ok {
				opts.DB = provider.DB()
			}
		}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Only the concrete, known service may use the bounded SQL implementation.
	// Decorators and other injected services remain the authorization boundary.
	if svc, ok := opts.Attendance.(attendanceService.Service); ok && svc.DB != nil {
		opts.Attendance = NewBoundedAttendance(svc.DB, svc)
	}

	return &Handler{
		opts:   opts,
		logger: logger,
	}
}

// ServeHTTP handles websocket upgrade, authentication, origin validation, and connection start.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}

	// 1. Reject Bearer tokens: panel websocket only accepts the session cookie contract.
	if r.Header.Get("Authorization") != "" {
		http.Error(w, "autorização por bearer não permitida", http.StatusUnauthorized)
		return
	}

	// 2. Validate Origin header: must be explicitly present and allowed.
	origin := r.Header.Get("Origin")
	if origin == "" || !isOriginAllowed(origin, h.opts.TrustedOrigins) {
		http.Error(w, "origem não permitida", http.StatusForbidden)
		return
	}

	// 3. Validate Cookie contract: only __Host-session and __Host-csrf are allowed.
	var sessionToken string
	for _, c := range r.Cookies() {
		switch c.Name {
		case auth.SessionCookieName: // "__Host-session"
			sessionToken = c.Value
		case auth.CSRFCookieName: // "__Host-csrf"
			// allowed panel CSRF cookie
		default:
			http.Error(w, "cookie fora do contrato do painel", http.StatusForbidden)
			return
		}
	}
	if sessionToken == "" {
		http.Error(w, "sessão necessária", http.StatusUnauthorized)
		return
	}

	// 4. Authenticate session and retrieve operational Principal.
	if h.opts.Authenticator == nil {
		http.Error(w, "autenticação indisponível", http.StatusServiceUnavailable)
		return
	}

	principal, err := h.opts.Authenticator.AuthenticateSession(r.Context(), sessionToken)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			http.Error(w, "acesso proibido", http.StatusForbidden)
		} else {
			http.Error(w, "sessão inválida", http.StatusUnauthorized)
		}
		return
	}

	// 5. Upgrade connection to WebSocket.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.opts.TrustedOrigins,
	})
	if err != nil {
		h.logger.Debug("websocket upgrade rejeitado", "err", err)
		return
	}
	h.openSocket()
	defer h.closeSocket()

	// 6. Parse optional initial query params.
	query := r.URL.Query()
	var initialInbox uuid.UUID
	if rawInbox := query.Get("inbox_id"); rawInbox != "" {
		initialInbox, _ = uuid.Parse(rawInbox)
	}
	var sinceSeq int64
	if rawSeq := query.Get("since_seq"); rawSeq != "" {
		sinceSeq, _ = strconv.ParseInt(rawSeq, 10, 64)
	}
	withSnapshot := query.Get("snapshot") == "true" || (initialInbox != uuid.Nil && sinceSeq == 0)

	// 7. Initialize and run connection until terminated.
	c := newConnection(h, ws, principal, sessionToken, initialInbox, sinceSeq)
	c.start(r.Context(), withSnapshot)
}

func (h *Handler) openSocket() {
	if h == nil || h.opts.Metrics == nil {
		return
	}
	h.opts.Metrics.WebSocketOpened()
}

func (h *Handler) closeSocket() {
	if h == nil || h.opts.Metrics == nil {
		return
	}
	h.opts.Metrics.WebSocketClosed()
}

func isOriginAllowed(origin string, trustedOrigins []string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, trusted := range trustedOrigins {
		trusted = strings.TrimSpace(trusted)
		if trusted == "" {
			continue
		}
		if trusted == "*" {
			return true
		}
		if strings.EqualFold(origin, trusted) {
			return true
		}
		tu, err := url.Parse(trusted)
		if err == nil && tu.Host != "" {
			if strings.EqualFold(u.Scheme, tu.Scheme) && strings.EqualFold(u.Host, tu.Host) {
				return true
			}
		}
		target := u.Host
		if strings.Contains(trusted, "://") {
			target = u.Scheme + "://" + u.Host
		}
		if matched, err := path.Match(strings.ToLower(trusted), strings.ToLower(target)); err == nil && matched {
			return true
		}
	}
	return false
}
