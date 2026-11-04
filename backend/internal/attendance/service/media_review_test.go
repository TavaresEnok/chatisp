package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/store"
	"chatisp/internal/media"
)

func TestSendMediaOutsiderDoesNotTouchStorage(t *testing.T) {
	ctx := context.Background()
	svc, w, conv, imageID := mediaFixture(t, ctx)
	files := svc.Files.(*memFiles)
	before := countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID)
	user, membership := outsider(t, w.tenant)
	other := Principal{UserID: user, MembershipID: membership, TenantID: w.tenant, Role: "agent"}
	_, err := svc.SendMedia(ctx, other, mediaInput(w, conv, "req-fora", "legenda", imageID, nil))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	if files.reads != 0 || files.creates != 0 || files.live() != 1 {
		t.Fatalf("storage tocado: reads=%d creates=%d live=%d", files.reads, files.creates, files.live())
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != before {
		t.Fatal("outsider persistiu anexo")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("outsider criou outbox")
	}
}

func TestSendMediaRefusesBeforeRead(t *testing.T) {
	ctx := context.Background()
	svc, w, conv, imageID := mediaFixture(t, ctx)
	files := svc.Files.(*memFiles)
	p := principal(w)
	stale := mediaInput(w, conv, "req-stale", "legenda", imageID, nil)
	stale.ExpectedVersion = conv.Version + 9
	if _, err := svc.SendMedia(ctx, p, stale); !errors.Is(err, store.ErrStaleVersion) {
		t.Fatalf("versão: %v", err)
	}
	closed, err := svc.Close(ctx, p, CloseInput{
		InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: conv.Version,
		ClientRequestID: "fecha-midia", Reason: "resolvido", Result: "resolved", Summary: "ok", At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SendMedia(ctx, p, mediaInput(w, closed.Conversation, "req-fechada", "legenda", imageID, nil)); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("fechada: %v", err)
	}
	if files.reads != 0 || files.creates != 0 {
		t.Fatalf("leitura antecipada: reads=%d creates=%d", files.reads, files.creates)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1`, w.tenant) != 1 {
		t.Fatal("recusa antes da leitura criou anexo")
	}
}

func TestSendMediaCaptionRejectedBeforeOutbox(t *testing.T) {
	ctx := context.Background()
	cases := []string{strings.Repeat("a", 1025), "linha\nquebra"}
	for _, body := range cases {
		svc, w, conv, imageID := mediaFixture(t, ctx)
		_, err := svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-legenda", body, imageID, nil))
		if !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("legenda %q: %v", body, err)
		}
		assertNoMediaSideEffects(t, ctx, w.tenant, conv.ID)
	}
	svc, w, conv, imageID := mediaFixture(t, ctx)
	enableSignature(t, ctx, w.tenant, strings.Repeat("x", 200))
	_, err := svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-assinada", strings.Repeat("b", 900), imageID, nil))
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("assinatura: %v", err)
	}
	assertNoMediaSideEffects(t, ctx, w.tenant, conv.ID)
}

func TestSendMediaQueueFailureKeepsImage(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	opened := mustReceive(t, live(testJobs), w, "ext-fila", "oi", time.Now().Add(-time.Minute))
	conv := mustAssume(t, live(testJobs), principal(w), opened.Conversation)
	imageID := insertServiceAttachment(t, ctx, w, conv.ID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G'})
	files := newMemFiles()
	files.put(imageID.String(), "v1", []byte{0x89, 'P', 'N', 'G'})
	svc := live(failQueue{})
	svc.Files = files
	svc.AttachmentLimits = generousAttachmentLimits
	_, err := svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-fila", "legenda", imageID, nil))
	if err == nil {
		t.Fatal("fila indisponível foi aceita")
	}
	if files.creates != 0 || files.live() != 1 {
		t.Fatalf("objeto extra creates=%d live=%d", files.creates, files.live())
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 1 {
		t.Fatal("falha da fila deixou anexo")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1`, w.tenant) != 0 {
		t.Fatal("falha da fila deixou outbox")
	}
}

func TestSendMediaConcurrentImageOneOutbox(t *testing.T) {
	ctx := context.Background()
	svc, w, conv, imageID := mediaFixture(t, ctx)
	in := mediaInput(w, conv, "req-concorrente", "legenda", imageID, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []SendResult
	var errs []error
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := svc.SendMedia(ctx, principal(w), in)
			mu.Lock()
			results = append(results, got)
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(results) != 2 || results[0].AttemptID != results[1].AttemptID || results[0].MessageID != results[1].MessageID {
		t.Fatalf("tentativas %#v", results)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 1 {
		t.Fatal("envio concorrente duplicou a outbox")
	}
}

func TestPublishReservedBudgetAndRelease(t *testing.T) {
	ctx := context.Background()
	w := seedWorld(t)
	opened := mustReceive(t, live(testJobs), w, "ext-orcamento", "oi", time.Now().Add(-time.Minute))
	conv := mustAssume(t, live(testJobs), principal(w), opened.Conversation)
	original := insertServiceAttachment(t, ctx, w, conv.ID, "audio/webm", "gravacao.webm", []byte("webm-original"))
	files := newMemFiles()
	plan := int64(1)
	limits := func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: 0, FileBytes: 8 << 20}, nil
	}
	pub := &attachments.Service{DB: testStore, Storage: files, Limits: limits}
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, _, e := pub.PublishReserved(ctx, tx, w.tenant, conv.ID, "audio.ogg", "application/ogg", []byte{1, 2}, time.Now().Add(time.Hour))
		return e
	})
	if !errors.Is(err, attachments.ErrLimit) || files.creates != 0 {
		t.Fatalf("cota esgotada: %v creates=%d", err, files.creates)
	}
	plan = 100
	var derived uuid.UUID
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		id, _, _, e := pub.PublishReserved(ctx, tx, w.tenant, conv.ID, "audio.ogg", "application/ogg", bytes.Repeat([]byte{7}, 40), time.Now().Add(time.Hour))
		derived = id
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if reservedBytes(t, ctx, w.tenant) != 40 || files.creates != 1 {
		t.Fatalf("reserva %d creates=%d", reservedBytes(t, ctx, w.tenant), files.creates)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, _, e := pub.PublishReserved(ctx, tx, w.tenant, conv.ID, "audio.ogg", "application/ogg", bytes.Repeat([]byte{8}, 70), time.Now().Add(time.Hour))
		return e
	})
	if !errors.Is(err, attachments.ErrLimit) || reservedBytes(t, ctx, w.tenant) != 40 || files.creates != 1 {
		t.Fatalf("segunda reserva: %v reserved=%d creates=%d", err, reservedBytes(t, ctx, w.tenant), files.creates)
	}
	err = testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return pub.ReleaseReserved(ctx, tx, w.tenant, derived)
	})
	if err != nil {
		t.Fatal(err)
	}
	if reservedBytes(t, ctx, w.tenant) != 0 || files.live() != 0 {
		t.Fatalf("após remoção reserved=%d live=%d", reservedBytes(t, ctx, w.tenant), files.live())
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND id=$2`, w.tenant, original) != 1 {
		t.Fatal("remoção do derivado apagou o original")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND id=$2`, w.tenant, derived) != 0 {
		t.Fatal("derivado permaneceu")
	}
}

func TestSendMediaRecordedAudioBudgetReplayAndCompensation(t *testing.T) {
	ffmpegPath, lookErr := exec.LookPath("ffmpeg")
	if lookErr != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	ctx := context.Background()
	webm := recordedWebM(t, 1)
	w := seedWorld(t)
	opened := mustReceive(t, live(testJobs), w, "ext-audio", "oi", time.Now().Add(-time.Minute))
	conv := mustAssume(t, live(testJobs), principal(w), opened.Conversation)
	sourceID := insertServiceAttachment(t, ctx, w, conv.ID, "audio/webm", "gravacao.webm", webm)
	tight := newMemFiles()
	tight.put(sourceID.String(), "v1", webm)
	plan := int64(1)
	tightSvc := live(testJobs)
	tightSvc.Files = tight
	tightSvc.AttachmentLimits = func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: 0, FileBytes: 8 << 20}, nil
	}
	_, err := tightSvc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-cota", "", sourceID, nil))
	if !errors.Is(err, attachments.ErrLimit) {
		t.Fatalf("cota: %v", err)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 1 || reservedBytes(t, ctx, w.tenant) != 0 || tight.live() != 1 {
		t.Fatalf("cota deixou derivado reserved=%d live=%d", reservedBytes(t, ctx, w.tenant), tight.live())
	}

	files := newMemFiles()
	files.put(sourceID.String(), "v1", webm)
	svc := live(failQueue{})
	svc.Files = files
	svc.AttachmentLimits = generousAttachmentLimits
	_, err = svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-compensa", "", sourceID, nil))
	if err == nil {
		t.Fatal("fila aceitou o áudio")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND id=$2`, w.tenant, sourceID) != 1 {
		t.Fatal("compensação alterou o original")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 1 || files.live() != 1 || reservedBytes(t, ctx, w.tenant) != 0 {
		t.Fatalf("derivado órfão live=%d reserved=%d", files.live(), reservedBytes(t, ctx, w.tenant))
	}

	okFiles := newMemFiles()
	okFiles.put(sourceID.String(), "v1", webm)
	ok := live(testJobs)
	ok.Files = okFiles
	ok.AttachmentLimits = generousAttachmentLimits
	reply := opened.MessageID
	first, err := ok.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-audio", "nota", sourceID, &reply))
	if err != nil {
		t.Fatal(err)
	}
	reserved := reservedBytes(t, ctx, w.tenant)
	if reserved == 0 || okFiles.creates != 1 {
		t.Fatalf("publicação reserved=%d creates=%d", reserved, okFiles.creates)
	}
	var derived int
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND name='audio.ogg' AND media_type='application/ogg' AND state='validated' AND direction='outgoing' AND expected_bytes=$3`, w.tenant, conv.ID, reserved) != 1 {
		derived = countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND name='audio.ogg'`, w.tenant)
		t.Fatalf("derivado contabilizado %d reserved=%d", derived, reserved)
	}
	reads, creates := okFiles.reads, okFiles.creates
	// t.Setenv restores PATH only when the test ends. A second t.Setenv records
	// the empty directory as the value its cleanup writes back. Keep the empty
	// PATH inside this replay and put the real directories back before the race.
	savedPATH, hadPATH := os.LookupEnv("PATH")
	if err = os.Setenv("PATH", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	restorePATH := func() {
		var setErr error
		switch {
		case hadPATH:
			setErr = os.Setenv("PATH", savedPATH)
		default:
			setErr = os.Setenv("PATH", filepath.Dir(ffmpegPath))
		}
		if setErr != nil {
			t.Errorf("restaurar PATH: %v", setErr)
		}
	}
	defer restorePATH()
	okFiles.failRead = true
	okFiles.failCreate = true
	again, err := ok.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-audio", "nota", sourceID, &reply))
	if err != nil || again.AttemptID != first.AttemptID || again.MessageID != first.MessageID {
		t.Fatalf("replay sem ffmpeg: %v %#v %#v", err, again, first)
	}
	if okFiles.reads != reads || okFiles.creates != creates || okFiles.live() != 2 {
		t.Fatalf("replay converteu de novo reads=%d/%d creates=%d/%d live=%d", okFiles.reads, reads, okFiles.creates, creates, okFiles.live())
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 1 {
		t.Fatal("replay duplicou a outbox")
	}
	restorePATH()
	if _, lookErr = exec.LookPath("ffmpeg"); lookErr != nil {
		if err = os.Setenv("PATH", filepath.Dir(ffmpegPath)); err != nil {
			t.Fatal(err)
		}
		if _, lookErr = exec.LookPath("ffmpeg"); lookErr != nil {
			t.Fatalf("ffmpeg ausente após restaurar PATH: %v", lookErr)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make([]uuid.UUID, 2)
	errs := make([]error, 2)
	fresh := newMemFiles()
	fresh.put(sourceID.String(), "v1", webm)
	// A second source keeps the concurrent case independent of the accepted key.
	otherID := insertServiceAttachment(t, ctx, w, conv.ID, "audio/webm", "outra.webm", webm)
	fresh.put(otherID.String(), "v1", webm)
	head := mustHead(t, ok, principal(w), w.inbox, conv.ID)
	race := live(testJobs)
	race.Files = fresh
	race.AttachmentLimits = generousAttachmentLimits
	raceIn := MediaSendInput{
		InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: head.Version,
		ClientRequestID: "req-audio-corrida", AttachmentID: otherID, At: time.Now(),
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := race.SendMedia(ctx, principal(w), raceIn)
			mu.Lock()
			ids[i] = got.AttemptID
			errs[i] = err
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] == uuid.Nil || ids[0] != ids[1] || fresh.creates != 1 {
		t.Fatalf("corrida ids=%v creates=%d", ids, fresh.creates)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND name='audio.ogg'`, w.tenant) != 2 {
		t.Fatal("corrida deixou derivado perdedor")
	}
}

func TestSendMediaOverlapsTextSendWithoutDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc, w, conv, imageID := mediaFixture(t, ctx)
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	var connector uuid.UUID
	if err = holder.QueryRow(ctx, `SELECT id FROM channel_connectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, w.tenant, w.connector).Scan(&connector); err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	waitLocks := func(query string, want int) {
		t.Helper()
		for {
			var n int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1`, query).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	mediaDone := make(chan error, 1)
	go func() {
		_, err := svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-ordem-midia", "legenda", imageID, nil))
		mediaDone <- err
	}()
	waitLocks("%FOR SHARE OF n,cc%", 1)
	textDone := make(chan error, 1)
	go func() {
		_, err := svc.Send(ctx, principal(w), SendInput{
			InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: conv.Version,
			ClientRequestID: "req-ordem-texto", Body: "texto junto", At: time.Now(),
		})
		textDone <- err
	}()
	waitLocks("%FOR SHARE OF n,cc%", 2)
	lockCtx, lockCancel := context.WithTimeout(ctx, 3*time.Second)
	defer lockCancel()
	var locked uuid.UUID
	if err = holder.QueryRow(lockCtx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, w.tenant, conv.ID).Scan(&locked); err != nil {
		t.Fatalf("conversa indisponível enquanto os envios esperam o conector: %v", err)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	mediaErr, textErr := <-mediaDone, <-textDone
	if isDeadlock(mediaErr) || isDeadlock(textErr) {
		t.Fatalf("deadlock mídia=%v texto=%v", mediaErr, textErr)
	}
	if mediaErr != nil && !errors.Is(mediaErr, store.ErrStaleVersion) {
		t.Fatalf("mídia: %v", mediaErr)
	}
	if textErr != nil && !errors.Is(textErr, store.ErrStaleVersion) {
		t.Fatalf("texto: %v", textErr)
	}
	if mediaErr != nil && textErr != nil {
		t.Fatal("nenhum envio concluiu")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) < 1 {
		t.Fatal("sobreposição não gravou outbox")
	}
}

func TestSendMediaUnderdeclaredWebMRefusesWithoutOutbox(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	ctx := context.Background()
	svc, w, conv, _ := mediaFixture(t, ctx)
	raw := recordedWebM(t, 121)
	declared, ok := media.ContainerDuration("audio/webm", raw)
	if !ok || declared <= 120000 {
		t.Fatalf("fixture real deveria declarar acima de 120s: %d %v", declared, ok)
	}
	lied, ok := rewriteWebMDurationUnits(raw, 1000)
	if !ok {
		t.Fatal("não reescreveu Duration")
	}
	got, ok := media.ContainerDuration("audio/webm", lied)
	if !ok || got <= 0 || got > 5000 {
		t.Fatalf("Duration subdeclarada: %d %v", got, ok)
	}
	sourceID := insertServiceAttachment(t, ctx, w, conv.ID, "audio/webm", "gravacao.webm", lied)
	files := svc.Files.(*memFiles)
	files.put(sourceID.String(), "v1", append([]byte(nil), lied...))
	before := files.live()
	_, err := svc.SendMedia(ctx, principal(w), mediaInput(w, conv, "req-subdeclarado", "", sourceID, nil))
	if !errors.Is(err, media.ErrConvert) {
		t.Fatalf("duração real acima do limite: %v", err)
	}
	if files.live() != before || files.creates != 0 || files.deletes != 0 {
		t.Fatalf("original alterado live=%d creates=%d deletes=%d", files.live(), files.creates, files.deletes)
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, w.tenant, conv.ID) != 0 {
		t.Fatal("recusa de duração criou outbox")
	}
	if countQuery(t, ctx, w.tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3`, w.tenant, conv.ID, sourceID) != 1 {
		t.Fatal("original não permaneceu")
	}
}

func isDeadlock(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "40P01"
}

func rewriteWebMDurationUnits(raw []byte, units float64) ([]byte, bool) {
	out := append([]byte(nil), raw...)
	for i := 0; i+3 < len(out); i++ {
		if out[i] != 0x44 || out[i+1] != 0x89 {
			continue
		}
		sizeByte := out[i+2]
		if sizeByte < 0x80 || sizeByte > 0x88 {
			continue
		}
		n := int(sizeByte & 0x7f)
		if n != 4 && n != 8 {
			continue
		}
		start := i + 3
		if start+n > len(out) {
			continue
		}
		if n == 4 {
			binary.BigEndian.PutUint32(out[start:], math.Float32bits(float32(units)))
		} else {
			binary.BigEndian.PutUint64(out[start:], math.Float64bits(units))
		}
		return out, true
	}
	return nil, false
}

func mediaFixture(t *testing.T, ctx context.Context) (Service, world, store.Conversation, uuid.UUID) {
	t.Helper()
	w := seedWorld(t)
	svc := live(testJobs)
	opened := mustReceive(t, svc, w, "ext-"+w.tenant.String()[:8], "oi", time.Now().Add(-time.Minute))
	conv := mustAssume(t, svc, principal(w), opened.Conversation)
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	id := insertServiceAttachment(t, ctx, w, conv.ID, "image/png", "foto.png", body)
	files := newMemFiles()
	files.put(id.String(), "v1", body)
	svc.Files = files
	svc.AttachmentLimits = generousAttachmentLimits
	return svc, w, conv, id
}

func mediaInput(w world, conv store.Conversation, requestID, body string, attachment uuid.UUID, reply *uuid.UUID) MediaSendInput {
	return MediaSendInput{
		InboxID: w.inbox, ConversationID: conv.ID, ExpectedVersion: conv.Version,
		ClientRequestID: requestID, Body: body, AttachmentID: attachment, ReplyToMessageID: reply, At: time.Now(),
	}
}

func insertServiceAttachment(t *testing.T, ctx context.Context, w world, conv uuid.UUID, mime, name string, body []byte) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sum := sha256.Sum256(body)
	err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO attachments (
				tenant_id, id, conversation_id, request_id, direction, name, expected_bytes,
				object_key, object_version, state, media_type, sha256, expires_at
			) VALUES ($1,$2,$3,$4,'outgoing',$5,$6,$7,'v1','validated',$8,$9, clock_timestamp() + interval '1 day')`,
			w.tenant, id, conv, uuid.New(), name, int64(len(body)), id.String(), mime, hex.EncodeToString(sum[:]))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func outsider(t *testing.T, tenant uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	user, membership := seedMember(t, tenant, "agent")
	ctx := context.Background()
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, tenant, membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return user, membership
}

func assertNoMediaSideEffects(t *testing.T, ctx context.Context, tenant, conv uuid.UUID) {
	t.Helper()
	if countQuery(t, ctx, tenant, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conv) != 0 {
		t.Fatal("legenda inválida entrou na outbox")
	}
	if countQuery(t, ctx, tenant, `SELECT count(*) FROM attendant_signature_receipts WHERE tenant_id=$1`, tenant) != 0 {
		t.Fatal("legenda inválida gravou recibo de assinatura")
	}
	if countQuery(t, ctx, tenant, `SELECT count(*) FROM attachments WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conv) != 1 {
		t.Fatal("legenda inválida criou anexo")
	}
}

func reservedBytes(t *testing.T, ctx context.Context, tenant uuid.UUID) int {
	t.Helper()
	return countQuery(t, ctx, tenant, `SELECT COALESCE((SELECT reserved_bytes FROM attachment_budgets WHERE tenant_id=$1), 0)`, tenant)
}

func generousAttachmentLimits(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
	return attachments.Limits{PlanBytes: 32 << 20, IncomingReserveBytes: 0, FileBytes: 8 << 20}, nil
}

func recordedWebM(t *testing.T, seconds int) []byte {
	t.Helper()
	dir := t.TempDir()
	out := dir + "/out.webm"
	var last []byte
	var lastErr error
	for _, spec := range []string{"anullsrc=r=8000:cl=mono", "anullsrc=channel_layout=mono:sample_rate=8000"} {
		cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-y", "-f", "lavfi", "-i", spec,
			"-t", strconv.Itoa(seconds), "-c:a", "libopus", "-b:a", "6k", "-f", "webm", out)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		msg, err := cmd.CombinedOutput()
		if err == nil {
			body, readErr := os.ReadFile(out)
			if readErr != nil || len(body) == 0 {
				t.Fatal(readErr)
			}
			return body
		}
		last, lastErr = msg, err
	}
	t.Fatalf("gerar webm: %v %s", lastErr, last)
	return nil
}

type memFiles struct {
	mu                      sync.Mutex
	objs                    map[string][]byte
	reads, creates, deletes int
	failRead, failCreate    bool
}

func newMemFiles() *memFiles { return &memFiles{objs: map[string][]byte{}} }

func (m *memFiles) put(key, version string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key+"\x00"+version] = append([]byte(nil), body...)
}

func (m *memFiles) live() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objs)
}

func (m *memFiles) Create(_ context.Context, key string, r io.ReadSeeker, n int64) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil || int64(len(body)) != n {
		return "", errors.New("tamanho")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreate {
		return "", errors.New("create")
	}
	version := "mem-" + strconv.Itoa(m.creates+1)
	id := key + "\x00" + version
	if _, ok := m.objs[id]; ok {
		return "", errors.New("existe")
	}
	m.objs[id] = body
	m.creates++
	return version, nil
}

func (m *memFiles) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.failRead {
		return nil, errors.New("read")
	}
	body, ok := m.objs[key+"\x00"+version]
	if !ok {
		return nil, errors.New("ausente")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (m *memFiles) Sign(context.Context, string, string, string, time.Duration) (string, error) {
	return "", errors.New("sem url")
}

func (m *memFiles) Delete(_ context.Context, key, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes++
	delete(m.objs, key+"\x00"+version)
	return nil
}
