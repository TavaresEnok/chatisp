package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/sectors"
)

func TestOutboundMediaImageAndDocument(t *testing.T) {
	ctx := context.Background()
	st, principal, convID, inboundID, externalID := seedMediaConversation(t, "5511966604401", "wamid.CITADO.4401")
	imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})
	documentID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "application/pdf", "boleto.pdf", []byte("%PDF-1.4\nboleto\n"))
	reply := inboundID

	image := sendMedia(t, ctx, st, principal, convID, "req-img-4401", "legenda da foto", imageID, &reply)
	replay := sendMedia(t, ctx, st, principal, convID, "req-img-4401", "legenda da foto", imageID, &reply)
	if replay.AttemptID != image.AttemptID || replay.MessageID != image.MessageID {
		t.Fatalf("replay %#v %#v", replay, image)
	}
	document := sendMedia(t, ctx, st, principal, convID, "req-doc-4401", "legenda do boleto", documentID, &reply)
	if document.AttemptID == image.AttemptID {
		t.Fatal("documento reutilizou a tentativa da imagem")
	}
	if count := countAttempts(t, ctx, st.tenantID, convID); count != 2 {
		t.Fatalf("outbox %d", count)
	}

	fake := whatsapp.NewFake()
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: fake})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory)
	if err = handler.DispatchOutbound(ctx, st.tenantID, image.AttemptID); err != nil {
		t.Fatal(err)
	}
	expireInboxLease(t, ctx, st)
	if err = handler.DispatchOutbound(ctx, st.tenantID, document.AttemptID); err != nil {
		t.Fatal(err)
	}
	caps := fake.Captures()
	if len(caps) != 2 {
		t.Fatalf("capturas %d", len(caps))
	}
	assertCapture(t, caps[0], "image", imageID, externalID, "legenda da foto")
	assertCapture(t, caps[1], "document", documentID, externalID, "legenda do boleto")
	assertAcceptedMessage(t, ctx, st.tenantID, image.MessageID, caps[0].Result.ProviderMessageID)
	assertAcceptedMessage(t, ctx, st.tenantID, document.MessageID, caps[1].Result.ProviderMessageID)
	if count := countAttempts(t, ctx, st.tenantID, convID); count != 2 {
		t.Fatalf("outbox após despacho %d", count)
	}
}

func TestOutboundMediaRefusedBeforeDriver(t *testing.T) {
	t.Run("janela", func(t *testing.T) {
		ctx := context.Background()
		st, principal, convID, inboundID, _ := seedMediaConversation(t, "5511966604402", "wamid.CITADO.4402")
		imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		sent := sendMedia(t, ctx, st, principal, convID, "req-janela", "foto", imageID, &inboundID)
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours' WHERE tenant_id=$1 AND direction='inbound'`, st.tenantID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		assertMediaNotDispatched(t, ctx, st, sent.AttemptID, "cancelled", "window_closed")
	})
	t.Run("bloqueio", func(t *testing.T) {
		ctx := context.Background()
		st, principal, convID, inboundID, _ := seedMediaConversation(t, "5511966604403", "wamid.CITADO.4403")
		imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		sent := sendMedia(t, ctx, st, principal, convID, "req-bloqueio", "foto", imageID, &inboundID)
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE contacts SET blocked=true, block_reason='abuso' WHERE tenant_id=$1`, st.tenantID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		assertMediaNotDispatched(t, ctx, st, sent.AttemptID, "failed", "despacho_invalidado")
	})
	t.Run("consentimento", func(t *testing.T) {
		ctx := context.Background()
		st, principal, convID, inboundID, _ := seedMediaConversation(t, "5511966604404", "wamid.CITADO.4404")
		var contactID uuid.UUID
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT contact_id FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, convID).Scan(&contactID)
		}); err != nil {
			t.Fatal(err)
		}
		policy := channelpolicy.Service{DB: testStore}
		scope := channelpolicy.Scope{InboxID: st.inboxID, ContactID: contactID, Purpose: "atendimento"}
		actor := sectors.Principal{TenantID: st.tenantID, UserID: st.userID, MembershipID: st.memberID, Role: "admin"}
		if _, err := policy.RecordConsent(ctx, actor, channelpolicy.ConsentInput{Scope: scope, Source: "painel", Evidence: "aceite do contato"}); err != nil {
			t.Fatal(err)
		}
		imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		sent := sendMedia(t, ctx, st, principal, convID, "req-consent", "foto", imageID, &inboundID)
		if _, err := policy.RecordConsent(ctx, actor, channelpolicy.ConsentInput{Scope: scope, Source: "painel", Evidence: "revogação do contato", Revoke: true}); err != nil {
			t.Fatal(err)
		}
		assertMediaNotDispatched(t, ctx, st, sent.AttemptID, "cancelled", "consent_revoked")
	})
}

func seedMediaConversation(t *testing.T, phone, externalID string) (seededTenant, service.Principal, uuid.UUID, uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-"+phone, "waba-media-"+phone, phone, "tok-media-"+phone, "secret-media-"+phone)
	principal := service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}
	contactID := uuid.New()
	identityID := uuid.New()
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Mídia')`, contactID, st.tenantID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`,
			identityID, st.tenantID, contactID, st.connectorID, phone)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	received, err := testAttendanceSvc.ReceiveText(ctx, principal, service.ReceiveInput{
		InboxID: st.inboxID, ContactID: contactID, IdentityID: identityID, ConnectorID: st.connectorID,
		ExternalMessageID: externalID, Body: "olá", Period: time.Now().UTC().Format("200601"),
		TrustedAt: time.Now().Add(-time.Minute), OpenedAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testAttendanceSvc.Assume(ctx, principal, st.inboxID, received.Conversation.ID, received.Conversation.Version, time.Now()); err != nil {
		t.Fatal(err)
	}
	return st, principal, received.Conversation.ID, received.MessageID, externalID
}

func insertOutgoingAttachment(t *testing.T, ctx context.Context, tenant, conv uuid.UUID, mime, name string, body []byte) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sum := sha256.Sum256(body)
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO attachments (
				tenant_id, id, conversation_id, request_id, direction, name, expected_bytes,
				object_key, object_version, state, media_type, sha256, expires_at
			) VALUES ($1,$2,$3,$4,'outgoing',$5,$6,$7,'v1','validated',$8,$9, clock_timestamp() + interval '1 day')`,
			tenant, id, conv, uuid.New(), name, int64(len(body)), id.String(), mime, hex.EncodeToString(sum[:]))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sendMedia(t *testing.T, ctx context.Context, st seededTenant, principal service.Principal, conv uuid.UUID, requestID, caption string, attachment uuid.UUID, reply *uuid.UUID) service.SendResult {
	t.Helper()
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&version)
	}); err != nil {
		t.Fatal(err)
	}
	sent, err := testAttendanceSvc.SendMedia(ctx, principal, service.MediaSendInput{
		InboxID: st.inboxID, ConversationID: conv, ExpectedVersion: version,
		ClientRequestID: requestID, Body: caption, AttachmentID: attachment, ReplyToMessageID: reply, At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sent
}

func countAttempts(t *testing.T, ctx context.Context, tenant, conv uuid.UUID) int {
	t.Helper()
	var n int
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conv).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func assertCapture(t *testing.T, cap whatsapp.Capture, kind string, attachment uuid.UUID, reply, caption string) {
	t.Helper()
	if cap.MediaKind != kind || cap.AttachmentID != attachment.String() || cap.ReplyToProviderID != reply || cap.Caption != caption {
		t.Fatalf("captura %#v", cap)
	}
}

func assertAcceptedMessage(t *testing.T, ctx context.Context, tenant, message uuid.UUID, providerID string) {
	t.Helper()
	var state, external string
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT local_state, external_message_id FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, message).Scan(&state, &external)
	})
	if err != nil || state != "accepted" || external != providerID || providerID == "" {
		t.Fatalf("mensagem %s %s %v", state, external, err)
	}
}

func expireInboxLease(t *testing.T, ctx context.Context, st seededTenant) {
	t.Helper()
	err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE inbox_send_leases SET expires_at = clock_timestamp() - interval '1 second' WHERE tenant_id=$1 AND inbox_id=$2`, st.tenantID, st.inboxID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertMediaNotDispatched(t *testing.T, ctx context.Context, st seededTenant, attempt uuid.UUID, wantState, wantCode string) {
	t.Helper()
	fake := whatsapp.NewFake()
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: fake})
	if err != nil {
		t.Fatal(err)
	}
	if err = NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attempt); err != nil {
		t.Fatal(err)
	}
	if len(fake.Captures()) != 0 {
		t.Fatalf("driver chamado: %#v", fake.Captures())
	}
	state, code := readAttempt(t, ctx, st.tenantID, attempt)
	if state != wantState || code != wantCode {
		t.Fatalf("tentativa %s/%s, esperado %s/%s", state, code, wantState, wantCode)
	}
}
