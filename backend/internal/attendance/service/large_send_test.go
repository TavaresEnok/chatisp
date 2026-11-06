package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"chatisp/internal/attendance/store"
	"github.com/jackc/pgx/v5"
)

func TestSendLargeEscapedBodyWithSignatureAndReplay(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	svc := live(nil)
	p := principal(w)
	p.Email = "Ana"
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO attendant_signature_settings (tenant_id,enabled,prefix,version) VALUES ($1,true,'{{nome_atendente}}: ',1)`, w.tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	conv := mustAssume(t, svc, p, mustReceive(t, svc, w, "large-text", "oi", at(0)).Conversation)
	body := strings.Repeat("<", 15995)
	in := SendInput{InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: conv.Version, ClientRequestID: "large-1", Body: body, At: at(4)}
	sent, err := svc.Send(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Send(ctx, p, in)
	if err != nil || !again.Replayed || again.MessageID != sent.MessageID || again.AttemptID != sent.AttemptID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	changed := in
	changed.Body = body[:len(body)-1] + ">"
	if _, err = svc.Send(ctx, p, changed); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	tooLarge := in
	tooLarge.ClientRequestID = "large-2"
	tooLarge.Body += "x"
	if _, err = svc.Send(ctx, p, tooLarge); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("signed body above 16000 bytes: %v", err)
	}
	var stored string
	if err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND id=$2`, w.tenant, sent.MessageID).Scan(&stored)
	}); err != nil {
		t.Fatal(err)
	}
	if stored != "Ana: "+body || len(stored) != 16000 {
		t.Fatalf("signed body size=%d", len(stored))
	}
	if n := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID); n != 1 {
		t.Fatalf("attempts=%d", n)
	}
}
