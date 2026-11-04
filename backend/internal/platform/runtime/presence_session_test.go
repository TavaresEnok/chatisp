package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/presence"
	"chatisp/internal/realtime"
)

type fakeCycle struct {
	beats  int
	ends   int
	err    error
	tabs   []uuid.UUID
	closed []uuid.UUID
}

func (f *fakeCycle) Heartbeat(_ context.Context, _ auth.Principal, tab uuid.UUID) (presence.Snapshot, error) {
	f.beats++
	f.tabs = append(f.tabs, tab)
	return presence.Snapshot{}, f.err
}

func (f *fakeCycle) Close(_ context.Context, _ auth.Principal, tab uuid.UUID) (presence.Snapshot, error) {
	f.ends++
	f.closed = append(f.closed, tab)
	return presence.Snapshot{}, f.err
}

type fakeAuth struct {
	principal realtime.Principal
	err       error
	checks    int
}

func (f *fakeAuth) AuthenticateSession(context.Context, string) (realtime.Principal, error) {
	f.checks++
	return f.principal, f.err
}

func (f *fakeAuth) RevalidatePrincipal(context.Context, realtime.Principal, string) error {
	f.checks++
	return f.err
}

func TestSessionPresenceFollowsRealtimeChecks(t *testing.T) {
	cycle := &fakeCycle{}
	authn := &fakeAuth{principal: realtime.Principal{SessionID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), TenantID: uuid.New(), Role: "agent"}}
	bridge := SessionPresence{Next: authn, Presence: cycle}
	if _, err := bridge.AuthenticateSession(context.Background(), "token-de-sessao"); err != nil {
		t.Fatal(err)
	}
	if err := bridge.RevalidatePrincipal(context.Background(), authn.principal, "token-de-sessao"); err != nil {
		t.Fatal(err)
	}
	if cycle.beats != 2 || authn.checks != 2 {
		t.Fatalf("beats %d checks %d", cycle.beats, authn.checks)
	}
	if cycle.tabs[0] != authn.principal.SessionID || cycle.tabs[1] != authn.principal.SessionID {
		t.Fatal("heartbeat fora da conexão usa o id da sessão")
	}
	rejected := SessionPresenceHandler{
		Next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "origem não permitida", http.StatusForbidden)
		}),
		Presence: cycle,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/panel/realtime", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "token-de-sessao"})
	rejected.ServeHTTP(httptest.NewRecorder(), req)
	if cycle.ends != 0 || cycle.beats != 2 || authn.checks != 2 {
		t.Fatalf("rejeição ends %d beats %d checks %d", cycle.ends, cycle.beats, authn.checks)
	}
}

func TestSessionPresenceClosesOnlyTheAcceptedConnection(t *testing.T) {
	cycle := &fakeCycle{}
	authn := &fakeAuth{principal: realtime.Principal{SessionID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), TenantID: uuid.New(), Role: "agent"}}
	bridge := SessionPresence{Next: authn, Presence: cycle}
	accept := func(w http.ResponseWriter, r *http.Request) {
		if _, err := bridge.AuthenticateSession(r.Context(), "token-de-sessao"); err != nil {
			t.Fatal(err)
		}
		if err := bridge.RevalidatePrincipal(r.Context(), authn.principal, "token-de-sessao"); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	}
	handler := SessionPresenceHandler{Next: http.HandlerFunc(accept), Presence: cycle}
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/api/panel/realtime", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "token-de-sessao"})
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	if cycle.beats != 4 || cycle.ends != 2 {
		t.Fatalf("beats %d ends %d", cycle.beats, cycle.ends)
	}
	if cycle.closed[0] == cycle.closed[1] || cycle.closed[0] == authn.principal.SessionID || cycle.closed[1] == authn.principal.SessionID {
		t.Fatalf("abas encerradas %v sessão %s", cycle.closed, authn.principal.SessionID)
	}
	if cycle.tabs[0] != cycle.closed[0] || cycle.tabs[2] != cycle.closed[1] || cycle.tabs[1] != cycle.closed[0] {
		t.Fatalf("heartbeats %v encerramentos %v", cycle.tabs, cycle.closed)
	}
	rejected := SessionPresenceHandler{
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
				return
			}
			t.Fatal("método permitido não deveria chegar aqui")
		}),
		Presence: cycle,
	}
	method := httptest.NewRequest(http.MethodPost, "/api/panel/realtime", nil)
	method.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "token-de-sessao"})
	rejected.ServeHTTP(httptest.NewRecorder(), method)
	if cycle.ends != 2 || cycle.beats != 4 {
		t.Fatalf("método recusado beats %d ends %d", cycle.beats, cycle.ends)
	}
}

func TestSessionPresenceKeepsSessionWhenPresenceRefuses(t *testing.T) {
	cycle := &fakeCycle{err: presence.ErrForbidden}
	authn := &fakeAuth{principal: realtime.Principal{SessionID: uuid.New(), Role: "admin"}}
	bridge := SessionPresence{Next: authn, Presence: cycle}
	if _, err := bridge.AuthenticateSession(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	authn.err = errors.New("sessão revogada")
	if err := bridge.RevalidatePrincipal(context.Background(), authn.principal, "token"); err == nil {
		t.Fatal("revalidação revogada deveria falhar")
	}
}

// ttlCycle applies the presence heartbeat rule: a tab is available while
// expires_at = now+HeartbeatTTL is still after the injected clock.
type ttlCycle struct {
	mu    sync.Mutex
	now   time.Time
	exp   time.Time
	tab   uuid.UUID
	beats int
	ends  int
}

func (c *ttlCycle) Heartbeat(_ context.Context, _ auth.Principal, tab uuid.UUID) (presence.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.beats++
	c.tab = tab
	c.exp = c.now.Add(presence.HeartbeatTTL)
	return presence.Snapshot{Connected: true, State: presence.StateAvailable}, nil
}

func (c *ttlCycle) Close(_ context.Context, _ auth.Principal, tab uuid.UUID) (presence.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ends++
	c.tab = tab
	c.exp = time.Time{}
	return presence.Snapshot{State: presence.StateOffline}, nil
}

func (c *ttlCycle) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *ttlCycle) beatCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.beats
}

func (c *ttlCycle) endCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ends
}

func (c *ttlCycle) connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.exp.IsZero() && c.exp.After(c.now)
}

func (c *ttlCycle) clock() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ttlCycle) seenTab() uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tab
}

type gatingAuth struct {
	next   *fakeAuth
	failed chan struct{}
	once   sync.Once
}

func (g *gatingAuth) AuthenticateSession(ctx context.Context, token string) (realtime.Principal, error) {
	return g.next.AuthenticateSession(ctx, token)
}

func (g *gatingAuth) RevalidatePrincipal(ctx context.Context, principal realtime.Principal, token string) error {
	err := g.next.RevalidatePrincipal(ctx, principal, token)
	if err != nil && g.failed != nil {
		g.once.Do(func() { close(g.failed) })
	}
	return err
}

func waitBeats(t *testing.T, cycle *ttlCycle, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for cycle.beatCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("heartbeats %d, esperava pelo menos %d", cycle.beatCount(), n)
		}
		runtime.Gosched()
	}
}

func refuseTick(t *testing.T, ticks chan struct{}) {
	t.Helper()
	select {
	case ticks <- struct{}{}:
		t.Fatal("o ticker renovou sem conexão ativa")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSocketPresenceOutlivesTTLWithoutClientPing(t *testing.T) {
	interval := (SessionPresence{}).renewEvery()
	if interval != presence.HeartbeatTTL/3 || interval >= presence.HeartbeatTTL {
		t.Fatalf("intervalo do servidor %s, TTL %s", interval, presence.HeartbeatTTL)
	}
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cycle := &ttlCycle{now: start}
	sessionID := uuid.New()
	authn := &fakeAuth{principal: realtime.Principal{SessionID: sessionID, UserID: uuid.New(), MembershipID: uuid.New(), TenantID: uuid.New(), Role: "agent"}}
	failed := make(chan struct{})
	ticks := make(chan struct{})
	release := make(chan struct{})
	entered := make(chan struct{})
	bridge := SessionPresence{
		Next:     &gatingAuth{next: authn, failed: failed},
		Presence: cycle,
		renewWait: func(ctx context.Context, every time.Duration) bool {
			if every != presence.HeartbeatTTL/3 {
				t.Errorf("ticker pediu %s", every)
			}
			select {
			case <-ctx.Done():
				return false
			case <-ticks:
				return true
			}
		},
	}
	handler := SessionPresenceHandler{
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := bridge.AuthenticateSession(r.Context(), "token-de-sessao"); err != nil {
				t.Errorf("autenticação %v", err)
				return
			}
			close(entered)
			<-release
			w.WriteHeader(http.StatusSwitchingProtocols)
		}),
		Presence: cycle,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/panel/realtime", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "token-de-sessao"})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("socket não autenticou")
	}
	if cycle.beatCount() != 1 || authn.checks != 1 {
		t.Fatalf("batida inicial %d checagens %d", cycle.beatCount(), authn.checks)
	}
	if cycle.seenTab() == uuid.Nil || cycle.seenTab() == sessionID {
		t.Fatalf("aba da conexão %s sessão %s", cycle.seenTab(), sessionID)
	}
	elapsed := time.Duration(0)
	beats := 1
	for elapsed <= presence.HeartbeatTTL {
		elapsed += interval
		cycle.advance(interval)
		beats++
		ticks <- struct{}{}
		waitBeats(t, cycle, beats)
	}
	if !cycle.clock().After(start.Add(presence.HeartbeatTTL)) {
		t.Fatalf("relógio %s não passou do TTL a partir de %s", cycle.clock(), start)
	}
	if !cycle.connected() {
		t.Fatal("socket aberto sem ping do cliente deixou a presença vencer")
	}
	if authn.checks != beats {
		t.Fatalf("checagens %d heartbeats %d", authn.checks, beats)
	}
	authn.err = errors.New("sessão revogada")
	ticks <- struct{}{}
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("sessão revogada não parou o ticker")
	}
	if cycle.beatCount() != beats {
		t.Fatalf("revogação renovou a presença: %d", cycle.beatCount())
	}
	cycle.advance(presence.HeartbeatTTL)
	if cycle.connected() {
		t.Fatal("depois da revogação a presença continuou disponível além do TTL")
	}
	refuseTick(t, ticks)
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("fechamento do socket não encerrou o ticker")
	}
	if cycle.beatCount() != beats || cycle.endCount() != 1 {
		t.Fatalf("após fechar heartbeats %d encerramentos %d", cycle.beatCount(), cycle.endCount())
	}
	refuseTick(t, ticks)
}

// TestSocketCloseStopsRenewalWhileSessionStaysValid fecha a conexão com a
// sessão ainda válida. A revogação não participa: o cancelamento do contexto
// no fechamento é o que impede os intervalos seguintes de renovar a presença.
func TestSocketCloseStopsRenewalWhileSessionStaysValid(t *testing.T) {
	interval := (SessionPresence{}).renewEvery()
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cycle := &ttlCycle{now: start}
	authn := &fakeAuth{principal: realtime.Principal{SessionID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), TenantID: uuid.New(), Role: "agent"}}
	ticks := make(chan struct{})
	release := make(chan struct{})
	entered := make(chan struct{})
	bridge := SessionPresence{
		Next:     authn,
		Presence: cycle,
		renewWait: func(ctx context.Context, every time.Duration) bool {
			if every != interval {
				t.Errorf("ticker pediu %s", every)
			}
			select {
			case <-ctx.Done():
				return false
			case <-ticks:
				return true
			}
		},
	}
	handler := SessionPresenceHandler{
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := bridge.AuthenticateSession(r.Context(), "token-de-sessao"); err != nil {
				t.Errorf("autenticação %v", err)
				return
			}
			close(entered)
			<-release
			w.WriteHeader(http.StatusSwitchingProtocols)
		}),
		Presence: cycle,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/panel/realtime", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "token-de-sessao"})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("socket não autenticou")
	}
	cycle.advance(interval)
	ticks <- struct{}{}
	waitBeats(t, cycle, 2)
	if authn.err != nil || authn.checks != 2 || cycle.beatCount() != 2 {
		t.Fatalf("antes do fecho err=%v checagens %d heartbeats %d", authn.err, authn.checks, cycle.beatCount())
	}
	beats := cycle.beatCount()
	checks := authn.checks
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("fechamento com sessão válida não encerrou o ticker")
	}
	if authn.err != nil {
		t.Fatal("a sessão foi invalidada; o cenário exige autenticação válida")
	}
	if cycle.endCount() != 1 {
		t.Fatalf("encerramentos %d", cycle.endCount())
	}
	for range 3 {
		cycle.advance(interval)
		refuseTick(t, ticks)
	}
	if cycle.clock().Before(start.Add(interval * 4)) {
		t.Fatalf("relógio %s não avançou vários intervalos desde %s", cycle.clock(), start)
	}
	if cycle.beatCount() != beats || authn.checks != checks {
		t.Fatalf("após fechar heartbeats %d (era %d) checagens %d (era %d)", cycle.beatCount(), beats, authn.checks, checks)
	}
	if cycle.connected() {
		t.Fatal("o fechamento não tirou a presença do ciclo")
	}
}
