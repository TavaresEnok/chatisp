package outwebhook

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatisp/internal/platform/urlguard"
	"github.com/google/uuid"
)

func TestStableIDAndSignature(t *testing.T) {
	tenant, sub, source := uuid.New(), uuid.New(), uuid.New()
	first, err := StableID(tenant, sub, EventMessageCreated, source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StableID(tenant, sub, EventMessageCreated, source)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.Version() != 5 {
		t.Fatalf("id: %s %s versão %d", first, second, first.Version())
	}
	other, err := StableID(tenant, sub, EventMessageCreated, uuid.New())
	if err != nil || other == first {
		t.Fatalf("fonte diferente: %s %v", other, err)
	}
	secret := []byte("segredo-de-teste")
	body := []byte(`{"event_id":"1"}`)
	header, err := Sign(secret, body)
	if err != nil {
		t.Fatal(err)
	}
	if err = Verify(secret, body, header); err != nil {
		t.Fatal(err)
	}
	if err = Verify([]byte("outro"), body, header); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("segredo: %v", err)
	}
	tampered := append([]byte(nil), body...)
	tampered[0] = 'X'
	if err = Verify(secret, tampered, header); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("corpo: %v", err)
	}
}

func TestRetryKeepsIDAndDoesNotDropTheMessage(t *testing.T) {
	msg, err := AcceptLocal("  texto local do cliente  ")
	if err != nil {
		t.Fatal(err)
	}
	keptID, keptBody := msg.ID, msg.Body
	sub := Subscription{
		ID: uuid.New(), TenantID: uuid.New(), URL: "https://example.com/hook",
		Secret: []byte("segredo-de-teste"), Enabled: true,
		Events: map[string]bool{EventMessageCreated: true},
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	delivery, err := Enqueue(sub, EventMessageCreated, msg.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(delivery.Body, []byte(keptBody)) {
		t.Fatal("o corpo do webhook levou o texto da mensagem")
	}
	var seen []string
	var bodies [][]byte
	poster := PosterFunc(func(_ context.Context, _ string, body []byte, header http.Header) (int, error) {
		seen = append(seen, header.Get(HeaderEventID))
		bodies = append(bodies, append([]byte(nil), body...))
		if len(seen) < 3 {
			return 500, nil
		}
		return 200, nil
	})
	clock := now
	for delivery.Status == StatusPending {
		delivery, err = Dispatch(context.Background(), poster, sub.Secret, sub.URL, delivery, clock)
		if err != nil {
			t.Fatal(err)
		}
		clock = delivery.NextAt
	}
	if delivery.Status != StatusDelivered || delivery.Attempt != 3 || delivery.ID.String() != seen[0] {
		t.Fatalf("entrega: %+v ids=%v", delivery, seen)
	}
	for _, id := range seen {
		if id != delivery.ID.String() {
			t.Fatalf("id mudou: %v", seen)
		}
	}
	for _, body := range bodies {
		if !bytes.Equal(body, delivery.Body) {
			t.Fatal("corpo mudou entre tentativas")
		}
	}
	if msg.ID != keptID || msg.Body != keptBody {
		t.Fatal("a mensagem local foi alterada")
	}
}

func TestDeadAfterTheAttemptBudget(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	d := Delivery{ID: id, Status: StatusPending, Body: []byte(`{"event_id":"1"}`), Type: EventConversationClosed, NextAt: now}
	for d.Status == StatusPending {
		d = Advance(d, 500, nil, now)
		now = d.NextAt
	}
	if d.Status != StatusDead || d.Attempt != MaxAttempts || d.ID != id {
		t.Fatalf("morto: %+v", d)
	}
}

func TestFakeReceiverRejectsReplay(t *testing.T) {
	secret := []byte("segredo-de-teste")
	sub := Subscription{
		ID: uuid.New(), TenantID: uuid.New(), URL: "https://example.com/hook",
		Secret: secret, Enabled: true,
		Events: map[string]bool{EventConversationCreated: true, EventConversationAssigned: true, EventConversationClosed: true},
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	source := uuid.New()
	delivery, err := Enqueue(sub, EventConversationCreated, source, now)
	if err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(secret)
	receiver.Failures = 1
	post := func(d Delivery) *httptest.ResponseRecorder {
		t.Helper()
		header, err := Headers(secret, d)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "https://example.com/hook", bytes.NewReader(d.Body))
		req.Header = header
		rec := httptest.NewRecorder()
		receiver.ServeHTTP(rec, req)
		return rec
	}
	if post(delivery).Code != 500 || receiver.Applied() != 0 {
		t.Fatalf("falha inicial applied=%d", receiver.Applied())
	}
	delivery = Advance(delivery, 500, nil, now)
	if delivery.ID.String() != sourceString(t, sub, EventConversationCreated, source) {
		t.Fatal("retentativa trocou o id")
	}
	if post(delivery).Code != 200 || receiver.Applied() != 1 {
		t.Fatalf("aceite applied=%d", receiver.Applied())
	}
	replay := post(delivery)
	if replay.Code != http.StatusConflict || receiver.Applied() != 1 {
		t.Fatalf("replay code=%d applied=%d body=%s", replay.Code, receiver.Applied(), replay.Body.String())
	}
	bad := NewReceiver([]byte("outro-segredo"))
	req := httptest.NewRequest(http.MethodPost, "https://example.com/hook", bytes.NewReader(delivery.Body))
	header, err := Headers(secret, delivery)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	rec := httptest.NewRecorder()
	bad.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || bad.Applied() != 0 {
		t.Fatalf("assinatura: %d applied=%d", rec.Code, bad.Applied())
	}
	assigned, err := Enqueue(sub, EventConversationAssigned, source, now)
	if err != nil || assigned.ID == delivery.ID {
		t.Fatal("atribuição deveria ser outro evento")
	}
	closed, err := Enqueue(sub, EventConversationClosed, source, now)
	if err != nil || closed.ID == delivery.ID || closed.ID == assigned.ID {
		t.Fatal("encerramento deveria ser outro evento")
	}
}

func TestGuardedPosterBlocksPrivateTargets(t *testing.T) {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	poster := GuardedPoster{
		Policy: policy,
		Scope:  urlguard.Scope{},
		Next: PosterFunc(func(context.Context, string, []byte, http.Header) (int, error) {
			called++
			return 200, nil
		}),
	}
	for _, raw := range []string{
		"http://127.0.0.1/hook",
		"http://169.254.169.254/latest",
		"http://10.1.1.1/hook",
		"http://user:pass@example.com/hook",
	} {
		if _, err = poster.Post(context.Background(), raw, []byte("{}"), nil); err == nil {
			t.Fatalf("aceitou %s", raw)
		}
	}
	if called != 0 {
		t.Fatalf("discou destino bloqueado: %d", called)
	}
	if _, err = poster.Post(context.Background(), "https://example.com/hook", []byte("{}"), nil); err != nil || called != 1 {
		t.Fatalf("público: %v called=%d", err, called)
	}
}

func sourceString(t *testing.T, sub Subscription, eventType string, source uuid.UUID) string {
	t.Helper()
	id, err := StableID(sub.TenantID, sub.ID, eventType, source)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
