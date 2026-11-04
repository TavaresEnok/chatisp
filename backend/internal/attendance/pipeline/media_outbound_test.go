package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
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

func TestSendMediaConvertedAudioOverlapsInboundCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binDir, err := os.MkdirTemp(wd, "ffmpeg-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })
	script := filepath.Join(binDir, "ffmpeg")
	const stub = "#!/bin/sh\necho 'Duration: 00:00:01.00' >&2\nout=\nfor a in \"$@\"; do out=$a; done\ncase \"$out\" in *nulo) exit 0 ;; esac\nprintf 'OggS\\000lock-order' > \"$out\"\nexit 0\n"
	if err = os.WriteFile(script, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err = exec.LookPath("ffmpeg"); err != nil {
		t.Fatal(err)
	}

	phone := "5511966604491"
	st, principal, convID, _, _ := seedMediaConversation(t, phone, "wamid.CITADO.4491")
	webm := []byte("webm-lock-order")
	sourceID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "audio/webm", "gravacao.webm", webm)
	files := newPipelineMem()
	files.put(sourceID.String(), "v1", webm)
	limits := func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: 8 << 20, IncomingReserveBytes: 8 << 20, FileBytes: 8 << 20}, nil
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{
		"4491": {MIME: "image/jpeg", Body: jpeg},
	}}
	downloader := &mediaHandler{
		db: testStore, storage: files, fetcher: fake, materials: testConfigServer,
		limits: limits, repo: store.Repo{},
	}
	payload, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.media.4491", From: phone,
		Timestamp: "1700004491", Type: "image", ProfileName: "Cliente Mídia",
		Object: map[string]any{"id": "4491", "mime_type": "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	delivery := storeDelivery(t, st.appID, payload)
	if err = NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var inboundMessage uuid.UUID
	var inboundConv uuid.UUID
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, conversation_id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.media.4491'`, st.tenantID).Scan(&inboundMessage, &inboundConv)
	}); err != nil {
		t.Fatal(err)
	}
	if inboundConv != convID {
		t.Fatalf("imagem inbound em outra conversa %s", inboundConv)
	}

	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	blocker, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var held int64
	if err = blocker.QueryRow(ctx, `
		INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2)
		ON CONFLICT (tenant_id) DO UPDATE SET namespace=attachment_budgets.namespace
		RETURNING reserved_bytes`, st.tenantID, uuid.New()).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT tenant_id FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE`, st.tenantID); err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	waitLocks := func(fragment string, want int) {
		t.Helper()
		for {
			var n int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename='chatisp_app' AND wait_event_type='Lock' AND position($1 in query)>0`, fragment).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			select {
			case <-ctx.Done():
				var lines string
				_ = observer.QueryRow(context.Background(), `SELECT coalesce(string_agg(left(query, 180), E'\n'), '') FROM pg_stat_activity WHERE datname=current_database() AND usename='chatisp_app' AND state<>'idle'`).Scan(&lines)
				t.Fatalf("%s: %v\n%s", fragment, ctx.Err(), lines)
			case <-time.After(15 * time.Millisecond):
			}
		}
	}

	inboundDone := make(chan error, 1)
	go func() {
		inboundDone <- downloader.DownloadInboundMedia(ctx, st.tenantID, inboundMessage)
	}()
	waitLocks("attachment_budgets", 1)

	var version int64
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, convID).Scan(&version)
	}); err != nil {
		t.Fatal(err)
	}
	svc := testAttendanceSvc
	svc.Files = files
	svc.AttachmentLimits = limits
	sendDone := make(chan error, 1)
	go func() {
		_, err := svc.SendMedia(ctx, principal, service.MediaSendInput{
			InboxID: st.inboxID, ConversationID: convID, ExpectedVersion: version,
			ClientRequestID: "req-lock-4491", Body: "nota", AttachmentID: sourceID, At: time.Now(),
		})
		sendDone <- err
	}()
	waitLocks("send-media-budget", 1)
	// FOR SHARE allows KEY SHARE. FOR UPDATE does not. A tuple lock that is
	// only recorded on the row header is invisible in pg_locks until it conflicts.
	probe, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close(context.Background())
	if _, err = probe.Exec(ctx, `SET lock_timeout = '800ms'`); err != nil {
		t.Fatal(err)
	}
	var probed uuid.UUID
	err = probe.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE`, st.tenantID, convID).Scan(&probed)
	if err != nil {
		t.Fatalf("conversa exclusiva enquanto o envio espera o orçamento: %v", err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	inboundErr, sendErr := <-inboundDone, <-sendDone
	if deadlock(inboundErr) || deadlock(sendErr) {
		t.Fatalf("40P01 inbound=%v send=%v", inboundErr, sendErr)
	}
	if inboundErr != nil || sendErr != nil {
		t.Fatalf("inbound=%v send=%v", inboundErr, sendErr)
	}

	var reserved, oggBytes int64
	var attachments, attempts, inboundRows, oggRows int
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID).Scan(&attachments); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID).Scan(&attempts); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2 AND direction='incoming' AND state='validated'`, st.tenantID, inboundMessage).Scan(&inboundRows); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(expected_bytes),0) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND name='audio.ogg' AND media_type='application/ogg' AND direction='outgoing' AND state='validated'`, st.tenantID, convID).Scan(&oggRows, &oggBytes); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT object_key, object_version FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := map[string]struct{}{}
		for rows.Next() {
			var key, version string
			if err := rows.Scan(&key, &version); err != nil {
				return err
			}
			if _, ok := files.objs[key+"\x00"+version]; !ok {
				return errors.New("anexo sem objeto " + key)
			}
			seen[key+"\x00"+version] = struct{}{}
		}
		files.mu.Lock()
		defer files.mu.Unlock()
		if len(files.objs) != len(seen) {
			return errors.New("objeto órfão no storage")
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if attachments != 3 || attempts != 1 || inboundRows != 1 || oggRows != 1 {
		t.Fatalf("anexos=%d tentativas=%d entrada=%d ogg=%d", attachments, attempts, inboundRows, oggRows)
	}
	if reserved != int64(len(jpeg))+oggBytes || oggBytes == 0 || !bytes.HasPrefix(oggStored(files, st.tenantID, convID), []byte("OggS")) {
		t.Fatalf("reserva=%d jpeg=%d ogg=%d", reserved, len(jpeg), oggBytes)
	}
}

func TestSendMediaCitationOverlapsInboundCommit(t *testing.T) {
	overlapCitedSend(t, "5511966604492", "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}, "req-cite-4492", false)
}

func TestSendMediaConvertedCitationOverlapsInboundCommit(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binDir, err := os.MkdirTemp(wd, "ffmpeg-cite-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })
	script := filepath.Join(binDir, "ffmpeg")
	const stub = "#!/bin/sh\necho 'Duration: 00:00:01.00' >&2\nout=\nfor a in \"$@\"; do out=$a; done\ncase \"$out\" in *nulo) exit 0 ;; esac\nprintf 'OggS\\000cite-lock' > \"$out\"\nexit 0\n"
	if err = os.WriteFile(script, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err = exec.LookPath("ffmpeg"); err != nil {
		t.Fatal(err)
	}
	overlapCitedSend(t, "5511966604493", "audio/webm", "gravacao.webm", []byte("webm-cite-lock"), "req-cite-4493", true)
}

// overlapCitedSend blocks attachment_budgets until commitStored of the inbound
// message holds that row and SendMedia, quoting it, waits on send-media-reply.
// Image and document share the non-opus path. The converted flag expects one
// derived OGG and its bytes in the shared reservation.
func overlapCitedSend(t *testing.T, phone, mime, name string, body []byte, requestID string, converted bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, principal, convID, _, _ := seedMediaConversation(t, phone, "wamid.CITADO."+phone)
	sourceID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, mime, name, body)
	files := newPipelineMem()
	files.put(sourceID.String(), "v1", body)
	limits := func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: 8 << 20, IncomingReserveBytes: 8 << 20, FileBytes: 8 << 20}, nil
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{
		phone: {MIME: "image/jpeg", Body: jpeg},
	}}
	downloader := &mediaHandler{
		db: testStore, storage: files, fetcher: fake, materials: testConfigServer,
		limits: limits, repo: store.Repo{},
	}
	payload, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.media." + phone, From: phone,
		Timestamp: "1700004492", Type: "image", ProfileName: "Cliente Mídia",
		Object: map[string]any{"id": phone, "mime_type": "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	delivery := storeDelivery(t, st.appID, payload)
	if err = NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var inboundMessage uuid.UUID
	var inboundConv uuid.UUID
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, conversation_id FROM messages WHERE tenant_id=$1 AND external_message_id=$2`, st.tenantID, "wamid.media."+phone).Scan(&inboundMessage, &inboundConv)
	}); err != nil {
		t.Fatal(err)
	}
	if inboundConv != convID {
		t.Fatalf("imagem inbound em outra conversa %s", inboundConv)
	}

	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	blocker, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `
		INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2)
		ON CONFLICT (tenant_id) DO UPDATE SET namespace=attachment_budgets.namespace`, st.tenantID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT tenant_id FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE`, st.tenantID); err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	waitLocks := func(fragment string, want int) {
		t.Helper()
		for {
			var n int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename='chatisp_app' AND wait_event_type='Lock' AND position($1 in query)>0`, fragment).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			select {
			case <-ctx.Done():
				var lines string
				_ = observer.QueryRow(context.Background(), `SELECT coalesce(string_agg(left(query, 180), E'\n'), '') FROM pg_stat_activity WHERE datname=current_database() AND usename='chatisp_app' AND state<>'idle'`).Scan(&lines)
				t.Fatalf("%s: %v\n%s", fragment, ctx.Err(), lines)
			case <-time.After(15 * time.Millisecond):
			}
		}
	}

	inboundDone := make(chan error, 1)
	go func() {
		inboundDone <- downloader.DownloadInboundMedia(ctx, st.tenantID, inboundMessage)
	}()
	waitLocks("attachment_budgets", 1)

	var version int64
	if err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, convID).Scan(&version)
	}); err != nil {
		t.Fatal(err)
	}
	svc := testAttendanceSvc
	svc.Files = files
	svc.AttachmentLimits = limits
	sendDone := make(chan error, 1)
	go func() {
		_, err := svc.SendMedia(ctx, principal, service.MediaSendInput{
			InboxID: st.inboxID, ConversationID: convID, ExpectedVersion: version,
			ClientRequestID: requestID, Body: "nota", AttachmentID: sourceID, ReplyToMessageID: &inboundMessage, At: time.Now(),
		})
		sendDone <- err
	}()
	waitLocks("send-media-reply", 1)
	probe, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close(context.Background())
	if _, err = probe.Exec(ctx, `SET lock_timeout = '800ms'`); err != nil {
		t.Fatal(err)
	}
	var probed uuid.UUID
	err = probe.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE`, st.tenantID, convID).Scan(&probed)
	if err != nil {
		t.Fatalf("conversa exclusiva enquanto o envio espera a mensagem citada: %v", err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	inboundErr, sendErr := <-inboundDone, <-sendDone
	if deadlock(inboundErr) || deadlock(sendErr) {
		t.Fatalf("40P01 inbound=%v send=%v", inboundErr, sendErr)
	}
	if inboundErr != nil || sendErr != nil {
		t.Fatalf("inbound=%v send=%v", inboundErr, sendErr)
	}

	var reserved, oggBytes int64
	var attachments, attempts, inboundRows, oggRows int
	var linked uuid.UUID
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID).Scan(&attachments); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID).Scan(&attempts); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2 AND direction='incoming' AND state='validated'`, st.tenantID, inboundMessage).Scan(&inboundRows); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(expected_bytes),0) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND name='audio.ogg' AND media_type='application/ogg' AND direction='outgoing' AND state='validated'`, st.tenantID, convID).Scan(&oggRows, &oggBytes); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT m.reply_to_message_id
			  FROM messages m
			  JOIN outbound_attempts a ON a.tenant_id = m.tenant_id AND a.message_id = m.id
			 WHERE m.tenant_id = $1 AND a.conversation_id = $2`, st.tenantID, convID).Scan(&linked); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT object_key, object_version FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, convID)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := map[string]struct{}{}
		for rows.Next() {
			var key, version string
			if err := rows.Scan(&key, &version); err != nil {
				return err
			}
			if _, ok := files.objs[key+"\x00"+version]; !ok {
				return errors.New("anexo sem objeto " + key)
			}
			seen[key+"\x00"+version] = struct{}{}
		}
		files.mu.Lock()
		defer files.mu.Unlock()
		if len(files.objs) != len(seen) {
			return errors.New("objeto órfão no storage")
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if linked != inboundMessage {
		t.Fatalf("citação %s, inbound %s", linked, inboundMessage)
	}
	wantAttachments := 2
	if converted {
		wantAttachments = 3
	}
	if attachments != wantAttachments || attempts != 1 || inboundRows != 1 || oggRows != boolToInt(converted) {
		t.Fatalf("anexos=%d tentativas=%d entrada=%d ogg=%d", attachments, attempts, inboundRows, oggRows)
	}
	wantReserved := int64(len(jpeg))
	if converted {
		wantReserved += oggBytes
	}
	if reserved != wantReserved || (converted && (oggBytes == 0 || !bytes.HasPrefix(oggStored(files, st.tenantID, convID), []byte("OggS")))) {
		t.Fatalf("reserva=%d jpeg=%d ogg=%d", reserved, len(jpeg), oggBytes)
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func oggStored(files *pipelineMem, tenant, conv uuid.UUID) []byte {
	files.mu.Lock()
	defer files.mu.Unlock()
	for key, body := range files.objs {
		if len(body) >= 4 && string(body[:4]) == "OggS" {
			_ = key
			return body
		}
	}
	return nil
}

func deadlock(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "40P01"
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
