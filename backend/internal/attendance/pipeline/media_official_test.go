package pipeline

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
)

func TestOutboundMediaInvalidCaptionIsFailedNotUncertain(t *testing.T) {
	bodies := []string{"linha\nquebra", strings.Repeat("a", 1025)}
	for i, body := range bodies {
		ctx := context.Background()
		phone := "551196660450" + strconv.Itoa(i)
		st, principal, convID, inboundID, _ := seedMediaConversation(t, phone, "wamid.CITADO."+phone)
		imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		sent := sendMedia(t, ctx, st, principal, convID, "req-legenda-"+phone, "legenda curta", imageID, &inboundID)
		if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE messages SET body=$3 WHERE tenant_id=$1 AND id=$2`, st.tenantID, sent.MessageID, body)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		fake := whatsapp.NewFake()
		factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: fake})
		if err != nil {
			t.Fatal(err)
		}
		if err = NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, sent.AttemptID); err != nil {
			t.Fatal(err)
		}
		if len(fake.Captures()) != 0 {
			t.Fatalf("driver recebeu legenda inválida: %#v", fake.Captures())
		}
		state, code := readAttempt(t, ctx, st.tenantID, sent.AttemptID)
		if state != "failed" || code != "legenda_invalida" {
			t.Fatalf("tentativa %s/%s", state, code)
		}
	}
}

func TestOutboundMediaOfficialLoopbackCarriesBytes(t *testing.T) {
	ctx := context.Background()
	st, principal, convID, _, _ := seedMediaConversation(t, "5511966604419", "wamid.CITADO.4419")
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("MARK-PNG-4419")...)
	imageID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "image/png", "foto.png", png)
	files := newPipelineMem()
	files.put(imageID.String(), "v1", png)
	svc := testAttendanceSvc
	svc.Files = files
	svc.AttachmentLimits = func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: 32 << 20, IncomingReserveBytes: 0, FileBytes: 8 << 20}, nil
	}
	sent := sendMediaOn(t, ctx, svc, st, principal, convID, "req-oficial-4419", "foto oficial", imageID, nil)

	var mu sync.Mutex
	var seen []recordedHit
	var messageSeq int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		mu.Lock()
		seen = append(seen, recordedHit{path: r.URL.Path, body: raw, host: r.Host})
		seq := 0
		if strings.Contains(r.URL.Path, "/messages") {
			messageSeq++
			seq = messageSeq
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/media") && !strings.Contains(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(`{"id":"media-loop"}`))
			return
		}
		// Image and audio must not share a provider id: messages_external_key
		// is unique per connector. The type in the body wins over the counter
		// so a repeated path still accepts both sends.
		remoteID := "wamid.LOOP.4419." + strconv.Itoa(seq)
		switch {
		case bytes.Contains(raw, []byte(`"type":"audio"`)):
			remoteID = "wamid.LOOP.4419.2"
		case bytes.Contains(raw, []byte(`"type":"image"`)), seq <= 1:
			remoteID = "wamid.LOOP.4419"
		}
		_, _ = w.Write([]byte(`{"messages":[{"id":"` + remoteID + `"}]}`))
	}))
	defer server.Close()
	driver, err := metacloud.LoopbackOfficial(server.URL, "v23.0", "106540352242922", "token-loopback-oficial", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewOutboundHandler(testStore, svc, testConfigServer, testConfigServer, fixedDriver{driver: driver})
	if err = handler.DispatchOutbound(ctx, st.tenantID, sent.AttemptID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	hits := append([]recordedHit(nil), seen...)
	mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("http %d", len(hits))
	}
	var upload, message bool
	for _, hit := range hits {
		if strings.Contains(hit.host, "facebook.com") {
			t.Fatalf("chamou host real %s", hit.host)
		}
		if strings.HasSuffix(hit.path, "/media") && bytes.Contains(hit.body, []byte("MARK-PNG-4419")) {
			upload = true
		}
		if strings.HasSuffix(hit.path, "/messages") && bytes.Contains(hit.body, []byte(`"type":"image"`)) && bytes.Contains(hit.body, []byte(`"id":"media-loop"`)) {
			message = true
		}
	}
	if !upload || !message {
		t.Fatal("corpos do upload ou da mensagem não conferem")
	}
	assertAcceptedMessage(t, ctx, st.tenantID, sent.MessageID, "wamid.LOOP.4419")

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	webm := ffmpegWebM(t, 1)
	audioID := insertOutgoingAttachment(t, ctx, st.tenantID, convID, "audio/webm", "gravacao.webm", webm)
	files.put(audioID.String(), "v1", webm)
	audioSent := sendMediaOn(t, ctx, svc, st, principal, convID, "req-oficial-audio", "", audioID, nil)
	expireInboxLease(t, ctx, st)
	mu.Lock()
	seen = nil
	mu.Unlock()
	if err = handler.DispatchOutbound(ctx, st.tenantID, audioSent.AttemptID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	hits = append([]recordedHit(nil), seen...)
	mu.Unlock()
	var opus bool
	for _, hit := range hits {
		if strings.HasSuffix(hit.path, "/media") && bytes.Contains(hit.body, []byte("OggS")) && bytes.Contains(hit.body, []byte("OpusHead")) {
			opus = true
			if bytes.Contains(hit.body, []byte("application/ogg")) || bytes.Contains(hit.body, []byte("video/ogg")) {
				t.Fatal("upload de áudio usou MIME de container em vez de audio/ogg")
			}
			if !bytes.Contains(hit.body, []byte("name=\"type\"\r\n\r\naudio/ogg\r\n")) || !bytes.Contains(hit.body, []byte("Content-Type: audio/ogg\r\n")) {
				t.Fatal("upload de áudio sem type=audio/ogg e Content-Type audio/ogg")
			}
		}
	}
	if !opus {
		t.Fatal("upload de áudio sem OGG/Opus")
	}
	assertAcceptedMessage(t, ctx, st.tenantID, audioSent.MessageID, "wamid.LOOP.4419.2")
}

type recordedHit struct {
	path, host string
	body       []byte
}

type fixedDriver struct{ driver whatsapp.TextDriver }

func (f fixedDriver) DriverFor(context.Context, uuid.UUID, uuid.UUID, configuration.Material) (whatsapp.TextDriver, error) {
	return f.driver, nil
}

func sendMediaOn(t *testing.T, ctx context.Context, svc service.Service, st seededTenant, principal service.Principal, conv uuid.UUID, requestID, caption string, attachment uuid.UUID, reply *uuid.UUID) service.SendResult {
	t.Helper()
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv).Scan(&version)
	}); err != nil {
		t.Fatal(err)
	}
	sent, err := svc.SendMedia(ctx, principal, service.MediaSendInput{
		InboxID: st.inboxID, ConversationID: conv, ExpectedVersion: version,
		ClientRequestID: requestID, Body: caption, AttachmentID: attachment, ReplyToMessageID: reply, At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sent
}

func ffmpegWebM(t *testing.T, seconds int) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.webm")
	var last []byte
	var lastErr error
	for _, spec := range []string{"anullsrc=r=8000:cl=mono", "anullsrc=channel_layout=mono:sample_rate=8000"} {
		cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-y", "-f", "lavfi", "-i", spec,
			"-t", strconv.Itoa(seconds), "-c:a", "libopus", "-b:a", "6k", "-f", "webm", out)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir()}
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

type pipelineMem struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newPipelineMem() *pipelineMem { return &pipelineMem{objs: map[string][]byte{}} }

func (m *pipelineMem) put(key, version string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key+"\x00"+version] = append([]byte(nil), body...)
}

func (m *pipelineMem) Create(_ context.Context, key string, r io.ReadSeeker, n int64) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil || int64(len(body)) != n {
		return "", io.ErrUnexpectedEOF
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	version := "mem-" + strconv.Itoa(len(m.objs)+1)
	m.objs[key+"\x00"+version] = body
	return version, nil
}

func (m *pipelineMem) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.objs[key+"\x00"+version]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (m *pipelineMem) Sign(context.Context, string, string, string, time.Duration) (string, error) {
	return "", io.ErrClosedPipe
}

func (m *pipelineMem) Delete(_ context.Context, key, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key+"\x00"+version)
	return nil
}
