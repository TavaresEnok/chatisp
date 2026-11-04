package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/presence"
	"chatisp/internal/realtime"
)

// connectionPresence is one websocket attempt. Its tab is not the session id,
// so closing it leaves the panel tab and every other socket of that session.
type connectionPresence struct {
	tab uuid.UUID

	mu           sync.Mutex
	accepted     bool
	principal    realtime.Principal
	renewStarted bool
	renewDone    chan struct{}
}

type presenceConnKey struct{}

// PresenceCycle is the presence heartbeat tied to one live panel session.
type PresenceCycle interface {
	Heartbeat(ctx context.Context, p auth.Principal, tab uuid.UUID) (presence.Snapshot, error)
	Close(ctx context.Context, p auth.Principal, tab uuid.UUID) (presence.Snapshot, error)
}

// SessionPresence refreshes presence when the existing realtime session check runs.
// It does not open another websocket. A connection scope in the context uses
// that connection's own tab. Without one, the tab id is the session id, which
// is the panel session opened by GET /api/panel/session.
// While that connection stays open, a server ticker renews the same tab. The
// ticker does not wait for the browser ping. It stops when the connection
// context ends or when the session check fails (expired or revoked).
type SessionPresence struct {
	Next     realtime.Authenticator
	Presence PresenceCycle
	Log      *slog.Logger
	// Interval overrides the server renewal period. Zero, or a value at least
	// as long as the presence TTL, uses one third of that TTL.
	Interval time.Duration
	// renewWait, when set, replaces the timer. A false result stops the loop.
	// Tests use it as the clock. Production leaves it nil.
	renewWait func(ctx context.Context, interval time.Duration) bool
}

// AuthenticateSession checks the session and records one heartbeat.
func (s SessionPresence) AuthenticateSession(ctx context.Context, token string) (realtime.Principal, error) {
	if s.Next == nil {
		return realtime.Principal{}, errors.New("sessão indisponível")
	}
	principal, err := s.Next.AuthenticateSession(ctx, token)
	if err != nil {
		return principal, err
	}
	s.beat(ctx, principal)
	s.armRenewal(ctx, principal, token)
	return principal, nil
}

// renewEvery is the server-side period for one live realtime connection.
// It stays strictly below the presence TTL so a late browser ping cannot
// expire an open socket.
func (s SessionPresence) renewEvery() time.Duration {
	every := s.Interval
	if every <= 0 || every >= presence.HeartbeatTTL {
		every = presence.HeartbeatTTL / 3
	}
	if every <= 0 {
		return time.Second
	}
	return every
}

func (s SessionPresence) armRenewal(ctx context.Context, principal realtime.Principal, token string) {
	if s.Next == nil || ctx == nil || principal.SessionID == uuid.Nil {
		return
	}
	scope, ok := ctx.Value(presenceConnKey{}).(*connectionPresence)
	if !ok || scope == nil || scope.tab == uuid.Nil {
		return
	}
	scope.mu.Lock()
	if scope.renewStarted {
		scope.mu.Unlock()
		return
	}
	scope.renewStarted = true
	scope.renewDone = make(chan struct{})
	scope.mu.Unlock()
	go func() {
		defer close(scope.renewDone)
		s.renew(ctx, principal, token)
	}()
}

func (s SessionPresence) renew(ctx context.Context, principal realtime.Principal, token string) {
	interval := s.renewEvery()
	for {
		if !s.awaitRenew(ctx, interval) || ctx.Err() != nil {
			return
		}
		if err := s.Next.RevalidatePrincipal(ctx, principal, token); err != nil {
			return
		}
		if err := s.beat(ctx, principal); errors.Is(err, presence.ErrForbidden) || ctx.Err() != nil {
			return
		}
	}
}

func (s SessionPresence) awaitRenew(ctx context.Context, interval time.Duration) bool {
	if s.renewWait != nil {
		return s.renewWait(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// RevalidatePrincipal repeats the session check and the heartbeat.
func (s SessionPresence) RevalidatePrincipal(ctx context.Context, principal realtime.Principal, token string) error {
	if s.Next == nil {
		return errors.New("sessão indisponível")
	}
	if err := s.Next.RevalidatePrincipal(ctx, principal, token); err != nil {
		return err
	}
	s.beat(ctx, principal)
	return nil
}

func (s SessionPresence) beat(ctx context.Context, principal realtime.Principal) error {
	if s.Presence == nil || principal.SessionID == uuid.Nil || ctx == nil {
		return nil
	}
	tab := principal.SessionID
	var scope *connectionPresence
	if value, ok := ctx.Value(presenceConnKey{}).(*connectionPresence); ok && value != nil && value.tab != uuid.Nil {
		scope = value
		tab = value.tab
	}
	if _, err := s.Presence.Heartbeat(ctx, authPrincipal(principal), tab); err != nil {
		if s.Log != nil && !errors.Is(err, presence.ErrForbidden) && !errors.Is(err, context.Canceled) {
			s.Log.Warn("heartbeat de presença no ciclo da sessão falhou", "component", "presence")
		}
		return err
	}
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	scope.accepted = true
	scope.principal = principal
	scope.mu.Unlock()
	return nil
}

// SessionPresenceHandler closes only the websocket connection that authenticated.
// A rejected method, origin or session does not heartbeat and does not close another tab.
type SessionPresenceHandler struct {
	Next     http.Handler
	Presence PresenceCycle
	Log      *slog.Logger
}

// ServeHTTP delegates to the existing realtime handler and then closes one accepted connection.
func (h SessionPresenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Next == nil {
		http.Error(w, "serviço indisponível", http.StatusServiceUnavailable)
		return
	}
	var cancel context.CancelFunc
	scope := &connectionPresence{tab: uuid.New()}
	if r != nil {
		var ctx context.Context
		ctx, cancel = context.WithCancel(r.Context())
		r = r.WithContext(context.WithValue(ctx, presenceConnKey{}, scope))
	}
	h.Next.ServeHTTP(w, r)
	if cancel != nil {
		cancel()
	}
	scope.mu.Lock()
	renewDone := scope.renewDone
	scope.mu.Unlock()
	if renewDone != nil {
		<-renewDone
	}
	if h.Presence == nil || r == nil {
		return
	}
	scope.mu.Lock()
	accepted := scope.accepted
	principal := scope.principal
	tab := scope.tab
	scope.mu.Unlock()
	if !accepted || tab == uuid.Nil || principal.SessionID == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if _, err := h.Presence.Close(ctx, authPrincipal(principal), tab); err != nil && h.Log != nil && !errors.Is(err, presence.ErrForbidden) {
		h.Log.Warn("encerramento de presença no ciclo da sessão falhou", "component", "presence")
	}
}

func authPrincipal(principal realtime.Principal) auth.Principal {
	return auth.Principal{
		SessionID:    principal.SessionID,
		UserID:       principal.UserID,
		MembershipID: principal.MembershipID,
		TenantID:     principal.TenantID,
		Role:         principal.Role,
		IsSuperadmin: principal.IsSuperadmin,
	}
}
