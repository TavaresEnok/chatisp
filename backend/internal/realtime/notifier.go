package realtime

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// RealtimeChannel is the PostgreSQL LISTEN/NOTIFY channel name.
	RealtimeChannel = "chatisp_realtime_events"
)

// PGNotifier listens on PostgreSQL for lightweight tenant update signals.
type PGNotifier struct {
	pool    *pgxpool.Pool
	logger  *slog.Logger
	mu      sync.RWMutex
	subs    map[uuid.UUID]map[chan struct{}]struct{}
	allSubs map[chan struct{}]struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewPGNotifier starts a dedicated PostgreSQL listener.
func NewPGNotifier(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) *PGNotifier {
	ctx, cancel := context.WithCancel(ctx)
	n := &PGNotifier{
		pool:    pool,
		logger:  logger,
		subs:    make(map[uuid.UUID]map[chan struct{}]struct{}),
		allSubs: make(map[chan struct{}]struct{}),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	if pool != nil {
		go n.run(ctx)
	} else {
		close(n.done)
	}
	return n
}

// Subscribe returns a channel signaled when a tenant event occurs, and an unsubscriber.
func (n *PGNotifier) Subscribe(tenantID uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	defer n.mu.Unlock()

	if tenantID == uuid.Nil {
		n.allSubs[ch] = struct{}{}
		return ch, func() {
			n.mu.Lock()
			delete(n.allSubs, ch)
			n.mu.Unlock()
		}
	}

	set, ok := n.subs[tenantID]
	if !ok {
		set = make(map[chan struct{}]struct{})
		n.subs[tenantID] = set
	}
	set[ch] = struct{}{}

	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if s, exists := n.subs[tenantID]; exists {
			delete(s, ch)
			if len(s) == 0 {
				delete(n.subs, tenantID)
			}
		}
	}
}

// Notify triggers a NOTIFY on PostgreSQL with the minimal tenant ID.
func (n *PGNotifier) Notify(ctx context.Context, tenantID uuid.UUID) error {
	n.broadcast(tenantID)
	if n.pool == nil {
		return nil
	}
	return EmitNotify(ctx, n.pool, tenantID)
}

// Close shuts down the background listener.
func (n *PGNotifier) Close() {
	if n.cancel != nil {
		n.cancel()
	}
	<-n.done
}

func (n *PGNotifier) run(ctx context.Context) {
	defer close(n.done)
	for ctx.Err() == nil {
		conn, err := n.pool.Acquire(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		_, err = conn.Exec(ctx, "LISTEN "+RealtimeChannel)
		if err != nil {
			conn.Release()
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		for ctx.Err() == nil {
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				conn.Release()
				break
			}
			payload := strings.TrimSpace(notification.Payload)
			var tid uuid.UUID
			if parsed, err := uuid.Parse(payload); err == nil {
				tid = parsed
			}
			n.broadcast(tid)
		}
	}
}

func (n *PGNotifier) broadcast(tenantID uuid.UUID) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for ch := range n.allSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	if tenantID != uuid.Nil {
		if set, ok := n.subs[tenantID]; ok {
			for ch := range set {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	} else {
		for _, set := range n.subs {
			for ch := range set {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}
}

// EmitNotify sends a NOTIFY on PostgreSQL carrying the minimal tenant ID.
func EmitNotify(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	if pool == nil {
		return nil
	}
	payload := ""
	if tenantID != uuid.Nil {
		payload = tenantID.String()
	}
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", RealtimeChannel, payload)
	return err
}

// LocalNotifier is an in-memory fallback for Notifier.
type LocalNotifier struct {
	mu      sync.RWMutex
	subs    map[uuid.UUID]map[chan struct{}]struct{}
	allSubs map[chan struct{}]struct{}
}

// NewLocalNotifier returns an in-memory Notifier without PG LISTEN.
func NewLocalNotifier() *LocalNotifier {
	return &LocalNotifier{
		subs:    make(map[uuid.UUID]map[chan struct{}]struct{}),
		allSubs: make(map[chan struct{}]struct{}),
	}
}

// Subscribe registers a listener for tenant events.
func (l *LocalNotifier) Subscribe(tenantID uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	l.mu.Lock()
	defer l.mu.Unlock()

	if tenantID == uuid.Nil {
		l.allSubs[ch] = struct{}{}
		return ch, func() {
			l.mu.Lock()
			delete(l.allSubs, ch)
			l.mu.Unlock()
		}
	}

	set, ok := l.subs[tenantID]
	if !ok {
		set = make(map[chan struct{}]struct{})
		l.subs[tenantID] = set
	}
	set[ch] = struct{}{}

	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if s, exists := l.subs[tenantID]; exists {
			delete(s, ch)
			if len(s) == 0 {
				delete(l.subs, tenantID)
			}
		}
	}
}

// Notify wakes up registered subscribers for the tenant.
func (l *LocalNotifier) Notify(_ context.Context, tenantID uuid.UUID) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for ch := range l.allSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	if tenantID != uuid.Nil {
		if set, ok := l.subs[tenantID]; ok {
			for ch := range set {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}
	return nil
}
