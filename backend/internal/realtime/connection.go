package realtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
)

const (
	defaultBufferSize         = 64
	defaultPollInterval       = 500 * time.Millisecond
	defaultRevalidateInterval = 5 * time.Second
	defaultWriteTimeout       = 5 * time.Second
	defaultDedupCapacity      = 2048
	defaultCacheCapacity      = 512
	maxEventsPerBatch         = 500
	maxSnapshotConversations  = 500
	maxSnapshotMessages       = 1000
)

type deliveryContextKey struct{}

type eventDedupKey struct {
	id      uuid.UUID
	version int64
}

type eventDedup struct {
	seen map[eventDedupKey]struct{}
	keys []eventDedupKey
	cap  int
}

func newEventDedup(cap int) *eventDedup {
	if cap <= 0 {
		cap = defaultDedupCapacity
	}
	return &eventDedup{
		seen: make(map[eventDedupKey]struct{}, cap),
		keys: make([]eventDedupKey, 0, cap),
		cap:  cap,
	}
}

func (d *eventDedup) hasOrAdd(id uuid.UUID, version int64) bool {
	k := eventDedupKey{id: id, version: version}
	if _, ok := d.seen[k]; ok {
		return true
	}
	if len(d.keys) >= d.cap {
		oldest := d.keys[0]
		d.keys = d.keys[1:]
		delete(d.seen, oldest)
	}
	d.seen[k] = struct{}{}
	d.keys = append(d.keys, k)
	return false
}

func (d *eventDedup) reset() {
	d.seen = make(map[eventDedupKey]struct{}, d.cap)
	d.keys = d.keys[:0]
}

type boundedCache struct {
	mu  sync.Mutex
	cap int
	m   map[uuid.UUID]uuid.UUID
	seq []uuid.UUID
}

func newBoundedCache(cap int) *boundedCache {
	if cap <= 0 {
		cap = defaultCacheCapacity
	}
	return &boundedCache{
		cap: cap,
		m:   make(map[uuid.UUID]uuid.UUID, cap),
		seq: make([]uuid.UUID, 0, cap),
	}
}

func (c *boundedCache) get(k uuid.UUID) (uuid.UUID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *boundedCache) put(k, v uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[k]; ok {
		c.m[k] = v
		return true
	}
	retained := true
	if len(c.seq) >= c.cap {
		retained = false
		oldest := c.seq[0]
		c.seq = c.seq[1:]
		delete(c.m, oldest)
	}
	c.m[k] = v
	c.seq = append(c.seq, k)
	return retained
}

func (c *boundedCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *boundedCache) entries() map[uuid.UUID]uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[uuid.UUID]uuid.UUID, len(c.m))
	for id, inbox := range c.m {
		out[id] = inbox
	}
	return out
}

// checkDelivery pins the same policy used by reads before consuming a queued
// payload. Network writes happen only after the transaction has ended.
func (c *conn) checkDelivery(ctx context.Context, msg ServerMessage) error {
	if c.handler.opts.DB == nil {
		return ErrForbidden
	}
	return c.handler.opts.DB.WithTenantTx(ctx, c.principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		p := sectorPrincipal(c.principal.AttendancePrincipal())
		if err := sectors.RequireInbox(ctx, tx, p, msg.InboxID); err != nil {
			return err
		}
		ids := make(map[uuid.UUID]struct{})
		for _, h := range msg.Conversations {
			ids[h.ID] = struct{}{}
		}
		for _, m := range msg.Messages {
			ids[m.ConversationID] = struct{}{}
		}
		for _, e := range msg.Events {
			if e.AggregateType != "conversation" {
				return ErrForbidden
			}
			ids[e.AggregateID] = struct{}{}
		}
		for id := range ids {
			if err := sectors.RequireConversation(ctx, tx, p, msg.InboxID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

type conn struct {
	handler      *Handler
	ws           *websocket.Conn
	principal    Principal
	sessionToken string

	// opMu serializes snapshot retrieval, client message operations (subscribe, replay, snapshot),
	// event fetching, and cursor / dedup updating.
	opMu sync.Mutex

	inboxID   uuid.UUID
	cursor    int64
	dedup     *eventDedup
	convInbox *boundedCache

	outbox    chan ServerMessage
	closed    chan struct{}
	closeOnce sync.Once

	revalidateInterval time.Duration
	pollInterval       time.Duration
	writeTimeout       time.Duration
	logger             *slog.Logger
	cancel             context.CancelFunc
}

func newConnection(h *Handler, ws *websocket.Conn, p Principal, sessionToken string, initialInbox uuid.UUID, sinceSeq int64) *conn {
	bufSize := h.opts.BufferSize
	if bufSize <= 0 {
		bufSize = defaultBufferSize
	}
	pollInt := h.opts.PollInterval
	if pollInt <= 0 {
		pollInt = defaultPollInterval
	}
	revalInt := h.opts.RevalidateInterval
	if revalInt <= 0 {
		revalInt = defaultRevalidateInterval
	}
	writeTimeout := h.opts.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}

	logger := h.logger
	if logger == nil {
		logger = slog.Default()
	}

	return &conn{
		handler:            h,
		ws:                 ws,
		principal:          p,
		sessionToken:       sessionToken,
		inboxID:            initialInbox,
		cursor:             sinceSeq,
		outbox:             make(chan ServerMessage, bufSize),
		closed:             make(chan struct{}),
		revalidateInterval: revalInt,
		pollInterval:       pollInt,
		writeTimeout:       writeTimeout,
		convInbox:          newBoundedCache(defaultCacheCapacity),
		dedup:              newEventDedup(defaultDedupCapacity),
		logger:             logger,
	}
}

func (c *conn) start(parentCtx context.Context, withInitialSnapshot bool) {
	ctx, cancel := context.WithCancel(parentCtx)
	c.cancel = cancel
	defer cancel()

	go c.writePump(ctx)

	// Perform initial snapshot or fetch before activating streamPump
	if c.inboxID != uuid.Nil {
		if withInitialSnapshot {
			c.loadSnapshot(ctx, c.inboxID, uuid.Nil)
		} else if c.cursor > 0 {
			c.fetchAndSendEvents(ctx)
		}
	}

	go c.streamPump(ctx)
	c.readPump(ctx)
}

func (c *conn) writePump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case msg, ok := <-c.outbox:
			if !ok {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			default:
			}

			// Revalidate authorization on consumption before delivering payload
			if msg.Type == "events" || msg.Type == "snapshot" {
				if c.handler.opts.Authenticator != nil {
					revalCtx, revalCancel := context.WithTimeout(context.WithValue(ctx, deliveryContextKey{}, true), c.writeTimeout)
					err := c.handler.opts.Authenticator.RevalidatePrincipal(revalCtx, c.principal, c.sessionToken)
					revalCancel()
					if err != nil {
						c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
						return
					}
				}
				checkCtx, checkCancel := context.WithTimeout(ctx, c.writeTimeout)
				err := c.checkDelivery(checkCtx, msg)
				checkCancel()
				if err != nil {
					c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
					return
				}
			}

			writeCtx, cancel := context.WithTimeout(ctx, c.writeTimeout)
			err := wsjson.Write(writeCtx, c.ws, msg)
			cancel()
			if err != nil {
				c.closeWithCode(websocket.StatusPolicyViolation, "slow_client")
				return
			}
		}
	}
}

func (c *conn) readPump(ctx context.Context) {
	defer c.closeWithCode(websocket.StatusNormalClosure, "client_disconnected")
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		default:
		}

		var msg ClientMessage
		err := wsjson.Read(ctx, c.ws, &msg)
		if err != nil {
			return
		}
		c.handleClientMessage(ctx, msg)
	}
}

func (c *conn) handleClientMessage(ctx context.Context, msg ClientMessage) {
	if c.handler.opts.Authenticator != nil {
		if err := c.handler.opts.Authenticator.RevalidatePrincipal(ctx, c.principal, c.sessionToken); err != nil {
			c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
			return
		}
	}

	switch msg.Operation() {
	case "subscribe":
		if msg.InboxID == uuid.Nil {
			_ = c.send(ServerMessage{Type: "error", Code: "invalid_inbox", Message: "inbox_id obrigatório"})
			return
		}

		c.opMu.Lock()
		defer c.opMu.Unlock()

		select {
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		default:
		}

		if msg.WithSnapshot || (msg.SinceSeq == 0 && msg.After == 0) {
			snap, err := c.handler.opts.Attendance.Snapshot(ctx, c.principal.AttendancePrincipal(), store.SnapshotInput{
				TenantID: c.principal.TenantID,
				InboxID:  msg.InboxID,
			})
			if err != nil {
				if errors.Is(err, ErrLoadLimitExceeded) {
					c.logger.Warn("snapshot excede limites de carga", "cursor", snap.NextCursor)
					_ = c.send(ServerMessage{
						Type:       "resync",
						InboxID:    msg.InboxID,
						Reason:     "load_limit_exceeded",
						NextCursor: snap.NextCursor,
					})
					c.inboxID = msg.InboxID
					c.cursor = snap.NextCursor
					c.dedup.reset()
					return
				}
				if errors.Is(err, attendanceService.ErrTenantInactive) || errors.Is(err, attendanceService.ErrForbidden) {
					c.closeWithCode(websocket.StatusPolicyViolation, "authorization_lost")
					return
				}
				if errors.Is(err, attendanceService.ErrInboxForbidden) {
					_ = c.send(ServerMessage{Type: "error", Code: "inbox_forbidden", Message: "caixa não permitida"})
					return
				}
				_ = c.send(ServerMessage{Type: "error", Code: "snapshot_failed", Message: err.Error()})
				return
			}

			if len(snap.Conversations) > maxSnapshotConversations || len(snap.Messages) > maxSnapshotMessages {
				c.logger.Warn("snapshot excede limites de carga", "conversations", len(snap.Conversations), "messages", len(snap.Messages))
				_ = c.send(ServerMessage{
					Type:       "resync",
					InboxID:    msg.InboxID,
					Reason:     "load_limit_exceeded",
					NextCursor: snap.NextCursor,
				})
				c.inboxID = msg.InboxID
				c.cursor = snap.NextCursor
				c.dedup.reset()
				return
			}

			c.inboxID = msg.InboxID
			c.cursor = snap.NextCursor
			c.dedup.reset()
			for _, conv := range snap.Conversations {
				if !c.convInbox.put(conv.ID, msg.InboxID) {
					c.closeWithCode(websocket.StatusPolicyViolation, "resync_required")
					return
				}
			}

			_ = c.send(ServerMessage{
				Type:          "snapshot",
				InboxID:       msg.InboxID,
				NextCursor:    snap.NextCursor,
				Conversations: snap.Conversations,
				Messages:      snap.Messages,
			})
		} else {
			after := msg.After
			if after <= 0 {
				after = msg.SinceSeq
			}
			c.inboxID = msg.InboxID
			c.cursor = after
			c.dedup.reset()
			c.fetchAndSendEventsLocked(ctx)
		}

	case "snapshot":
		c.opMu.Lock()
		inboxID := msg.InboxID
		if inboxID == uuid.Nil {
			inboxID = c.inboxID
		}
		c.opMu.Unlock()

		if inboxID == uuid.Nil {
			_ = c.send(ServerMessage{Type: "error", Code: "invalid_inbox", Message: "inbox_id obrigatório"})
			return
		}
		c.loadSnapshot(ctx, inboxID, msg.ConversationID)

	case "replay":
		c.opMu.Lock()
		defer c.opMu.Unlock()

		select {
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		default:
		}

		after := msg.After
		if after <= 0 && msg.SinceSeq > 0 {
			after = msg.SinceSeq
		}
		if msg.InboxID != uuid.Nil {
			c.inboxID = msg.InboxID
		}
		c.cursor = after
		c.dedup.reset()
		c.fetchAndSendEventsLocked(ctx)

	case "ping":
		_ = c.send(ServerMessage{Type: "pong"})

	default:
		_ = c.send(ServerMessage{Type: "error", Code: "unknown_action", Message: "ação não reconhecida"})
	}
}

func (c *conn) loadSnapshot(ctx context.Context, inboxID, conversationID uuid.UUID) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	select {
	case <-c.closed:
		return
	case <-ctx.Done():
		return
	default:
	}

	snap, err := c.handler.opts.Attendance.Snapshot(ctx, c.principal.AttendancePrincipal(), store.SnapshotInput{
		TenantID:       c.principal.TenantID,
		InboxID:        inboxID,
		ConversationID: conversationID,
	})
	if err != nil {
		if errors.Is(err, ErrLoadLimitExceeded) {
			c.logger.Warn("snapshot excede limites de carga", "cursor", snap.NextCursor)
			_ = c.send(ServerMessage{
				Type:       "resync",
				InboxID:    inboxID,
				Reason:     "load_limit_exceeded",
				NextCursor: snap.NextCursor,
			})
			c.inboxID = inboxID
			c.cursor = snap.NextCursor
			c.dedup.reset()
			return
		}
		if errors.Is(err, attendanceService.ErrTenantInactive) || errors.Is(err, attendanceService.ErrForbidden) {
			c.closeWithCode(websocket.StatusPolicyViolation, "authorization_lost")
			return
		}
		if errors.Is(err, attendanceService.ErrInboxForbidden) {
			_ = c.send(ServerMessage{Type: "error", Code: "inbox_forbidden", Message: "caixa não permitida"})
			return
		}
		_ = c.send(ServerMessage{Type: "error", Code: "snapshot_failed", Message: err.Error()})
		return
	}

	if len(snap.Conversations) > maxSnapshotConversations || len(snap.Messages) > maxSnapshotMessages {
		c.logger.Warn("snapshot excede limites de carga", "conversations", len(snap.Conversations), "messages", len(snap.Messages))
		_ = c.send(ServerMessage{
			Type:       "resync",
			InboxID:    inboxID,
			Reason:     "load_limit_exceeded",
			NextCursor: snap.NextCursor,
		})
		c.inboxID = inboxID
		c.cursor = snap.NextCursor
		c.dedup.reset()
		return
	}

	c.inboxID = inboxID
	c.cursor = snap.NextCursor
	c.dedup.reset()
	for _, conv := range snap.Conversations {
		if !c.convInbox.put(conv.ID, inboxID) {
			c.closeWithCode(websocket.StatusPolicyViolation, "resync_required")
			return
		}
	}

	_ = c.send(ServerMessage{
		Type:          "snapshot",
		InboxID:       inboxID,
		NextCursor:    snap.NextCursor,
		Conversations: snap.Conversations,
		Messages:      snap.Messages,
	})
}

func (c *conn) streamPump(ctx context.Context) {
	var notifyCh <-chan struct{}
	var unsub func()
	if c.handler.opts.Notifier != nil {
		notifyCh, unsub = c.handler.opts.Notifier.Subscribe(c.principal.TenantID)
		defer unsub()
	}

	pollTicker := time.NewTicker(c.pollInterval)
	defer pollTicker.Stop()

	revalTicker := time.NewTicker(c.revalidateInterval)
	defer revalTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case <-revalTicker.C:
			if c.handler.opts.Authenticator != nil {
				if err := c.handler.opts.Authenticator.RevalidatePrincipal(ctx, c.principal, c.sessionToken); err != nil {
					c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
					return
				}
			}
			for id, inbox := range c.convInbox.entries() {
				if err := c.checkDelivery(ctx, ServerMessage{InboxID: inbox, Conversations: []store.Head{{ID: id}}}); err != nil {
					c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
					return
				}
			}
		case <-notifyCh:
			c.fetchAndSendEvents(ctx)
		case <-pollTicker.C:
			c.fetchAndSendEvents(ctx)
		}
	}
}

func (c *conn) fetchAndSendEvents(ctx context.Context) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.fetchAndSendEventsLocked(ctx)
}

func (c *conn) fetchAndSendEventsLocked(ctx context.Context) {
	select {
	case <-c.closed:
		return
	case <-ctx.Done():
		return
	default:
	}

	if c.handler.opts.Authenticator != nil {
		if err := c.handler.opts.Authenticator.RevalidatePrincipal(ctx, c.principal, c.sessionToken); err != nil {
			c.closeWithCode(websocket.StatusPolicyViolation, "authorization_revoked")
			return
		}
	}

	inboxID := c.inboxID
	currentCursor := c.cursor
	if inboxID == uuid.Nil {
		return
	}

	page, err := c.handler.opts.Attendance.Events(ctx, c.principal.AttendancePrincipal(), inboxID, currentCursor)
	if err != nil {
		if errors.Is(err, store.ErrResync) {
			_ = c.send(ServerMessage{
				Type:       "resync",
				InboxID:    inboxID,
				Reason:     "retention_gap",
				NextCursor: currentCursor,
			})
			return
		}
		if errors.Is(err, ErrLoadLimitExceeded) {
			_ = c.send(ServerMessage{
				Type:       "resync",
				InboxID:    inboxID,
				Reason:     "load_limit_exceeded",
				NextCursor: page.NextCursor,
			})
			c.cursor = page.NextCursor
			return
		}
		if errors.Is(err, attendanceService.ErrForbidden) || errors.Is(err, attendanceService.ErrTenantInactive) || errors.Is(err, attendanceService.ErrInboxForbidden) {
			c.closeWithCode(websocket.StatusPolicyViolation, "authorization_lost")
			return
		}
		c.logger.Debug("falha ao consultar eventos realtime", "err", err)
		return
	}

	if page.NextCursor <= currentCursor {
		return
	}

	if len(page.Events) > maxEventsPerBatch {
		c.logger.Warn("carga de eventos excede limite máximo por lote", "count", len(page.Events), "max", maxEventsPerBatch)
		_ = c.send(ServerMessage{
			Type:       "resync",
			InboxID:    inboxID,
			Reason:     "load_limit_exceeded",
			NextCursor: page.NextCursor,
		})
		c.cursor = page.NextCursor
		return
	}

	var lastExaminedSeq int64 = currentCursor
	var lastSentCursor int64 = currentCursor
	var hasTransientError bool

	for _, ev := range page.Events {
		belongs, err := c.isEventForInbox(ctx, ev, inboxID)
		if err != nil {
			hasTransientError = true
			c.logger.Warn("erro transitório ao resolver caixa de evento", "err", err, "conv_id", ev.AggregateID)
			break
		}

		lastExaminedSeq = ev.Seq

		if !belongs {
			// Invisible event: do NOT record in dedup, keeping state clean for other boxes/replays.
			continue
		}

		// Event destined for this inbox: record in dedup.
		if c.dedup.hasOrAdd(ev.ID, ev.Version) {
			continue
		}

		lastSentCursor = ev.Seq
		if err := c.send(ServerMessage{
			Type:       "events",
			InboxID:    inboxID,
			NextCursor: ev.Seq,
			Events:     []store.Event{ev},
		}); err != nil {
			return
		}
	}

	newCursor := page.NextCursor
	if hasTransientError {
		newCursor = lastExaminedSeq
	}
	c.cursor = newCursor

	// If there are invisible events advancing beyond last sent cursor, send an advance message
	if lastSentCursor < newCursor {
		_ = c.send(ServerMessage{
			Type:       "events",
			InboxID:    inboxID,
			NextCursor: newCursor,
			Events:     nil,
		})
	}
}

func (c *conn) isEventForInbox(ctx context.Context, ev store.Event, targetInbox uuid.UUID) (bool, error) {
	if ev.AggregateType == "conversation" && ev.AggregateID != uuid.Nil {
		convID := ev.AggregateID
		if cachedInbox, ok := c.convInbox.get(convID); ok {
			return cachedInbox == targetInbox, nil
		}
		if c.handler.opts.InboxResolver != nil {
			resolved, err := c.handler.opts.InboxResolver.ResolveConversationInbox(ctx, c.principal.TenantID, convID)
			if err != nil {
				if errors.Is(err, ErrInboxForbidden) || errors.Is(err, store.ErrNotFound) {
					// Definitive negation: conversation does not belong to accessible inbox
					return false, nil
				}
				// Operational / transient database error
				return false, err
			}
			if resolved == uuid.Nil {
				return false, nil
			}
			if !c.convInbox.put(convID, resolved) {
				c.closeWithCode(websocket.StatusPolicyViolation, "resync_required")
				return false, ErrConnectionClosed
			}
			return resolved == targetInbox, nil
		}
		// When InboxResolver is nil and conversation is not in cache, we cannot determine the inbox.
		// Deny delivery to prevent events from other inboxes leaking across subscriptions.
		return false, nil
	}
	return false, nil
}

func (c *conn) send(msg ServerMessage) error {
	select {
	case <-c.closed:
		return ErrConnectionClosed
	default:
	}

	timer := time.NewTimer(c.writeTimeout)
	defer timer.Stop()
	select {
	case <-c.closed:
		return ErrConnectionClosed
	case c.outbox <- msg:
		return nil
	case <-timer.C:
		c.closeWithCode(websocket.StatusPolicyViolation, "slow_client")
		return ErrSlowClient
	}
}

func (c *conn) closeWithCode(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.drainOutbox()
		_ = c.ws.Close(code, reason)
		if c.cancel != nil {
			c.cancel()
		}
	})
}

func (c *conn) drainOutbox() {
	for {
		select {
		case <-c.outbox:
		default:
			return
		}
	}
}
