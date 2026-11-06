package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/retention"
)

type memStorage struct {
	mu        sync.Mutex
	body      map[string][]byte
	ver       map[string]string
	seq       int
	deleteErr error
}

func TestInboundMediaFailureWaitsForSuspensionToEnd(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "suspend-media-app", "suspend-media-waba", "5511999990197", "suspend-media-token", "suspend-media-secret")
	files := &memStorage{}
	fetcher := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{}}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.suspend.media", From: "551198887719", Timestamp: "1700000700", Type: "image", Object: map[string]any{"id": "missing", "mime_type": "image/jpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, body)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var message uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.suspend.media'`, st.tenantID).Scan(&message)
	}); err != nil {
		t.Fatal(err)
	}
	downloader := mediaWorkers(t, files, fetcher)
	downloader.beforeFetch = func() { setSuspension(t, st, "suspended") }
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, message); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("download durante suspensão: %v", err)
	}
	var status string
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT content_context->>'media_status' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, message).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "reserved" {
		t.Fatalf("mídia alterada durante suspensão: %s", status)
	}
	setSuspension(t, st, "active")
	downloader.beforeFetch = nil
	downloader.reserveFor = -1
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, message); err != nil {
		t.Fatalf("retomada após suspensão: %v", err)
	}
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT content_context->>'media_status' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, message).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("falha sem objeto pendente não foi concluída: %s", status)
	}
}

func TestInboundMediaPrivacyDuringCreate(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-privacy", "waba-media-privacy", "5511999990141", "token-media-privacy", "secret-media-privacy")
	files := &memStorage{}
	fetcher := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{"901": {MIME: "image/jpeg", Body: []byte{0xff, 0xd8, 0xff, 0}}}}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.privacy.media", From: "551198887741", Timestamp: "1700000700", Type: "image", Object: map[string]any{"id": "901", "mime_type": "image/jpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, body)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var message, contact uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT m.id,c.contact_id FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id WHERE m.tenant_id=$1 AND m.external_message_id='wamid.privacy.media'`, st.tenantID).Scan(&message, &contact)
	}); err != nil {
		t.Fatal(err)
	}
	downloader := mediaWorkers(t, files, fetcher)
	erased := make(chan error, 1)
	downloader.afterCreate = func() error {
		go func() {
			_, err := (retention.Service{DB: testStore, Objects: files, Keys: testRing}).DeletePrivacy(ctx, retention.Principal{TenantID: st.tenantID, UserID: st.userID, MembershipID: st.memberID, Role: "admin"}, contact)
			erased <- err
		}()
		return nil
	}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, message); err != nil {
		t.Fatalf("download after erasure: %v", err)
	}
	if err := <-erased; err != nil {
		t.Fatal(err)
	}
	if files.objects() != 0 {
		t.Fatalf("erased media remains in storage: %d", files.objects())
	}
}

func (m *memStorage) Create(_ context.Context, key string, r io.ReadSeeker, n int64) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.body == nil {
		m.body = map[string][]byte{}
		m.ver = map[string]string{}
	}
	if _, exists := m.body[key]; exists {
		return "", errors.New("objeto já existe")
	}
	m.seq++
	version := "v" + strconv.Itoa(m.seq)
	m.body[key] = buf
	m.ver[key] = version
	return version, nil
}
func (m *memStorage) Read(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (m *memStorage) Sign(context.Context, string, string, string, time.Duration) (string, error) {
	return "", nil
}
func (m *memStorage) Delete(_ context.Context, key, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if version != "" && m.ver[key] != version {
		return nil
	}
	delete(m.body, key)
	delete(m.ver, key)
	return nil
}
func (m *memStorage) objects() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.body)
}

func mediaWorkers(t *testing.T, files *memStorage, fetcher whatsapp.MediaFetcher) *mediaHandler {
	t.Helper()
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: whatsapp.NewFake()})
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := NewWorkers(WorkersConfig{
		DB:                 testStore,
		IngressPool:        testPools.Ingress,
		JobsClient:         testJobs,
		AttendanceService:  testAttendanceSvc,
		MaterialResolver:   testConfigServer,
		CredentialReporter: testConfigServer,
		DriverFactory:      factory,
		MediaStorage:       files,
		MediaFetcher:       fetcher,
		Simulation:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := handlers.Media.(*mediaHandler)
	if !ok || handler.storage != files || handler.fetcher != fetcher {
		t.Fatal("NewWorkers não entregou o storage e o fetcher da composição")
	}
	return handler
}

func TestInboundImageWebhookDownloadIdempotent(t *testing.T) {
	ctx := context.Background()
	appID := "app-media-in"
	st := seedTenantWithMeta(t, appID, "waba-media-in", "5511999990101", "tok-media", "secret-media")
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{
		"100": {MIME: "image/jpeg", Body: jpeg},
	}}
	files := &memStorage{}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, files, fake)
	ingress := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer)

	textBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.media.text", From: "551198887701",
		Timestamp: "1700000200", Type: "text", Object: map[string]string{"body": "antes"}, ProfileName: "Ana",
	})
	if err != nil {
		t.Fatal(err)
	}
	textDelivery := storeDelivery(t, appID, textBody)
	if err := ingress.ExpandIngress(ctx, textDelivery); err != nil {
		t.Fatalf("expand texto: %v", err)
	}
	applyDelivery(t, inbound, st.tenantID, textDelivery)

	imageBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.media.image", From: "551198887701",
		Timestamp: "1700000201", Type: "image", ContextID: "wamid.media.text", ProfileName: "Ana",
		Object: map[string]any{"id": "100", "mime_type": "image/jpeg", "caption": "foto da fachada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	imageDelivery := storeDelivery(t, appID, imageBody)
	if err := ingress.ExpandIngress(ctx, imageDelivery); err != nil {
		t.Fatalf("expand imagem: %v", err)
	}
	applyDelivery(t, inbound, st.tenantID, imageDelivery)

	var messageID, conversationID, replyTo uuid.UUID
	var kind, status string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, conversation_id, content_kind, content_context->>'media_status', reply_to_message_id
			  FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.media.image'
		`, st.tenantID).Scan(&messageID, &conversationID, &kind, &status, &replyTo)
	})
	if err != nil || kind != "image" || status != "pending" || replyTo == uuid.Nil {
		t.Fatalf("mensagem de imagem: %v kind=%s status=%s reply=%s", err, kind, status, replyTo)
	}
	var eventReply, eventQuote, eventContext string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT payload->>'reply_to_message_id', payload->>'reply_to_text', payload->'content'->>'context_id'
			  FROM realtime_events
			 WHERE tenant_id=$1 AND event_type='conversation.message' AND payload->>'message_id'=$2
		`, st.tenantID, messageID).Scan(&eventReply, &eventQuote, &eventContext)
	})
	if err != nil || eventReply != replyTo.String() || eventQuote != "antes" || eventContext != "wamid.media.text" {
		t.Fatalf("evento de citação: reply=%s texto=%s contexto=%s err=%v", eventReply, eventQuote, eventContext, err)
	}
	var jobsN int
	if err := testJobsPool.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE kind=$1 AND args->>'message_id'=$2`, "chatisp.inbound.media.v1", messageID.String()).Scan(&jobsN); err != nil || jobsN != 1 {
		t.Fatalf("job de download: %d %v", jobsN, err)
	}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatalf("download: %v", err)
	}
	if fake.Calls != 1 {
		t.Fatalf("chamadas do fake: %d", fake.Calls)
	}
	var attachments, mediaRows int
	var mediaType, objectKey string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND state='validated' AND media_type='image/jpeg'`, st.tenantID, conversationID).Scan(&attachments); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*), max(a.media_type), max(a.object_key) FROM media_items mi JOIN attachments a ON a.tenant_id=mi.tenant_id AND a.id=mi.attachment_id WHERE mi.tenant_id=$1 AND mi.message_id=$2`, st.tenantID, messageID).Scan(&mediaRows, &mediaType, &objectKey)
	})
	if err != nil || attachments != 1 || mediaRows != 1 || mediaType != "image/jpeg" || files.body[objectKey] == nil {
		t.Fatalf("anexo: att=%d media=%d type=%s key=%s err=%v", attachments, mediaRows, mediaType, objectKey, err)
	}
	parts := strings.Split(objectKey, "/")
	if len(parts) != 2 {
		t.Fatalf("object key not namespace/object: %q", objectKey)
	}
	if _, err := uuid.Parse(parts[0]); err != nil {
		t.Fatalf("invalid namespace: %q", objectKey)
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		t.Fatalf("invalid object id: %q", objectKey)
	}

	history, err := testAttendanceSvc.History(ctx, service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}, st.inboxID, conversationID)
	if err != nil {
		t.Fatalf("histórico: %v", err)
	}
	var saw bool
	for _, message := range history.Messages {
		if message.ID == messageID {
			saw = message.Attachment != nil && message.Attachment.MediaType == "image/jpeg" && message.ContentKind == "image" && bytes.Contains(message.Content, []byte("foto da fachada"))
		}
	}
	if !saw {
		t.Fatalf("histórico sem anexo: %+v", history.Messages)
	}
	var events int
	_ = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND event_type='conversation.inbound_media' AND payload->>'attachment_id'<>''`, st.tenantID).Scan(&events)
	})
	if events < 1 {
		t.Fatal("evento de mídia ausente")
	}

	if err := ingress.ExpandIngress(ctx, imageDelivery); err != nil {
		t.Fatalf("replay expand: %v", err)
	}
	applyDelivery(t, inbound, st.tenantID, imageDelivery)
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatalf("replay download: %v", err)
	}
	var messages int
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.media.image'`, st.tenantID).Scan(&messages); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, st.tenantID, conversationID).Scan(&attachments)
	})
	if err != nil || messages != 1 || attachments != 1 || fake.Calls != 1 {
		t.Fatalf("idempotência messages=%d attachments=%d calls=%d err=%v", messages, attachments, fake.Calls, err)
	}

	other := seedTenantWithMeta(t, "app-media-other", "waba-media-other", "5511999990102", "tok-other", "secret-other")
	var leakedMessages, leakedFiles int
	err = testStore.WithTenantTx(ctx, other.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1`, messageID).Scan(&leakedMessages); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE conversation_id=$1`, conversationID).Scan(&leakedFiles)
	})
	if err != nil || leakedMessages != 0 || leakedFiles != 0 {
		t.Fatalf("RLS: messages=%d files=%d err=%v", leakedMessages, leakedFiles, err)
	}
}

func TestInboundMediaFailuresStayOnTheMessage(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-fail", "waba-media-fail", "5511999990103", "tok-fail", "secret-fail")
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	big := make([]byte, 5<<20+1)
	copy(big, jpeg)
	fake := &whatsapp.FakeFetcher{
		Objects: map[string]whatsapp.MediaBlob{
			"200": {Body: jpeg},
			"201": {Body: big},
		},
		Errors: map[string]error{"202": whatsapp.ErrMediaDisabled},
	}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, &memStorage{}, fake)
	cases := []struct {
		id, mediaID, mime, want string
	}{
		{"wamid.fail.mime", "200", "image/png", "mime_divergente"},
		{"wamid.fail.size", "201", "image/jpeg", "tamanho_excedido"},
		{"wamid.fail.fetch", "202", "image/jpeg", "download_falhou"},
	}
	for _, tc := range cases {
		body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
			WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: tc.id, From: "551198887702",
			Timestamp: "1700000300", Type: "image", Object: map[string]any{"id": tc.mediaID, "mime_type": tc.mime},
		})
		if err != nil {
			t.Fatal(err)
		}
		delivery := storeDelivery(t, st.appID, body)
		if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
			t.Fatalf("expand %s: %v", tc.id, err)
		}
		applyDelivery(t, inbound, st.tenantID, delivery)
		var messageID uuid.UUID
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id=$2`, st.tenantID, tc.id).Scan(&messageID)
		}); err != nil {
			t.Fatalf("ingresso %s: %v", tc.id, err)
		}
		if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
			t.Fatalf("job %s derrubou ingresso: %v", tc.id, err)
		}
		var got string
		var files int
		err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT content_context->>'media_error' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, messageID).Scan(&got); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT count(*) FROM media_items WHERE tenant_id=$1 AND message_id=$2`, st.tenantID, messageID).Scan(&files)
		})
		if err != nil || got != tc.want || files != 0 {
			t.Fatalf("%s: erro=%s files=%d err=%v", tc.id, got, files, err)
		}
	}
}

func TestInboundReactionLocationAndVCard(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-struct", "waba-media-struct", "5511999990104", "tok-struct", "secret-struct")
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	from := "551198887703"
	textBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.struct.text", From: from,
		Timestamp: "1700000400", Type: "text", Object: map[string]string{"body": "olá"}, ProfileName: "Ana",
	})
	if err != nil {
		t.Fatal(err)
	}
	textDelivery := storeDelivery(t, st.appID, textBody)
	ingress := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer)
	if err := ingress.ExpandIngress(ctx, textDelivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, textDelivery)
	var conversation uuid.UUID
	var beforeMessages int
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1`, st.tenantID).Scan(&conversation); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET status='closed' WHERE tenant_id=$1 AND id=$2`, st.tenantID, conversation); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, st.tenantID).Scan(&beforeMessages)
	})
	if err != nil {
		t.Fatal(err)
	}
	reactionBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.struct.react", From: from,
		Timestamp: "1700000401", Type: "reaction", Object: map[string]string{"message_id": "wamid.struct.text", "emoji": "👍"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reactionDelivery := storeDelivery(t, st.appID, reactionBody)
	if err := ingress.ExpandIngress(ctx, reactionDelivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, reactionDelivery)
	var status string
	var messages, conversations, reactions int
	var emoji string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conversation).Scan(&status); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, st.tenantID).Scan(&messages); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE tenant_id=$1`, st.tenantID).Scan(&conversations); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*), max(emoji) FROM message_reactions WHERE tenant_id=$1 AND target_external_id='wamid.struct.text'`, st.tenantID).Scan(&reactions, &emoji)
	})
	if err != nil || status != "closed" || messages != beforeMessages || conversations != 1 || reactions != 1 || emoji != "👍" {
		t.Fatalf("reação status=%s messages=%d/%d conv=%d reactions=%d emoji=%s err=%v", status, messages, beforeMessages, conversations, reactions, emoji, err)
	}
	send := func(id, stamp, emoji string) {
		t.Helper()
		body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
			WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: id, From: from,
			Timestamp: stamp, Type: "reaction", Object: map[string]string{"message_id": "wamid.struct.text", "emoji": emoji},
		})
		if err != nil {
			t.Fatal(err)
		}
		delivery := storeDelivery(t, st.appID, body)
		if err := ingress.ExpandIngress(ctx, delivery); err != nil {
			t.Fatal(err)
		}
		applyDelivery(t, inbound, st.tenantID, delivery)
	}
	heartBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.struct.react2", From: from,
		Timestamp: "1700000402", Type: "reaction", Object: map[string]string{"message_id": "wamid.struct.text", "emoji": "❤️"},
	})
	if err != nil {
		t.Fatal(err)
	}
	heartDelivery := storeDelivery(t, st.appID, heartBody)
	if err := ingress.ExpandIngress(ctx, heartDelivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, heartDelivery)
	if err := ingress.ExpandIngress(ctx, heartDelivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, heartDelivery)
	send("wamid.struct.react-old", "1700000401", "👍")
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conversation).Scan(&status); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, st.tenantID).Scan(&messages); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*), max(emoji) FROM message_reactions WHERE tenant_id=$1 AND target_external_id='wamid.struct.text' AND actor_external_id=$2`, st.tenantID, from).Scan(&reactions, &emoji)
	})
	if err != nil || status != "closed" || messages != beforeMessages || reactions != 1 || emoji != "❤️" {
		t.Fatalf("troca de reação: status=%s messages=%d reactions=%d emoji=%s err=%v", status, messages, reactions, emoji, err)
	}
	send("wamid.struct.react3", "1700000403", "")
	history, err := testAttendanceSvc.History(ctx, service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}, st.inboxID, conversation)
	if err != nil {
		t.Fatal(err)
	}
	var visible int
	for _, message := range history.Messages {
		visible += len(message.Reactions)
	}
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conversation).Scan(&status); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, st.tenantID).Scan(&messages); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*), max(emoji) FROM message_reactions WHERE tenant_id=$1 AND actor_external_id=$2`, st.tenantID, from).Scan(&reactions, &emoji)
	})
	if err != nil || status != "closed" || messages != beforeMessages || reactions != 1 || emoji != "" || visible != 0 {
		t.Fatalf("remoção: status=%s messages=%d reactions=%d emoji=%q visíveis=%d err=%v", status, messages, reactions, emoji, visible, err)
	}

	locBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.struct.loc", From: from,
		Timestamp: "1700000402", Type: "location",
		Object: map[string]any{"latitude": -23.5, "longitude": -46.6, "name": "Sede", "address": "Rua 1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cardBody, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.struct.card", From: from,
		Timestamp: "1700000403", Type: "contacts",
		Object: []any{map[string]any{
			"name":   map[string]string{"formatted_name": "Bia"},
			"phones": []any{map[string]string{"phone": "+5511988887777"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{locBody, cardBody} {
		delivery := storeDelivery(t, st.appID, body)
		if err := ingress.ExpandIngress(ctx, delivery); err != nil {
			t.Fatal(err)
		}
		applyDelivery(t, inbound, st.tenantID, delivery)
	}
	var locName, vcard string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT content_context->>'location_name' FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.struct.loc'`, st.tenantID).Scan(&locName); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT content_context->'contacts'->0->>'vcard' FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.struct.card'`, st.tenantID).Scan(&vcard)
	})
	if err != nil || locName != "Sede" || !bytes.Contains([]byte(vcard), []byte("BEGIN:VCARD")) || !bytes.Contains([]byte(vcard), []byte("+5511988887777")) {
		t.Fatalf("estruturado: %s %s %v", locName, vcard, err)
	}
}

func TestInboundTextDoesNotCarryStructure(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-inject", "waba-media-inject", "5511999990105", "tok-inject", "secret-inject")
	raw, _ := json.Marshal(map[string]string{"type": "reaction", "reaction_target": "wamid.alvo", "emoji": "👍"})
	bodyText := "\x1echatisp-in1:" + base64.RawURLEncoding.EncodeToString(raw)
	envelope, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.inject.text", From: "551198887704",
		Timestamp: "1700000500", Type: "text", Object: map[string]string{"body": bodyText}, ProfileName: "Ana",
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, envelope)
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var stored, kind string
	var reactions, jobsN int
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT body, content_kind FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.inject.text'`, st.tenantID).Scan(&stored, &kind); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM message_reactions WHERE tenant_id=$1`, st.tenantID).Scan(&reactions); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND content_context->>'media_status'='pending'`, st.tenantID).Scan(&jobsN)
	})
	if err != nil || stored != bodyText || kind != "text" || reactions != 0 || jobsN != 0 {
		t.Fatalf("texto injetado: body=%q kind=%s reactions=%d pending=%d err=%v", stored, kind, reactions, jobsN, err)
	}
}

func TestInboundDocumentTextDownload(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-doc", "waba-media-doc", "5511999990106", "tok-doc", "secret-doc")
	textDoc := []byte("Contrato de servico\n")
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{
		"301": {MIME: "text/plain", Body: textDoc},
	}}
	files := &memStorage{}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, files, fake)
	body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.doc.text", From: "551198887705",
		Timestamp: "1700000600", Type: "document", ProfileName: "Ana",
		Object: map[string]any{"id": "301", "mime_type": "text/plain", "filename": "contrato.txt", "caption": "contrato"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, body)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var messageID uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.doc.text'`, st.tenantID).Scan(&messageID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatal(err)
	}
	var mediaType, filename string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT a.media_type, a.name
			  FROM media_items mi
			  JOIN attachments a ON a.tenant_id=mi.tenant_id AND a.id=mi.attachment_id
			 WHERE mi.tenant_id=$1 AND mi.message_id=$2 AND a.state='validated'
		`, st.tenantID, messageID).Scan(&mediaType, &filename)
	})
	if err != nil || mediaType != "text/plain" || filename != "contrato.txt" || files.objects() != 1 {
		t.Fatalf("documento: type=%s name=%s objects=%d err=%v", mediaType, filename, files.objects(), err)
	}
}

func TestInboundMediaCreateIsRecoverable(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-crash", "waba-media-crash", "5511999990107", "tok-crash-media", "secret-crash-media")
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{"401": {MIME: "image/jpeg", Body: jpeg}}}
	files := &memStorage{}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, files, fake)
	body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.crash.image", From: "551198887706",
		Timestamp: "1700000700", Type: "image", Object: map[string]any{"id": "401", "mime_type": "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, body)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var messageID uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.crash.image'`, st.tenantID).Scan(&messageID)
	}); err != nil {
		t.Fatal(err)
	}
	downloader.afterCreate = func() error { return errors.New("queda antes do commit") }
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err == nil {
		t.Fatal("a queda antes do commit deveria interromper a tentativa")
	}
	var status string
	var attachments int
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT content_context->>'media_status' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, messageID).Scan(&status); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2`, st.tenantID, messageID).Scan(&attachments)
	})
	if err != nil || status != "reserved" || attachments != 1 || files.objects() != 1 {
		t.Fatalf("reserva após queda: status=%s attachments=%d objects=%d err=%v", status, attachments, files.objects(), err)
	}
	downloader.afterCreate = nil
	downloader.reserveFor = -1
	legacyKey := "in/" + st.tenantID.String() + "/" + messageID.String()
	if _, err := files.Create(ctx, legacyKey, bytes.NewReader(jpeg), int64(len(jpeg))); err != nil {
		t.Fatal(err)
	}
	files.deleteErr = errors.New("limpeza indisponível")
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err == nil || files.objects() != 2 {
		t.Fatalf("falha de limpeza deve preservar objeto e reserva: %v", err)
	}
	files.deleteErr = nil
	delete(fake.Objects, "401")
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err == nil || files.objects() != 1 {
		t.Fatalf("failed retry must preserve tracked object: %v", err)
	}
	fake.Objects["401"] = whatsapp.MediaBlob{MIME: "image/jpeg", Body: append(append([]byte{}, jpeg...), 1)}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err == nil || files.objects() != 1 {
		t.Fatalf("changed size must preserve tracked object: %v", err)
	}
	fake.Objects["401"] = whatsapp.MediaBlob{MIME: "image/jpeg", Body: jpeg}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT content_context->>'media_status' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, messageID).Scan(&status); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2 AND state='validated'`, st.tenantID, messageID).Scan(&attachments)
	})
	if err != nil || status != "stored" || attachments != 1 || files.objects() != 1 {
		t.Fatalf("recuperação: status=%s attachments=%d objects=%d err=%v", status, attachments, files.objects(), err)
	}

	overlap := mediaWorkers(t, files, fake)
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	overlap.beforeFetch = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	body2, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.crash.overlap", From: "551198887706",
		Timestamp: "1700000701", Type: "image", Object: map[string]any{"id": "401", "mime_type": "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery2 := storeDelivery(t, st.appID, body2)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery2); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery2)
	var secondID uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.crash.overlap'`, st.tenantID).Scan(&secondID)
	}); err != nil {
		t.Fatal(err)
	}
	firstErr := make(chan error, 1)
	go func() { firstErr <- overlap.DownloadInboundMedia(ctx, st.tenantID, secondID) }()
	select {
	case <-entered:
	case err := <-firstErr:
		t.Fatalf("primeira execução terminou antes da reserva: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("reserva não começou")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- overlap.DownloadInboundMedia(ctx, st.tenantID, secondID) }()
	var snooze *river.JobSnoozeError
	select {
	case err := <-secondDone:
		if !errors.As(err, &snooze) {
			t.Fatalf("execução sobreposta: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execução sobreposta não respeitou a reserva")
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("primeira execução: %v", err)
	}
	if err := overlap.DownloadInboundMedia(ctx, st.tenantID, secondID); err != nil {
		t.Fatalf("segunda após sucesso: %v", err)
	}
	var failed string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT content_context->>'media_status' FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, secondID).Scan(&status); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(content_context->>'media_error', '') FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, secondID).Scan(&failed); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2`, st.tenantID, secondID).Scan(&attachments)
	})
	if err != nil || status != "stored" || failed != "" || attachments != 1 {
		t.Fatalf("sobreposição: status=%s erro=%s attachments=%d err=%v", status, failed, attachments, err)
	}
}

func TestInboundAudioMP4Download(t *testing.T) {
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-media-aac", "waba-media-aac", "5511999990108", "tok-aac", "secret-aac")
	bodyBytes := audioContainerMP4()
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{
		"501": {MIME: "audio/mp4", Body: bodyBytes},
	}}
	files := &memStorage{}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, files, fake)
	body, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: "wamid.media.aac", From: "551198887708",
		Timestamp: "1700000800", Type: "audio", Object: map[string]any{"id": "501", "mime_type": "audio/mp4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, body)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var messageID uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id='wamid.media.aac'`, st.tenantID).Scan(&messageID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatalf("download aac: %v", err)
	}
	var mediaType, status string
	err = testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT a.media_type, m.content_context->>'media_status'
			  FROM messages m
			  JOIN attachments a ON a.tenant_id=m.tenant_id AND a.request_id=m.id
			 WHERE m.tenant_id=$1 AND m.id=$2
		`, st.tenantID, messageID).Scan(&mediaType, &status)
	})
	if err != nil || mediaType != "audio/mp4" || status != "stored" || files.objects() != 1 {
		t.Fatalf("aac armazenado: type=%s status=%s objects=%d err=%v", mediaType, status, files.objects(), err)
	}
}

func TestInboundAttachmentBudget(t *testing.T) {
	ctx := context.Background()
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}

	t.Run("ocupado", func(t *testing.T) {
		st := seedTenantWithMeta(t, "app-budget-full", "waba-budget-full", "5511999990109", "tok-budget-full", "secret-budget-full")
		setAttachmentLimits(t, st.tenantID, `{"attachment_plan_bytes":8,"attachment_reserve_bytes":0,"attachment_file_bytes":100}`)
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO attachment_budgets(tenant_id,namespace,reserved_bytes) VALUES($1,$2,8)`, st.tenantID, uuid.New())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		messageID, files := downloadImage(t, st, "wamid.budget.full", "551198887709", "601", jpeg)
		assertMediaFailed(t, st.tenantID, messageID, "limite_excedido", files)
		var reserved int64
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved)
		}); err != nil || reserved != 8 {
			t.Fatalf("reserva ocupada alterada: %d %v", reserved, err)
		}
	})

	t.Run("arquivo", func(t *testing.T) {
		st := seedTenantWithMeta(t, "app-budget-file", "waba-budget-file", "5511999990110", "tok-budget-file", "secret-budget-file")
		setAttachmentLimits(t, st.tenantID, `{"attachment_plan_bytes":1000,"attachment_reserve_bytes":0,"attachment_file_bytes":3}`)
		messageID, files := downloadImage(t, st, "wamid.budget.file", "551198887710", "602", jpeg)
		assertMediaFailed(t, st.tenantID, messageID, "limite_excedido", files)
		var budgets int
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&budgets)
		}); err != nil || budgets != 0 {
			t.Fatalf("orçamento criado na recusa do arquivo: %d %v", budgets, err)
		}
	})

	t.Run("teto do plano", func(t *testing.T) {
		st := seedTenantWithMeta(t, "app-budget-plan", "waba-budget-plan", "5511999990111", "tok-budget-plan", "secret-budget-plan")
		setAttachmentLimits(t, st.tenantID, `{"attachment_plan_bytes":100000,"attachment_reserve_bytes":0,"attachment_file_bytes":100000,"max_storage_bytes":1}`)
		messageID, files := downloadImage(t, st, "wamid.budget.plan", "551198887711", "603", jpeg)
		assertMediaFailed(t, st.tenantID, messageID, "limite_excedido", files)
		if err := downloadAgain(t, st, messageID, files); err != nil {
			t.Fatalf("replay da recusa: %v", err)
		}
		assertMediaFailed(t, st.tenantID, messageID, "limite_excedido", files)
	})

	t.Run("conjunto", func(t *testing.T) {
		st := seedTenantWithMeta(t, "app-budget-joint", "waba-budget-joint", "5511999990112", "tok-budget-joint", "secret-budget-joint")
		setAttachmentLimits(t, st.tenantID, `{"attachment_plan_bytes":10,"attachment_reserve_bytes":6,"attachment_file_bytes":10}`)
		messageID, files := downloadImage(t, st, "wamid.budget.joint", "551198887712", "604", jpeg)
		var conversation uuid.UUID
		var reserved int64
		var stored int
		err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE tenant_id=$1 AND id=$2`, st.tenantID, messageID).Scan(&conversation); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2 AND state='validated'`, st.tenantID, messageID).Scan(&stored); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved)
		})
		if err != nil || stored != 1 || reserved != int64(len(jpeg)) || files.objects() != 1 {
			t.Fatalf("entrada: att=%d reserved=%d objects=%d err=%v", stored, reserved, files.objects(), err)
		}
		if err := downloadAgain(t, st, messageID, files); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved)
		}); err != nil || reserved != int64(len(jpeg)) {
			t.Fatalf("replay alterou a reserva: %d %v", reserved, err)
		}
		sessionID := uuid.New()
		now := time.Now().UTC()
		if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
			return testStore.InsertSession(ctx, db.NewSession{
				ID: sessionID, TokenHash: auth.HashSecret(uuid.NewString()), UserID: st.userID, TenantID: st.tenantID,
				MembershipID: st.memberID, CSRFHash: auth.HashSecret(uuid.NewString()), Now: now,
				IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour),
			})
		}); err != nil {
			t.Fatal(err)
		}
		principal := auth.Principal{SessionID: sessionID, UserID: st.userID, TenantID: st.tenantID, MembershipID: st.memberID, Role: "admin"}
		svc := &attachments.Service{DB: testStore, Storage: files, Limits: tenantLimitResolver(st.tenantID), Now: func() time.Time { return now }}
		if _, err := svc.Register(ctx, principal, st.inboxID, conversation, attachments.RegisterInput{RequestID: uuid.New(), Name: "saida-grande.pdf", Bytes: 8, Direction: "outgoing"}); !errors.Is(err, attachments.ErrLimit) {
			t.Fatalf("saída acima do plano: %v", err)
		}
		if _, err := svc.Register(ctx, principal, st.inboxID, conversation, attachments.RegisterInput{RequestID: uuid.New(), Name: "saida.pdf", Bytes: 6, Direction: "outgoing"}); err != nil {
			t.Fatalf("saída dentro do plano: %v", err)
		}
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1`, st.tenantID).Scan(&reserved)
		}); err != nil || reserved != int64(len(jpeg))+6 {
			t.Fatalf("contabilização conjunta: %d %v", reserved, err)
		}
	})
}

func downloadImage(t *testing.T, st seededTenant, externalID, from, mediaID string, body []byte) (uuid.UUID, *memStorage) {
	t.Helper()
	ctx := context.Background()
	fake := &whatsapp.FakeFetcher{Objects: map[string]whatsapp.MediaBlob{mediaID: {MIME: "image/jpeg", Body: body}}}
	files := &memStorage{}
	inbound := NewInboundHandler(testStore, testAttendanceSvc).(*inboundHandler)
	inbound.jobs = testJobs
	downloader := mediaWorkers(t, files, fake)
	downloader.limits = tenantLimitResolver(st.tenantID)
	payload, err := metacloud.MarshalMessageEnvelope(metacloud.MessageFixture{
		WABAID: st.wabaID, PhoneNumberID: st.phoneID, MessageID: externalID, From: from,
		Timestamp: "1700000900", Type: "image", Object: map[string]any{"id": mediaID, "mime_type": "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := storeDelivery(t, st.appID, payload)
	if err := NewIngressHandler(testPools.Ingress, testJobs, testConfigServer).ExpandIngress(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	applyDelivery(t, inbound, st.tenantID, delivery)
	var messageID uuid.UUID
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE tenant_id=$1 AND external_message_id=$2`, st.tenantID, externalID).Scan(&messageID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := downloader.DownloadInboundMedia(ctx, st.tenantID, messageID); err != nil {
		t.Fatal(err)
	}
	return messageID, files
}

func downloadAgain(t *testing.T, st seededTenant, messageID uuid.UUID, files *memStorage) error {
	t.Helper()
	fake := &whatsapp.FakeFetcher{}
	downloader := mediaWorkers(t, files, fake)
	downloader.limits = tenantLimitResolver(st.tenantID)
	return downloader.DownloadInboundMedia(context.Background(), st.tenantID, messageID)
}

func assertMediaFailed(t *testing.T, tenantID, messageID uuid.UUID, code string, files *memStorage) {
	t.Helper()
	var status, got string
	var messages, attachments int
	err := testStore.WithTenantTx(context.Background(), tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND id=$2`, tenantID, messageID).Scan(&messages); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT content_context->>'media_status', content_context->>'media_error' FROM messages WHERE tenant_id=$1 AND id=$2`, tenantID, messageID).Scan(&status, &got); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND request_id=$2`, tenantID, messageID).Scan(&attachments)
	})
	if err != nil || messages != 1 || status != "failed" || got != code || attachments != 0 || files.objects() != 0 {
		t.Fatalf("recusa: messages=%d status=%s erro=%s attachments=%d objects=%d err=%v", messages, status, got, attachments, files.objects(), err)
	}
}

func setAttachmentLimits(t *testing.T, tenant uuid.UUID, limits string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `UPDATE tenants SET limits=$2::jsonb WHERE id=$1`, tenant, limits)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("limites: %v rows=%d", err, tag.RowsAffected())
	}
}

func tenantLimitResolver(tenant uuid.UUID) attachments.ResolveLimits {
	return func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (attachments.Limits, error) {
		if id != tenant {
			return attachments.Limits{}, attachments.ErrStorage
		}
		var plan, reserve, file int64
		err := tx.QueryRow(ctx, `
			SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_reserve_bytes')::bigint, 0),
			       COALESCE((limits->>'attachment_file_bytes')::bigint, 0)
			  FROM tenants
			 WHERE id=$1
			   AND id=NULLIF(current_setting('app.tenant_id', true), '')::uuid
		`, id).Scan(&plan, &reserve, &file)
		if err != nil || plan <= 0 || file <= 0 || reserve < 0 {
			return attachments.Limits{}, attachments.ErrStorage
		}
		return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: reserve, FileBytes: file}, nil
	}
}

func audioContainerMP4() []byte {
	return mp4Track("isom", "soun", "mp4a")
}

func mp4Track(brand, handler, sample string) []byte {
	ftyp := mp4Box("ftyp", append([]byte(brand), 0, 0, 0, 0))
	hdlrBody := make([]byte, 25)
	copy(hdlrBody[8:12], handler)
	stsd := mp4Box("stsd", append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, mp4Box(sample, make([]byte, 28))...))
	mdia := mp4Box("mdia", append(mp4Box("hdlr", hdlrBody), mp4Box("minf", mp4Box("stbl", stsd))...))
	return append(ftyp, mp4Box("moov", mp4Box("trak", mdia))...)
}

func mp4Box(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	n := uint32(len(out))
	out[0] = byte(n >> 24)
	out[1] = byte(n >> 16)
	out[2] = byte(n >> 8)
	out[3] = byte(n)
	copy(out[4:], typ)
	copy(out[8:], payload)
	return out
}

func storeDelivery(t *testing.T, appID string, body []byte) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.WithIngressTx(context.Background(), testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		delivery, err := (store.Repo{}).StoreDelivery(ctx, tx, appID, body, "", "")
		id = delivery.ID
		return err
	})
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	return id
}

func applyDelivery(t *testing.T, inbound *inboundHandler, tenantID, deliveryID uuid.UUID) {
	t.Helper()
	var ids []uuid.UUID
	err := testStore.WithTenantTx(context.Background(), tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM webhook_items WHERE tenant_id=$1 AND delivery_id=$2 AND kind='message'`, tenantID, deliveryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil || len(ids) == 0 {
		t.Fatalf("itens do webhook: %v %d", err, len(ids))
	}
	for _, id := range ids {
		if err := inbound.ApplyInbound(context.Background(), tenantID, id); err != nil {
			t.Fatalf("apply %s: %v", id, err)
		}
	}
}
