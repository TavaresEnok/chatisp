package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCanonicalPayloadLimit(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	now := time.Now().UTC()
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		opened, err := (Repo{}).Open(ctx, tx, OpenInput{TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, Period: "202610", OpenedAt: now})
		if err != nil {
			return err
		}
		in := OutboundInput{TenantID: w.tenant, ConversationID: opened.Conversation.ID, ConnectorID: w.connector, PrincipalID: w.membership, Operation: "send_text", ClientRequestID: "limit", Payload: bytes.Repeat([]byte{'x'}, maxCanonicalPayload+1), Body: "texto", At: now}
		if _, err = (Repo{}).EnqueueText(ctx, tx, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("oversized envelope: %v", err)
		}
		in.Payload = in.Payload[:maxCanonicalPayload]
		first, err := (Repo{}).EnqueueText(ctx, tx, in)
		if err != nil {
			return err
		}
		replay, err := (Repo{}).EnqueueText(ctx, tx, in)
		if err != nil {
			return err
		}
		if !replay.Replayed || first.MessageID != replay.MessageID || first.AttemptID != replay.AttemptID {
			t.Fatal("replay do limite divergente")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
