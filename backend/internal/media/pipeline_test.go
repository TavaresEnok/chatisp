package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/media"
	mediahttp "chatisp/internal/media/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPipelinePreservesOriginalWithoutFFmpeg(t *testing.T) {
	ctx := context.Background()
	env, e := testdb.Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer env.Close()
	secrets, e := db.NewRoleSecrets()
	if e != nil {
		t.Fatal(e)
	}
	urls, e := db.Bootstrap(ctx, env.AdminURL, secrets)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := db.MigrationsDir()
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ApplyMigrations(ctx, urls.Owner, dir); e != nil {
		t.Fatal(e)
	}
	pools, e := db.OpenPools(ctx, urls)
	if e != nil {
		t.Fatal(e)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	identity, e := auth.New(auth.Options{Store: store, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	tenant, user, member := uuid.New(), uuid.New(), uuid.New()
	inbox, connector, sector, conv := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	otherConv := uuid.New()
	contact, ci, ciOther := uuid.New(), uuid.New(), uuid.New()
	if e = store.WithPlatform(ctx, func(c context.Context) error {
		if _, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
			return e
		}
		_, e := store.CreateUser(c, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := store.CreateMembership(c, db.NewMembership{ID: member, UserID: user, Role: "agent", Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Teste')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, member}},
			{`INSERT INTO contacts(id,tenant_id) VALUES($1,$2)`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{ci, tenant, contact, connector, ci.String()}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{ciOther, tenant, contact, connector, ciOther.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'P-1','202610',1)`, []any{conv, tenant, inbox, sector, contact, ci}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'P-2','202610',1)`, []any{otherConv, tenant, inbox, sector, contact, ciOther}},
		} {
			if _, e := tx.Exec(c, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	token, csrf, session := uuid.NewString(), uuid.NewString(), uuid.New()
	if e = store.WithAuth(ctx, func(c context.Context) error {
		return store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(csrf), Now: now, IdleExpiresAt: now.Add(4 * time.Hour), AbsoluteExpiresAt: now.Add(4 * time.Hour)})
	}); e != nil {
		t.Fatal(e)
	}
	p := auth.Principal{SessionID: session, UserID: user, TenantID: tenant, MembershipID: member, Role: "agent"}
	storage := &simStorage{objects: map[string][]byte{}}
	clock := func() time.Time { return now }
	attachmentsSvc := &attachments.Service{DB: store, Storage: storage, Limits: func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) {
		return attachments.Limits{PlanBytes: 1 << 20, IncomingReserveBytes: 1 << 20, FileBytes: 1 << 20}, nil
	}, Now: clock}
	svc := &media.Service{DB: store, Attachments: attachmentsSvc, Now: clock}

	quoted := uuid.New()
	outbound := uuid.New()
	foreign := uuid.New()
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,local_state) VALUES
			($1,$2,$3,$4,'inbound','customer','public','received'),
			($1,$5,$3,$4,'outbound','human','public','accepted'),
			($1,$6,$7,$4,'inbound','customer','public','received')`,
			tenant, quoted, conv, connector, outbound, foreign, otherConv)
		return e
	}); e != nil {
		t.Fatal(e)
	}

	wav := wave(8000, 1, 8, 8000)
	incoming := publish(t, ctx, attachmentsSvc, p, inbox, conv, "nota.wav", wav, "incoming")
	voice := publish(t, ctx, attachmentsSvc, p, inbox, conv, "gravacao.wav", wav, "outgoing")
	png := publish(t, ctx, attachmentsSvc, p, inbox, conv, "foto.png", pngBytes(), "outgoing")
	originalSum := sha256.Sum256(wav)

	got, e := svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: incoming, ReplyToMessageID: &quoted})
	if e != nil {
		t.Fatal(e)
	}
	if got.Direction != "incoming" || got.Kind != "audio" || got.SizeBytes != int64(len(wav)) || got.DurationSource != "container" || got.DurationMS == nil || *got.DurationMS != 1000 {
		t.Fatalf("entrada %#v", got)
	}
	if got.ReplyToMessageID == nil || *got.ReplyToMessageID != quoted {
		t.Fatal("citação de entrada")
	}
	again, e := svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: incoming, ReplyToMessageID: &quoted})
	if e != nil || again.ID != got.ID {
		t.Fatalf("idempotência %v %#v", e, again)
	}
	if _, e = svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: incoming, ReplyToMessageID: &outbound}); !errors.Is(e, media.ErrConflict) {
		t.Fatalf("citação divergente: %v", e)
	}
	if _, e = svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: voice, ReplyToMessageID: &foreign, MessageID: &outbound, Voice: true}); !errors.Is(e, media.ErrInvalid) {
		t.Fatalf("citação de outra conversa: %v", e)
	}
	sent, e := svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: voice, ReplyToMessageID: &quoted, MessageID: &outbound, Voice: true})
	if e != nil {
		t.Fatal(e)
	}
	if sent.Kind != "voice" || sent.Direction != "outgoing" {
		t.Fatalf("voz %#v", sent)
	}
	var linked *uuid.UUID
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT reply_to_message_id FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, outbound).Scan(&linked)
	}); e != nil || linked == nil || *linked != quoted {
		t.Fatalf("reply-to da mensagem %v %v", linked, e)
	}
	image, e := svc.Accept(ctx, p, inbox, conv, media.AcceptInput{AttachmentID: png})
	if e != nil || image.Kind != "image" || image.DurationSource != "absent" || image.DurationMS != nil {
		t.Fatalf("imagem %#v %v", image, e)
	}

	empty := t.TempDir()
	t.Setenv("PATH", empty)
	refused, e := svc.Convert(ctx, p, inbox, conv, sent.ID)
	if !errors.Is(e, media.ErrFFmpegMissing) || refused.ConversionState != "refused" || refused.ConversionError != media.FFmpegMissingDetail || refused.OutputAttachmentID != nil {
		t.Fatalf("recusa %#v %v", refused, e)
	}
	if sum := sha256.Sum256(storage.objects[objectKey(t, ctx, store, tenant, voice)]); sum != originalSum {
		t.Fatal("o original mudou na recusa")
	}

	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	mediahttp.Register(api, mediahttp.Dependencies{Auth: identity, Media: svc, TrustedOrigins: []string{"https://panel.example"}})
	body := httptest.NewRequest(http.MethodPost, "/api/panel/inboxes/"+inbox.String()+"/conversations/"+conv.String()+"/media/"+sent.ID.String()+"/convert", nil)
	body.AddCookie(&http.Cookie{Name: "__Host-session", Value: token})
	body.Header.Set("Origin", "https://panel.example")
	body.Header.Set("X-CSRF-Token", csrf)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, body)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), media.FFmpegMissingDetail) {
		t.Fatalf("http %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "converted") && strings.Contains(rec.Body.String(), `"conversion_state":"converted"`) {
		t.Fatal("a resposta fingiu conversão")
	}

	// t.TempDir is on noexec /tmp, so LookPath reports EACCES as not found.
	// The stand-in must be an absolute path on a filesystem that can execute.
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	binDir, e := os.MkdirTemp(wd, "ffmpeg-bin-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(binDir) })
	script := filepath.Join(binDir, "ffmpeg")
	if e = os.WriteFile(script, []byte("#!/bin/sh\nout=\nfor a in \"$@\"; do out=$a; done\nprintf 'OggS\\000audio-original-preservado-nao-substituido' > \"$out\"\nexit 0\n"), 0o755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", binDir)
	converted, e := svc.Convert(ctx, p, inbox, conv, sent.ID)
	if e != nil {
		t.Fatal(e)
	}
	if converted.ConversionState != "converted" || converted.OutputAttachmentID == nil || *converted.OutputAttachmentID == voice {
		t.Fatalf("saída %#v", converted)
	}
	if sum := sha256.Sum256(storage.objects[objectKey(t, ctx, store, tenant, voice)]); sum != originalSum {
		t.Fatal("o original mudou depois da conversão")
	}
	outKey := objectKey(t, ctx, store, tenant, *converted.OutputAttachmentID)
	if !bytes.HasPrefix(storage.objects[outKey], []byte("OggS\x00")) {
		t.Fatal("saída não é o ogg produzido")
	}
}

func publish(t *testing.T, ctx context.Context, svc *attachments.Service, p auth.Principal, inbox, conv uuid.UUID, name string, body []byte, direction string) uuid.UUID {
	t.Helper()
	rec, e := svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: name, Bytes: int64(len(body)), Direction: direction})
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Upload(ctx, p, inbox, conv, rec.ID, bytes.NewReader(body)); e != nil {
		t.Fatal(e)
	}
	got, e := svc.Validate(ctx, p, inbox, conv, rec.ID)
	if e != nil || got.State != "validated" {
		t.Fatalf("validação %s %v %#v", name, e, got)
	}
	return rec.ID
}

func objectKey(t *testing.T, ctx context.Context, store *db.Store, tenant, id uuid.UUID) string {
	t.Helper()
	var key, version string
	e := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT object_key, object_version FROM attachments WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&key, &version)
	})
	if e != nil || version == "" {
		t.Fatal(e)
	}
	return key + "@" + version
}

func wave(rate, channels, bits, samples int) []byte {
	dataSize := samples * channels * bits / 8
	byteRate := rate * channels * bits / 8
	fmtChunk := make([]byte, 16)
	fmtChunk[0] = 1
	fmtChunk[2] = byte(channels)
	fmtChunk[4] = byte(rate)
	fmtChunk[5] = byte(rate >> 8)
	fmtChunk[8] = byte(byteRate)
	fmtChunk[9] = byte(byteRate >> 8)
	fmtChunk[12] = byte(channels * bits / 8)
	fmtChunk[14] = byte(bits)
	out := []byte("RIFF")
	out = append(out, 0, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ', 16, 0, 0, 0)
	out = append(out, fmtChunk...)
	out = append(out, 'd', 'a', 't', 'a')
	size := []byte{byte(dataSize), byte(dataSize >> 8), byte(dataSize >> 16), byte(dataSize >> 24)}
	out = append(out, size...)
	out = append(out, make([]byte, dataSize)...)
	n := len(out) - 8
	out[4] = byte(n)
	out[5] = byte(n >> 8)
	out[6] = byte(n >> 16)
	out[7] = byte(n >> 24)
	return out
}

func pngBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0, 0, 0, 0, 0}
}

type simStorage struct{ objects map[string][]byte }

func (s *simStorage) Create(_ context.Context, key string, r io.ReadSeeker, n int64) (string, error) {
	if _, ok := s.objects[key+"@v1"]; ok {
		return "", attachments.ErrConflict
	}
	b, e := io.ReadAll(io.LimitReader(r, n+1))
	if e != nil || int64(len(b)) != n {
		return "", attachments.ErrStorage
	}
	s.objects[key+"@v1"] = b
	return "v1", nil
}
func (s *simStorage) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	b, ok := s.objects[key+"@"+version]
	if !ok {
		return nil, attachments.ErrStorage
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *simStorage) Sign(context.Context, string, string, string, time.Duration) (string, error) {
	return "", attachments.ErrStorage
}
func (s *simStorage) Delete(context.Context, string, string) error { return nil }
