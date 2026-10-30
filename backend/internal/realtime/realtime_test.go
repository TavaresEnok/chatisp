package realtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
)

const testTrustedOrigin = "https://painel.chatisp.test"

func newTestHandler(pollInterval, revalInterval time.Duration, bufSize int) (*Handler, *AuthAdapter) {
	adapter := NewAuthAdapter(testAuth, testStore)
	h := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      adapter,
		Attendance:         testAttendance,
		InboxResolver:      adapter,
		Notifier:           testNotifier,
		PollInterval:       pollInterval,
		RevalidateInterval: revalInterval,
		BufferSize:         bufSize,
		WriteTimeout:       2 * time.Second,
	})
	return h, adapter
}

func sendTestMessage(t *testing.T, w world, inboxID uuid.UUID, body string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	connectorID := w.connector
	identityID := w.identity
	if inboxID == w.inbox2 {
		connectorID = w.connector2
		identityID = w.identity2
	}
	res, err := testAttendance.ReceiveText(ctx, principal(w), attendanceService.ReceiveInput{
		InboxID:           inboxID,
		ContactID:         w.contact,
		IdentityID:        identityID,
		ConnectorID:       connectorID,
		ExternalMessageID: "ext-" + uuid.NewString(),
		Body:              body,
		Period:            "202610",
		TrustedAt:         now,
		OpenedAt:          now,
	})
	if err != nil {
		t.Fatalf("falha ao enviar mensagem de teste: %s", err)
	}
	return res.MessageID, res.Conversation.ID
}

func sendTestMessageCustom(t *testing.T, w world, inboxID, contactID, identityID, connectorID uuid.UUID, body string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	res, err := testAttendance.ReceiveText(ctx, principal(w), attendanceService.ReceiveInput{
		InboxID:           inboxID,
		ContactID:         contactID,
		IdentityID:        identityID,
		ConnectorID:       connectorID,
		ExternalMessageID: "ext-" + uuid.NewString(),
		Body:              body,
		Period:            "202610",
		TrustedAt:         now,
		OpenedAt:          now,
	})
	if err != nil {
		t.Fatalf("falha ao enviar mensagem de teste customizada: %s", err)
	}
	return res.MessageID, res.Conversation.ID
}

func TestOriginValidation(t *testing.T) {
	w := seedWorld(t, "agent")
	h, _ := newTestHandler(50*time.Millisecond, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Missing Origin header
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err == nil {
		t.Fatal("esperava erro sem Origin, mas conectou")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("esperava 403 Forbidden para Origin ausente, obteve %v", resp)
	}

	// 2. Untrusted / evil Origin
	_, resp, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"https://evil.example.com"},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err == nil {
		t.Fatal("esperava erro com Origin hostil, mas conectou")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("esperava 403 Forbidden para Origin hostil, obteve %v", resp)
	}

	// 3. Trusted Origin
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("esperava conexão com Origin confiável: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("esperava 101 Switching Protocols, obteve %d", resp.StatusCode)
	}
}

func TestHandshakeSessionAndCookieContract(t *testing.T) {
	w := seedWorld(t, "agent")
	h, _ := newTestHandler(50*time.Millisecond, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Missing session cookie
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
		},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperava 401 sem cookie de sessão, obteve %v (err: %v)", resp, err)
	}

	// 2. Bearer authorization header present -> rejected
	_, resp, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":        []string{testTrustedOrigin},
			"Cookie":        []string{auth.SessionCookieName + "=" + w.sessionToken},
			"Authorization": []string{"Bearer some-random-bearer-token"},
		},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperava 401 com Authorization Bearer, obteve %v (err: %v)", resp, err)
	}

	// 3. Cookie outside panel contract (e.g. tracking cookie) -> rejected
	_, resp, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{
				auth.SessionCookieName + "=" + w.sessionToken,
				"tracking_id=malicious-cookie",
			},
		},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("esperava 403 com cookie fora do contrato, obteve %v (err: %v)", resp, err)
	}

	// 4. Invalid session token
	_, resp, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=token-invalido-inexistente"},
		},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperava 401 com token inválido, obteve %v (err: %v)", resp, err)
	}
}

func TestTenantAndInboxIsolation(t *testing.T) {
	wA := seedWorld(t, "agent")
	wB := seedWorld(t, "agent")

	h, _ := newTestHandler(20*time.Millisecond, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. User A tries to subscribe to User B's inbox
	connA, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + wA.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar A: %s", err)
	}
	defer connA.Close(websocket.StatusNormalClosure, "done")

	err = wsjson.Write(ctx, connA, ClientMessage{
		Action:  "subscribe",
		InboxID: wB.inbox,
	})
	if err != nil {
		t.Fatalf("falha ao enviar subscrição proibida: %s", err)
	}

	var errMsg ServerMessage
	if err := wsjson.Read(ctx, connA, &errMsg); err != nil {
		t.Fatalf("falha ao ler resposta da subscrição: %s", err)
	}
	if errMsg.Type != "error" || errMsg.Code != "inbox_forbidden" {
		t.Fatalf("esperava erro inbox_forbidden ao assinar caixa de outro tenant, obteve: %#v", errMsg)
	}

	// 2. User A subscribes to Box A, User B subscribes to Box B
	err = wsjson.Write(ctx, connA, ClientMessage{
		Action:       "subscribe",
		InboxID:      wA.inbox,
		WithSnapshot: true,
	})
	if err != nil {
		t.Fatalf("falha ao assinar Box A: %s", err)
	}
	var snapA ServerMessage
	if err := wsjson.Read(ctx, connA, &snapA); err != nil || snapA.Type != "snapshot" {
		t.Fatalf("esperava snapshot de Box A, obteve %#v (err: %v)", snapA, err)
	}

	connB, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + wB.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar B: %s", err)
	}
	defer connB.Close(websocket.StatusNormalClosure, "done")

	err = wsjson.Write(ctx, connB, ClientMessage{
		Action:       "subscribe",
		InboxID:      wB.inbox,
		WithSnapshot: true,
	})
	if err != nil {
		t.Fatalf("falha ao assinar Box B: %s", err)
	}
	var snapB ServerMessage
	if err := wsjson.Read(ctx, connB, &snapB); err != nil || snapB.Type != "snapshot" {
		t.Fatalf("esperava snapshot de Box B, obteve %#v (err: %v)", snapB, err)
	}

	// 3. Emit message in Tenant B only
	sendTestMessage(t, wB, wB.inbox, "Mensagem confidencial de B")

	// User B receives the event
	var evB ServerMessage
	if err := wsjson.Read(ctx, connB, &evB); err != nil || evB.Type != "events" {
		t.Fatalf("esperava evento em B, obteve %#v (err: %v)", evB, err)
	}
	if len(evB.Events) == 0 {
		t.Fatal("esperava pelo menos 1 evento em B")
	}

	// User A must NOT receive any message
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer timeoutCancel()
	var leaked ServerMessage
	if err := wsjson.Read(timeoutCtx, connA, &leaked); err == nil {
		t.Fatalf("vazamento de evento entre tenants detectado: %#v", leaked)
	}
}

type gatingAttendance struct {
	AttendanceService
	mu              sync.Mutex
	onAfterSnapshot func(snap store.Snapshot)
}

func (g *gatingAttendance) Unwrap() AttendanceService {
	return g.AttendanceService
}

func (g *gatingAttendance) Snapshot(ctx context.Context, p attendanceService.Principal, in store.SnapshotInput) (store.Snapshot, error) {
	snap, err := g.AttendanceService.Snapshot(ctx, p, in)
	if err == nil {
		g.mu.Lock()
		hook := g.onAfterSnapshot
		g.mu.Unlock()
		if hook != nil {
			hook(snap)
		}
	}
	return snap, err
}

func TestSnapshotAndConcurrentEvents(t *testing.T) {
	w := seedWorld(t, "agent")
	adapter := NewAuthAdapter(testAuth, testStore)
	bounded := NewBoundedAttendance(testStore, testAttendance)
	attendanceGating := &gatingAttendance{AttendanceService: bounded}

	h := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      adapter,
		Attendance:         attendanceGating,
		InboxResolver:      adapter,
		Notifier:           testNotifier,
		PollInterval:       20 * time.Millisecond,
		RevalidateInterval: 1 * time.Second,
		BufferSize:         16,
		WriteTimeout:       2 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Commit Change 1 for Contact 1 before snapshot
	msg1ID, conv1ID := sendTestMessageCustom(t, w, w.inbox, w.contact, w.identity, w.connector, "Mensagem concorrente 1")

	// 2. Barrier at the snapshot boundary: commit Change 2 for Contact 2 (conv2ID != conv1ID)
	// immediately after snapshot transaction establishes its cursor boundary.
	var msg2ID, conv2ID uuid.UUID
	snapshotHookCalled := false

	attendanceGating.mu.Lock()
	attendanceGating.onAfterSnapshot = func(snap store.Snapshot) {
		attendanceGating.mu.Lock()
		already := snapshotHookCalled
		snapshotHookCalled = true
		attendanceGating.mu.Unlock()
		if !already {
			// Commit Msg 2 for Contact 2 right at the snapshot boundary
			msg2ID, conv2ID = sendTestMessageCustom(t, w, w.inbox, w.contact2, w.identity1B, w.connector, "Mensagem concorrente 2")
		}
	}
	attendanceGating.mu.Unlock()

	// Connect socket and trigger initial snapshot
	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}

	// Read initial snapshot
	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil {
		t.Fatalf("falha ao ler snapshot inicial: %s", err)
	}
	if snap.Type != "snapshot" || snap.InboxID != w.inbox {
		t.Fatalf("resposta inválida de snapshot: %#v", snap)
	}

	// Change 1 was committed before snapshot, so conv1ID MUST be present in snapshot
	foundConv1InSnap := false
	for _, c := range snap.Conversations {
		if c.ID == conv1ID && c.Version >= 1 {
			foundConv1InSnap = true
			break
		}
	}
	if !foundConv1InSnap {
		t.Fatalf("conversa 1 ausente ou versão inválida no snapshot: %#v", snap.Conversations)
	}

	// conv1ID != conv2ID: presence of conv1 cannot masquerade as conv2
	if conv1ID == conv2ID {
		t.Fatal("conv1ID e conv2ID não devem ser iguais (devem ser contatos/conversas distintos)")
	}

	lastCursor := snap.NextCursor

	// Read streamed events until Change 2 (committed right after snapshot cursor) arrives via live stream
	var foundMsg2 bool
	var conv2Version int64
	deadline := time.After(4 * time.Second)
	for !foundMsg2 {
		select {
		case <-deadline:
			t.Fatalf("tempo esgotado aguardando evento ao vivo da mudança 2 (msg2ID=%s, conv2ID=%s)", msg2ID, conv2ID)
		default:
		}

		var ev ServerMessage
		readCtx, readCancel := context.WithTimeout(ctx, 1*time.Second)
		err := wsjson.Read(readCtx, conn, &ev)
		readCancel()
		if err != nil {
			t.Fatalf("falha ao ler eventos transmitidos: %s", err)
		}
		if ev.Type == "events" {
			if ev.NextCursor > lastCursor {
				lastCursor = ev.NextCursor
			}
			for _, event := range ev.Events {
				if event.AggregateID == conv2ID {
					if event.Version > conv2Version {
						conv2Version = event.Version
					}
					if strings.Contains(string(event.Payload), msg2ID.String()) {
						foundMsg2 = true
					}
				}
			}
		}
	}

	if !foundMsg2 || conv2Version < 1 {
		t.Fatalf("mudança 2 concorrente não foi recebida com versão válida: foundMsg2=%v, conv2Version=%d", foundMsg2, conv2Version)
	}

	// Close first connection
	conn.Close(websocket.StatusNormalClosure, "reconnecting")

	// 3. Commit Change 3 during disconnection (on conv1, bumping version to 2)
	msg3ID, conv3ID := sendTestMessageCustom(t, w, w.inbox, w.contact, w.identity, w.connector, "Mensagem 3 para recuperação pós-reconexão")
	if conv3ID != conv1ID {
		t.Fatalf("esperava reuso da conversa 1 ativa para msg3, obteve %s", conv3ID)
	}

	// 4. Reconnect with since_seq = lastCursor
	conn2, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String()+"&since_seq="+strconv.FormatInt(lastCursor, 10), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao reconectar: %s", err)
	}
	defer conn2.Close(websocket.StatusNormalClosure, "done")

	// Read events on reconnected socket
	var ev2 ServerMessage
	if err := wsjson.Read(ctx, conn2, &ev2); err != nil {
		t.Fatalf("falha ao ler eventos na reconexão: %s", err)
	}
	if ev2.Type != "events" || ev2.NextCursor <= lastCursor {
		t.Fatalf("esperava eventos avançando cursor na reconexão: %#v (lastCursor: %d)", ev2, lastCursor)
	}

	// Verify the identity of the 3rd change is explicitly recovered and verified
	foundMsg3 := false
	for _, event := range ev2.Events {
		if event.AggregateID == conv1ID && event.Version >= 2 {
			if strings.Contains(string(event.Payload), msg3ID.String()) {
				foundMsg3 = true
				break
			}
		}
	}
	if !foundMsg3 {
		t.Fatalf("mudança 3 produzida durante a desconexão (msg3ID=%s, conv1ID=%s v2) não foi recuperada: %#v", msg3ID, conv1ID, ev2.Events)
	}
	_ = msg1ID
}

func TestSwitchInboxInvisibleEventAndReplay(t *testing.T) {
	w := seedWorld(t, "agent")
	h, _ := newTestHandler(20*time.Millisecond, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Client subscribes to inbox 1
	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}
	cursorBefore := snap.NextCursor

	// 2. Send message to inbox 2 (different inbox in same tenant)
	mBID, _ := sendTestMessage(t, w, w.inbox2, "Mensagem para caixa 2 (invisível para caixa 1)")

	// 3. Client on inbox 1 receives an events message with advanced NextCursor and 0 events
	var ev ServerMessage
	if err := wsjson.Read(ctx, conn, &ev); err != nil {
		t.Fatalf("falha ao ler avanço de cursor para eventos invisíveis: %s", err)
	}
	if ev.Type != "events" {
		t.Fatalf("esperava tipo events, obteve: %#v", ev)
	}
	if ev.NextCursor <= cursorBefore {
		t.Fatalf("NextCursor não avançou após evento invisível: antes=%d depois=%d", cursorBefore, ev.NextCursor)
	}
	if len(ev.Events) != 0 {
		t.Fatalf("evento da caixa 2 vazou para a caixa 1: %#v", ev.Events)
	}

	// 4. Now, ON THE SAME SOCKET, replay events for inbox 2 from cursorBefore
	err = wsjson.Write(ctx, conn, ClientMessage{
		Action:  "replay",
		InboxID: w.inbox2,
		After:   cursorBefore,
	})
	if err != nil {
		t.Fatalf("falha ao enviar replay para inbox 2: %s", err)
	}

	// 5. Must receive the event for inbox 2; dedup must NOT have suppressed it!
	var evB ServerMessage
	if err := wsjson.Read(ctx, conn, &evB); err != nil {
		t.Fatalf("falha ao ler eventos de inbox 2 após troca de caixa: %s", err)
	}
	if evB.Type != "events" || len(evB.Events) == 0 {
		t.Fatalf("evento de inbox 2 foi indevidamente suprimido pelo dedup de inbox 1: %#v", evB)
	}
	_ = mBID
}

type flakyResolver struct {
	real      InboxResolver
	mu        sync.Mutex
	failCount int
}

func (f *flakyResolver) ResolveConversationInbox(ctx context.Context, tenantID, conversationID uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCount > 0 {
		f.failCount--
		return uuid.Nil, errors.New("falha transitória na conexão do banco de dados")
	}
	return f.real.ResolveConversationInbox(ctx, tenantID, conversationID)
}

func TestTransientInboxResolverErrorDoesNotDropEvents(t *testing.T) {
	w := seedWorld(t, "agent")
	adapter := NewAuthAdapter(testAuth, testStore)
	flaky := &flakyResolver{
		real:      adapter,
		failCount: 1, // Will fail on the first lookup and succeed on retry
	}

	h := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      adapter,
		Attendance:         testAttendance,
		InboxResolver:      flaky,
		Notifier:           testNotifier,
		PollInterval:       20 * time.Millisecond,
		RevalidateInterval: 1 * time.Second,
		BufferSize:         16,
		WriteTimeout:       2 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}

	// Send message to trigger event
	sendTestMessage(t, w, w.inbox, "Mensagem para teste de erro transitório")

	// Read event on client: even though first lookup failed, retry must succeed without dropping the event!
	var ev ServerMessage
	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()

	if err := wsjson.Read(readCtx, conn, &ev); err != nil {
		t.Fatalf("falha ao receber evento após retry de erro transitório: %s", err)
	}
	if ev.Type != "events" || len(ev.Events) == 0 {
		t.Fatalf("evento foi descartado pelo erro transitório do resolver: %#v", ev)
	}
}

type gatingAuthenticator struct {
	real         Authenticator
	mu           sync.Mutex
	onRevalidate func()
}

func (g *gatingAuthenticator) AuthenticateSession(ctx context.Context, sessionToken string) (Principal, error) {
	return g.real.AuthenticateSession(ctx, sessionToken)
}

func (g *gatingAuthenticator) RevalidatePrincipal(ctx context.Context, p Principal, sessionToken string) error {
	g.mu.Lock()
	hook := g.onRevalidate
	g.mu.Unlock()
	if hook != nil && ctx.Value(deliveryContextKey{}) == true {
		hook()
	}
	return g.real.RevalidatePrincipal(ctx, p, sessionToken)
}

func TestPermissionRevocationAndSuspension(t *testing.T) {
	ctx := context.Background()

	// 1. Revocation via Logout with pending messages in outbox
	t.Run("LogoutWithPendingOutbox", func(t *testing.T) {
		w := seedWorld(t, "agent")
		adapter := NewAuthAdapter(testAuth, testStore)
		gating := &gatingAuthenticator{real: adapter}

		h := NewHandler(Options{
			TrustedOrigins:     []string{testTrustedOrigin},
			Authenticator:      gating,
			Attendance:         testAttendance,
			InboxResolver:      adapter,
			Notifier:           testNotifier,
			PollInterval:       20 * time.Millisecond,
			RevalidateInterval: 10 * time.Second, // prevents timer revalidation from interfering
			BufferSize:         16,
			WriteTimeout:       2 * time.Second,
		})
		srv := httptest.NewServer(h)
		defer srv.Close()

		wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
		testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(testCtx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": []string{testTrustedOrigin},
				"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
			},
		})
		if err != nil {
			t.Fatalf("falha ao conectar: %s", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var snap ServerMessage
		if err := wsjson.Read(testCtx, conn, &snap); err != nil || snap.Type != "snapshot" {
			t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
		}

		// Control the boundary between enqueueing and consumption:
		// Hold writePump exactly when it is about to revalidate for delivering the pending payload.
		var revalEntered sync.WaitGroup
		revalEntered.Add(1)
		var allowReval sync.WaitGroup
		allowReval.Add(1)

		gating.mu.Lock()
		gating.onRevalidate = func() {
			revalEntered.Done()
			allowReval.Wait()
		}
		gating.mu.Unlock()

		// Enqueue message
		sendTestMessage(t, w, w.inbox, "Msg pendente garantida no writePump")

		// Wait until writePump dequeues the message and is at the consumption revalidation boundary
		revalEntered.Wait()

		// Payload is provably pending consumption on the server. Revoke session now!
		if err := testAuth.Logout(testCtx, w.sessionToken); err != nil {
			t.Fatalf("falha ao revogar sessão: %s", err)
		}

		// Allow writePump to proceed with revalidation: it must query DB, detect revocation, drop payload, and close socket.
		allowReval.Done()

		// Client must receive NO business payload and connection must close with StatusPolicyViolation (1008)
		var incoming ServerMessage
		readCtx, readCancel := context.WithTimeout(testCtx, 2*time.Second)
		defer readCancel()
		err = wsjson.Read(readCtx, conn, &incoming)
		if err == nil {
			t.Fatalf("falha de segurança: payload foi entregue após revogação de autorização: %#v", incoming)
		}
		status := websocket.CloseStatus(err)
		if status != websocket.StatusPolicyViolation {
			t.Fatalf("esperava StatusPolicyViolation (1008) na revogação, obteve código %v (err: %v)", status, err)
		}
	})

	// 2. User suspension
	t.Run("UserSuspension", func(t *testing.T) {
		w := seedWorld(t, "agent")
		adapter := NewAuthAdapter(testAuth, testStore)
		gating := &gatingAuthenticator{real: adapter}

		h := NewHandler(Options{
			TrustedOrigins:     []string{testTrustedOrigin},
			Authenticator:      gating,
			Attendance:         testAttendance,
			InboxResolver:      adapter,
			Notifier:           testNotifier,
			PollInterval:       20 * time.Millisecond,
			RevalidateInterval: 10 * time.Second,
			BufferSize:         16,
			WriteTimeout:       2 * time.Second,
		})
		srv := httptest.NewServer(h)
		defer srv.Close()

		wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
		testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(testCtx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": []string{testTrustedOrigin},
				"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
			},
		})
		if err != nil {
			t.Fatalf("falha ao conectar: %s", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var snap ServerMessage
		if err := wsjson.Read(testCtx, conn, &snap); err != nil || snap.Type != "snapshot" {
			t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
		}

		var revalEntered sync.WaitGroup
		revalEntered.Add(1)
		var allowReval sync.WaitGroup
		allowReval.Add(1)

		gating.mu.Lock()
		gating.onRevalidate = func() {
			revalEntered.Done()
			allowReval.Wait()
		}
		gating.mu.Unlock()

		sendTestMessage(t, w, w.inbox, "Msg pendente para suspensão de usuário")
		revalEntered.Wait()

		// Suspend user while message is pending in writePump
		err = testStore.WithPlatform(testCtx, func(txCtx context.Context) error {
			tag, err := testPools.Platform.Exec(txCtx, `UPDATE users SET status = 'disabled' WHERE id = $1`, w.user)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errors.New("nenhum usuário atualizado")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("falha ao desativar usuário: %s", err)
		}

		allowReval.Done()

		var incoming ServerMessage
		readCtx, readCancel := context.WithTimeout(testCtx, 2*time.Second)
		defer readCancel()
		err = wsjson.Read(readCtx, conn, &incoming)
		if err == nil {
			t.Fatalf("falha de segurança: payload entregue após suspensão do usuário: %#v", incoming)
		}
		status := websocket.CloseStatus(err)
		if status != websocket.StatusPolicyViolation {
			t.Fatalf("esperava StatusPolicyViolation (1008) na suspensão do usuário, obteve %v (err: %v)", status, err)
		}
	})

	// 3. Membership suspension
	t.Run("MembershipSuspension", func(t *testing.T) {
		w := seedWorld(t, "agent")
		adapter := NewAuthAdapter(testAuth, testStore)
		gating := &gatingAuthenticator{real: adapter}

		h := NewHandler(Options{
			TrustedOrigins:     []string{testTrustedOrigin},
			Authenticator:      gating,
			Attendance:         testAttendance,
			InboxResolver:      adapter,
			Notifier:           testNotifier,
			PollInterval:       20 * time.Millisecond,
			RevalidateInterval: 10 * time.Second,
			BufferSize:         16,
			WriteTimeout:       2 * time.Second,
		})
		srv := httptest.NewServer(h)
		defer srv.Close()

		wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
		testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(testCtx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": []string{testTrustedOrigin},
				"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
			},
		})
		if err != nil {
			t.Fatalf("falha ao conectar: %s", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var snap ServerMessage
		if err := wsjson.Read(testCtx, conn, &snap); err != nil || snap.Type != "snapshot" {
			t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
		}

		var revalEntered sync.WaitGroup
		revalEntered.Add(1)
		var allowReval sync.WaitGroup
		allowReval.Add(1)

		gating.mu.Lock()
		gating.onRevalidate = func() {
			revalEntered.Done()
			allowReval.Wait()
		}
		gating.mu.Unlock()

		sendTestMessage(t, w, w.inbox, "Msg pendente para suspensão de membership")
		revalEntered.Wait()

		// Suspend membership while payload is pending
		err = testStore.WithTenant(testCtx, w.tenant, func(txCtx context.Context) error {
			_, err := testStore.UpdateMembershipStatus(txCtx, w.membership, "disabled")
			return err
		})
		if err != nil {
			t.Fatalf("falha ao desativar membership: %s", err)
		}

		allowReval.Done()

		var incoming ServerMessage
		readCtx, readCancel := context.WithTimeout(testCtx, 2*time.Second)
		defer readCancel()
		err = wsjson.Read(readCtx, conn, &incoming)
		if err == nil {
			t.Fatalf("falha de segurança: payload entregue após suspensão do vínculo: %#v", incoming)
		}
		status := websocket.CloseStatus(err)
		if status != websocket.StatusPolicyViolation {
			t.Fatalf("esperava StatusPolicyViolation (1008) na suspensão de vínculo, obteve %v (err: %v)", status, err)
		}
	})

	// 4. Tenant suspension
	t.Run("TenantSuspension", func(t *testing.T) {
		w := seedWorld(t, "agent")
		adapter := NewAuthAdapter(testAuth, testStore)
		gating := &gatingAuthenticator{real: adapter}

		h := NewHandler(Options{
			TrustedOrigins:     []string{testTrustedOrigin},
			Authenticator:      gating,
			Attendance:         testAttendance,
			InboxResolver:      adapter,
			Notifier:           testNotifier,
			PollInterval:       20 * time.Millisecond,
			RevalidateInterval: 10 * time.Second,
			BufferSize:         16,
			WriteTimeout:       2 * time.Second,
		})
		srv := httptest.NewServer(h)
		defer srv.Close()

		wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
		testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(testCtx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": []string{testTrustedOrigin},
				"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
			},
		})
		if err != nil {
			t.Fatalf("falha ao conectar: %s", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var snap ServerMessage
		if err := wsjson.Read(testCtx, conn, &snap); err != nil || snap.Type != "snapshot" {
			t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
		}

		var revalEntered sync.WaitGroup
		revalEntered.Add(1)
		var allowReval sync.WaitGroup
		allowReval.Add(1)

		gating.mu.Lock()
		gating.onRevalidate = func() {
			revalEntered.Done()
			allowReval.Wait()
		}
		gating.mu.Unlock()

		sendTestMessage(t, w, w.inbox, "Msg pendente para suspensão de tenant")
		revalEntered.Wait()

		// Suspend tenant while message is pending in writePump
		err = testStore.WithPlatform(testCtx, func(txCtx context.Context) error {
			_, err := testStore.SetTenantStatus(txCtx, w.tenant, "suspended")
			return err
		})
		if err != nil {
			t.Fatalf("falha ao suspender tenant: %s", err)
		}

		allowReval.Done()

		var incoming ServerMessage
		readCtx, readCancel := context.WithTimeout(testCtx, 2*time.Second)
		defer readCancel()
		err = wsjson.Read(readCtx, conn, &incoming)
		if err == nil {
			t.Fatalf("falha de segurança: payload entregue após suspensão do tenant: %#v", incoming)
		}
		status := websocket.CloseStatus(err)
		if status != websocket.StatusPolicyViolation {
			t.Fatalf("esperava StatusPolicyViolation (1008) na suspensão do tenant, obteve %v (err: %v)", status, err)
		}
	})
}

func TestRetentionGapExplicitResync(t *testing.T) {
	w := seedWorld(t, "agent")
	h, _ := newTestHandler(20*time.Millisecond, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}

	// Produce 2 messages
	sendTestMessage(t, w, w.inbox, "Msg 1 para avanço de seq")
	sendTestMessage(t, w, w.inbox, "Msg 2 para avanço de seq")

	var ev ServerMessage
	for {
		if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "events" {
			t.Fatalf("esperava eventos, obteve: %#v (err: %v)", ev, err)
		}
		if ev.NextCursor >= 3 {
			break
		}
	}

	// Compact events moving retain_after up to ev.NextCursor
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return store.Repo{}.Compact(ctx, tx, w.tenant, ev.NextCursor)
	})
	if err != nil {
		t.Fatalf("falha ao compactar eventos: %s", err)
	}

	// Now ask for replay from sequence 0 (older than retain_after floor)
	err = wsjson.Write(ctx, conn, ClientMessage{
		Action: "replay",
		After:  0,
	})
	if err != nil {
		t.Fatalf("falha ao enviar replay pós-expurgo: %s", err)
	}

	var resyncMsg ServerMessage
	if err := wsjson.Read(ctx, conn, &resyncMsg); err != nil {
		t.Fatalf("falha ao ler resync: %s", err)
	}
	if resyncMsg.Type != "resync" || resyncMsg.Reason != "retention_gap" {
		t.Fatalf("esperava mensagem explícita de resync com retention_gap, obteve: %#v", resyncMsg)
	}
}

func TestPGNotifySignaling(t *testing.T) {
	w := seedWorld(t, "agent")
	// Set PollInterval to 10 seconds: updates MUST come via PG NOTIFY!
	h, _ := newTestHandler(10*time.Second, 1*time.Second, 16)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}

	// Insert message
	sendTestMessage(t, w, w.inbox, "Mensagem acordada por NOTIFY")

	// Trigger PG NOTIFY with minimal tenant_id
	if err := EmitNotify(ctx, testPools.App, w.tenant); err != nil {
		t.Fatalf("falha ao emitir NOTIFY: %s", err)
	}

	// Must be received quickly (in < 1.5s, way before 10s poll ticker)
	readCtx, readCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer readCancel()

	var ev ServerMessage
	if err := wsjson.Read(readCtx, conn, &ev); err != nil {
		t.Fatalf("NOTIFY não acordou a entrega de eventos duráveis a tempo: %s", err)
	}
	if ev.Type != "events" || len(ev.Events) == 0 {
		t.Fatalf("evento incorreto recebido pós-NOTIFY: %#v", ev)
	}
}

func TestSlowClientBackpressure(t *testing.T) {
	w := seedWorld(t, "agent")
	// Tiny buffer of 2 messages and fast write timeout
	h, adapter := newTestHandler(20*time.Millisecond, 1*time.Second, 2)
	gating := &gatingAuthenticator{real: adapter}
	h.opts.Authenticator = gating
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}

	// Stall delivery so the bounded queue remains full past the deadline.
	release := make(chan struct{})
	defer close(release)
	gating.mu.Lock()
	gating.onRevalidate = func() { <-release }
	gating.mu.Unlock()
	// Produce enough events to fill the queue while consumption is stalled.
	for i := range 10 {
		sendTestMessage(t, w, w.inbox, "Mensagem rápida para estourar buffer "+string(rune('A'+i)))
	}

	// Now try to read: server should have disconnected with StatusPolicyViolation (1008)
	var ev ServerMessage
	for {
		err := wsjson.Read(ctx, conn, &ev)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status != websocket.StatusPolicyViolation {
				t.Fatalf("esperava desconexão com StatusPolicyViolation (1008), obteve código %v (err: %v)", status, err)
			}
			break
		}
	}
}

func TestBoundedCacheAndDedup(t *testing.T) {
	cache := newBoundedCache(3)
	id1, id2, id3, id4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	target := uuid.New()

	cache.put(id1, target)
	cache.put(id2, target)
	cache.put(id3, target)
	if cache.len() != 3 {
		t.Fatalf("esperava 3 itens no cache, obteve %d", cache.len())
	}

	// 4th item evicts oldest (id1)
	cache.put(id4, target)
	if cache.len() != 3 {
		t.Fatalf("capacidade máxima não respeitada: %d", cache.len())
	}
	if _, ok := cache.get(id1); ok {
		t.Fatal("id1 deveria ter sido evictado")
	}
	if _, ok := cache.get(id4); !ok {
		t.Fatal("id4 deveria estar no cache")
	}

	// Dedup reset
	dedup := newEventDedup(10)
	evID := uuid.New()
	if dedup.hasOrAdd(evID, 1) {
		t.Fatal("evento não deveria estar presente")
	}
	if !dedup.hasOrAdd(evID, 1) {
		t.Fatal("evento deveria estar presente")
	}
	dedup.reset()
	if dedup.hasOrAdd(evID, 1) {
		t.Fatal("evento não deveria estar presente pós-reset")
	}
}

func TestInboxIsolationWithoutResolver(t *testing.T) {
	w := seedWorld(t, "agent")
	adapter := NewAuthAdapter(testAuth, testStore)

	// Handler configured with InboxResolver: nil
	h := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      adapter,
		Attendance:         testAttendance,
		InboxResolver:      nil, // EXPLICITLY NIL
		Notifier:           testNotifier,
		PollInterval:       20 * time.Millisecond,
		RevalidateInterval: 1 * time.Second,
		BufferSize:         16,
		WriteTimeout:       2 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + w.sessionToken},
		},
	})
	if err != nil {
		t.Fatalf("falha ao conectar: %s", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	var snap ServerMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot inicial falhou: %#v (err: %v)", snap, err)
	}
	cursorBefore := snap.NextCursor

	// Send message to inbox 2 (different inbox in same tenant)
	m2ID, conv2ID := sendTestMessage(t, w, w.inbox2, "Mensagem confidencial para caixa 2 sem resolver")

	// Read event on socket subscribed to inbox 1:
	// Without resolver, the server cannot determine the inbox for conv2, so delivery MUST be denied!
	// It should receive only an events message advancing the cursor, with 0 events delivered.
	var ev ServerMessage
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	err = wsjson.Read(readCtx, conn, &ev)
	readCancel()
	if err != nil {
		t.Fatalf("falha ao ler avanço de cursor: %s", err)
	}
	if ev.Type != "events" {
		t.Fatalf("esperava tipo events, obteve: %#v", ev)
	}
	if ev.NextCursor <= cursorBefore {
		t.Fatalf("cursor deveria ter avançado além de %d, obteve %d", cursorBefore, ev.NextCursor)
	}
	if len(ev.Events) != 0 {
		t.Fatalf("falha de isolamento: evento da caixa 2 foi entregue na caixa 1 quando InboxResolver era nil: %#v", ev.Events)
	}
	_ = m2ID
	_ = conv2ID
}

func TestBoundedAttendanceLimitExceededAndPaging(t *testing.T) {
	w := seedWorld(t, "agent")
	adapter := NewAuthAdapter(testAuth, testStore)
	bounded := NewBoundedAttendance(testStore, testAttendance)

	ctx := context.Background()
	p := principal(w)

	// 1. Verify that inserting 505 events allows bounded pagination:
	// Page 1 returns maxEventsPerBatch (500) and NextCursor == 500 (distinguishing examined page from pending)
	// Page 2 returns the remaining 5 events with NextCursor == 505
	err := testStore.WithTenantTx(ctx, w.tenant, func(txCtx context.Context, tx pgx.Tx) error {
		for i := 1; i <= 505; i++ {
			if _, err := tx.Exec(txCtx, `
				INSERT INTO realtime_events (tenant_id, seq, id, event_type, aggregate_type, aggregate_id, version, payload)
				VALUES ($1, $2, $3, 'conversation.test', 'conversation', $4, 1, '{}')
			`, w.tenant, int64(i), uuid.New(), w.contact); err != nil {
				return err
			}
		}
		_, err := tx.Exec(txCtx, `
			INSERT INTO realtime_tenant_cursors (tenant_id, seq, retain_after)
			VALUES ($1, 505, 0)
			ON CONFLICT (tenant_id) DO UPDATE SET seq = 505
		`, w.tenant)
		return err
	})
	if err != nil {
		t.Fatalf("falha ao semear 505 eventos: %s", err)
	}

	page1, err := bounded.Events(ctx, p, w.inbox, 0)
	if err != nil {
		t.Fatalf("falha ao ler página 1 de eventos: %s", err)
	}
	if len(page1.Events) != 500 {
		t.Fatalf("esperava exatamente 500 eventos na página 1, obteve %d", len(page1.Events))
	}
	if page1.NextCursor != 500 {
		t.Fatalf("esperava NextCursor=500 na página 1 (distinguindo página examinada), obteve %d", page1.NextCursor)
	}

	page2, err := bounded.Events(ctx, p, w.inbox, page1.NextCursor)
	if err != nil {
		t.Fatalf("falha ao ler página 2 de eventos: %s", err)
	}
	if len(page2.Events) != 5 {
		t.Fatalf("esperava 5 eventos na página 2, obteve %d", len(page2.Events))
	}
	if page2.NextCursor != 505 {
		t.Fatalf("esperava NextCursor=505 na página 2, obteve %d", page2.NextCursor)
	}

	// 2. Verify snapshot load ceiling:
	// Insert 501 conversations to exceed maxSnapshotConversations (500)
	err = testStore.WithTenantTx(ctx, w.tenant, func(txCtx context.Context, tx pgx.Tx) error {
		for i := 1; i <= 501; i++ {
			if _, err := tx.Exec(txCtx, `
				INSERT INTO conversations (
					id, tenant_id, inbox_id, contact_id, contact_identity_id,
					protocol, protocol_period, status, version, controller,
					controller_epoch, cycle_number, summary
				) VALUES (
					$1, $2, $3, $4, $5,
					$6, '202610', 'closed', 1, 'bot',
					1, 1, 'resumo teste'
				)
			`, uuid.New(), w.tenant, w.inbox, w.contact, w.identity, fmt.Sprintf("PROTO-%d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("falha ao semear 501 conversas: %s", err)
	}

	// Snapshot must return ErrLoadLimitExceeded without allocating full table in memory
	snap, err := bounded.Snapshot(ctx, p, store.SnapshotInput{
		TenantID: w.tenant,
		InboxID:  w.inbox,
	})
	if !errors.Is(err, ErrLoadLimitExceeded) {
		t.Fatalf("esperava ErrLoadLimitExceeded ao exceder 500 conversas, obteve err=%v snap=%#v", err, snap)
	}
	if snap.NextCursor != 505 {
		t.Fatalf("esperava cursor coerente (505) no snapshot com excesso de carga, obteve %d", snap.NextCursor)
	}
	_ = adapter
}

type denyingAttendance struct{ AttendanceService }

func (d denyingAttendance) Snapshot(context.Context, attendanceService.Principal, store.SnapshotInput) (store.Snapshot, error) {
	return store.Snapshot{}, attendanceService.ErrInboxForbidden
}
func (d denyingAttendance) Events(context.Context, attendanceService.Principal, uuid.UUID, int64) (store.EventPage, error) {
	return store.EventPage{}, attendanceService.ErrInboxForbidden
}

func TestInjectedAttendanceDenialWithDB(t *testing.T) {
	w := seedWorld(t, "agent")
	adapter := NewAuthAdapter(testAuth, testStore)
	denied := denyingAttendance{testAttendance}
	h := NewHandler(Options{DB: testStore, Authenticator: adapter, InboxResolver: adapter, Attendance: denied, TrustedOrigins: []string{testTrustedOrigin}})
	if _, ok := h.opts.Attendance.(denyingAttendance); !ok {
		t.Fatal("serviço injetado foi substituído")
	}
	if _, err := h.opts.Attendance.Snapshot(context.Background(), principal(w), store.SnapshotInput{InboxID: w.inbox}); !errors.Is(err, attendanceService.ErrInboxForbidden) {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := h.opts.Attendance.Events(context.Background(), principal(w), w.inbox, 0); !errors.Is(err, attendanceService.ErrInboxForbidden) {
		t.Fatalf("events: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http://", "ws://", 1)+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {testTrustedOrigin}, "Cookie": {auth.SessionCookieName + "=" + w.sessionToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var msg ServerMessage
	if err := wsjson.Read(ctx, ws, &msg); err == nil && (msg.Type == "snapshot" || msg.Type == "events") {
		t.Fatalf("conteúdo após negação: %#v", msg)
	}
}

func TestSocketReplayAcrossPageAndDisconnectedConnector(t *testing.T) {
	w := seedWorld(t, "agent")
	_, conversation := sendTestMessage(t, w, w.inbox, "primeira")
	h, _ := newTestHandler(10*time.Millisecond, 10*time.Second, 64)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http://", "ws://", 1)+"?inbox_id="+w.inbox.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {testTrustedOrigin}, "Cookie": {auth.SessionCookieName + "=" + w.sessionToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var snap ServerMessage
	if err := wsjson.Read(ctx, ws, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("snapshot: %#v %v", snap, err)
	}
	start := snap.NextCursor
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE channel_connectors SET status='disconnected', enabled=false WHERE tenant_id=$1 AND id=$2`, w.tenant, w.connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO realtime_events (tenant_id, seq, id, event_type, aggregate_type, aggregate_id, version, payload) SELECT $1, $2+n, gen_random_uuid(), 'conversation.test', 'conversation', $3, 1, '{}' FROM generate_series(1, 505) n`, w.tenant, start, conversation); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE realtime_tenant_cursors SET seq=$2 WHERE tenant_id=$1`, w.tenant, start+505)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	bounded := NewBoundedAttendance(testStore, testAttendance)
	if _, err := bounded.Snapshot(ctx, principal(w), store.SnapshotInput{InboxID: w.inbox}); err != nil {
		t.Fatalf("snapshot canal indisponível: %v", err)
	}
	if _, err := bounded.Events(ctx, principal(w), uuid.New(), start); !errors.Is(err, attendanceService.ErrInboxForbidden) {
		t.Fatalf("caixa inválida: %v", err)
	}
	count := 0
	cursor := start
	for cursor < start+505 {
		var msg ServerMessage
		if err := wsjson.Read(ctx, ws, &msg); err != nil {
			t.Fatalf("replay após %d eventos cursor %d: %v", count, cursor, err)
		}
		if msg.Type == "events" {
			count += len(msg.Events)
			cursor = msg.NextCursor
		}
	}
	if count != 505 {
		t.Fatalf("eventos recebidos: %d", count)
	}
}
