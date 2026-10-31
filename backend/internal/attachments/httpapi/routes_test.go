package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/platform/urlguard"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LOCAL SIMULATION ONLY. Not an S3 server or evidence of S3 connectivity.
type simulatedLocalStorage struct {
	versions    map[string][]byte
	keys        map[string]bool
	signed      int
	readVersion string
	fail        bool
}

func (s *simulatedLocalStorage) Create(_ context.Context, k string, r io.ReadSeeker, _ int64) (string, error) {
	if s.fail {
		return "", attachments.ErrStorage
	}
	if s.keys[k] {
		return "", attachments.ErrConflict
	}
	b, e := io.ReadAll(r)
	if e != nil {
		return "", e
	}
	s.keys[k] = true
	s.versions[k+"@v1"] = b
	return "v1", nil
}
func (s *simulatedLocalStorage) Read(_ context.Context, k, v string) (io.ReadCloser, error) {
	s.readVersion = v
	b, ok := s.versions[k+"@"+v]
	if !ok {
		return nil, attachments.ErrStorage
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *simulatedLocalStorage) Sign(_ context.Context, k, v, n string, ttl time.Duration) (string, error) {
	if _, ok := s.versions[k+"@"+v]; !ok || ttl != attachments.DownloadTTL {
		return "", attachments.ErrStorage
	}
	s.signed++
	return "https://simulation.invalid/temporary", nil
}
func (s *simulatedLocalStorage) Delete(_ context.Context, k, v string) error {
	if s.fail {
		return attachments.ErrStorage
	}
	for key := range s.versions {
		if strings.HasPrefix(key, k+"@") {
			delete(s.versions, key)
		}
	}
	delete(s.keys, k)
	return nil
}

func TestPersistentAuthorizedAttachmentLifecycle(t *testing.T) {
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
	tenant, user, member, inbox, connector, sector, conv := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	contact, ci := uuid.New(), uuid.New()
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
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'P-1','202610',1)`, []any{conv, tenant, inbox, sector, contact, ci}},
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
	storage := &simulatedLocalStorage{versions: map[string][]byte{}, keys: map[string]bool{}}
	limits := attachments.Limits{PlanBytes: 128, IncomingReserveBytes: 64, FileBytes: 128} // fixture bytes, not a commercial plan
	svc := &attachments.Service{DB: store, Storage: storage, Limits: func(context.Context, pgx.Tx, uuid.UUID) (attachments.Limits, error) { return limits, nil }, Now: func() time.Time { return now }}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{identity, svc, []string{"https://panel.example"}})
	base := fmt.Sprintf("/api/panel/inboxes/%s/conversations/%s/attachments", inbox, conv)
	request := func(method, path, body, origin, csrfValue string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: token})
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrfValue)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	pdf := "%PDF-1.7\nfixture\n"
	key := uuid.New()
	body := fmt.Sprintf(`{"request_id":%q,"name":"invoice.pdf","bytes":%d}`, key, len(pdf))
	request("POST", base, body, "https://bad", csrf, 403)
	request("POST", base, body, "https://panel.example", "bad", 403)
	raw := request("POST", base, body, "https://panel.example", csrf, 200)
	var rec attachments.Record
	if e = json.Unmarshal([]byte(raw), &rec); e != nil {
		t.Fatal(e)
	}
	if rec.ID == uuid.Nil {
		t.Fatal(raw)
	}
	same, e := svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: key, Name: "invoice.pdf", Bytes: int64(len(pdf)), Direction: "outgoing"})
	if e != nil || same.ID != rec.ID {
		t.Fatal("idempotency", e)
	}
	if _, e = svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: key, Name: "different.pdf", Bytes: int64(len(pdf)), Direction: "outgoing"}); !errors.Is(e, attachments.ErrConflict) {
		t.Fatal("idempotency mismatch", e)
	}
	if _, e = svc.Download(ctx, p, inbox, conv, rec.ID); !errors.Is(e, attachments.ErrConflict) {
		t.Fatal("unvalidated download", e)
	}
	if e = svc.Upload(ctx, p, inbox, conv, rec.ID, strings.NewReader(pdf+"extra")); !errors.Is(e, attachments.ErrInvalid) {
		t.Fatal("byte overflow", e)
	}
	request("PUT", base+"/"+rec.ID.String()+"/content", pdf, "https://panel.example", csrf, 204)
	if e = svc.Upload(ctx, p, inbox, conv, rec.ID, strings.NewReader(pdf)); !errors.Is(e, attachments.ErrConflict) {
		t.Fatal("object reuse", e)
	}
	// A newer malicious version exists in the simulated store. Validation must
	// read the recorded v1 rather than a mutable latest key.
	for k := range storage.keys {
		storage.versions[k+"@v2"] = []byte("<html>active</html>")
	}
	raw = request("POST", base+"/"+rec.ID.String()+"/validate", "", "https://panel.example", csrf, 200)
	if !strings.Contains(raw, `"media_type":"application/pdf"`) || storage.readVersion != "v1" {
		t.Fatal(raw)
	}
	// A new service instance reads durable state from PG (not an in-memory repo).
	restarted := *svc
	got, e := restarted.Get(ctx, p, inbox, conv, rec.ID)
	if e != nil || got.State != "validated" {
		t.Fatal(got, e)
	}
	request("POST", base+"/"+rec.ID.String()+"/download", "", "https://panel.example", csrf, 200)
	if strings.Contains(raw, "object_key") || strings.Contains(raw, "sha256") {
		t.Fatal("internal storage fields exposed")
	}
	before := storage.signed
	other := p
	other.TenantID = uuid.New()
	if _, e = svc.Download(ctx, other, inbox, conv, rec.ID); e == nil {
		t.Fatal("cross tenant")
	}
	if _, e = svc.Download(ctx, p, uuid.New(), conv, rec.ID); e == nil {
		t.Fatal("wrong inbox")
	}
	if storage.signed != before {
		t.Fatal("URL signed before authorization")
	}
	// Unknown content disguised as a PNG is durable failed, never downloadable.
	bad := "<svg onload='alert(1)'/>"
	badrec, e := svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: "safe.png", Bytes: int64(len(bad)), Direction: "outgoing"})
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Upload(ctx, p, inbox, conv, badrec.ID, strings.NewReader(bad)); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Validate(ctx, p, inbox, conv, badrec.ID); !errors.Is(e, attachments.ErrInvalid) {
		t.Fatal("disguised type", e)
	}
	got, e = svc.Get(ctx, p, inbox, conv, badrec.ID)
	if e != nil || got.State != "failed" {
		t.Fatal("failure not persisted", got, e)
	}
	if _, e = svc.Download(ctx, p, inbox, conv, badrec.ID); !errors.Is(e, attachments.ErrConflict) {
		t.Fatal(e)
	}
	limits.PlanBytes = int64(len(pdf) + len(bad))
	limits.IncomingReserveBytes = 8
	if _, e = svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: "x", Bytes: 1, Direction: "outgoing"}); !errors.Is(e, attachments.ErrLimit) {
		t.Fatal("plan limit", e)
	}
	incoming, e := svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: "received", Bytes: 8, Direction: "incoming"})
	if e != nil {
		t.Fatal("incoming reserve", e)
	}
	if _, e = svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: "x", Bytes: 1, Direction: "incoming"}); !errors.Is(e, attachments.ErrLimit) {
		t.Fatal("unbounded reserve", e)
	}
	policy, e := urlguard.NewPolicy()
	if e != nil {
		t.Fatal(e)
	}
	guard, e := urlguard.NewClient(policy)
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.ReceiveRemote(ctx, p, inbox, conv, incoming.ID, guard, urlguard.Scope{TenantID: tenant.String(), ConnectionID: connector.String()}, "http://127.0.0.1/secret"); !errors.Is(e, attachments.ErrStorage) {
		t.Fatal("guard not applied", e)
	}
	// Expire attachment fixtures independently from session idle expiry: HTTP
	// authentication legitimately refreshed the session with its shorter idle TTL.
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE attachments SET expires_at=$2 WHERE tenant_id=$1 AND state<>'validated'`, tenant, now.Add(-time.Second))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = svc.Upload(ctx, p, inbox, conv, incoming.ID, strings.NewReader("12345678")); !errors.Is(e, attachments.ErrConflict) {
		t.Fatal("expired upload", e)
	}
	storage.fail = true
	if _, e = svc.Cleanup(ctx, p, inbox, conv); !errors.Is(e, attachments.ErrStorage) {
		t.Fatal("cleanup failure released quota", e)
	}
	storage.fail = false
	n, e := svc.Cleanup(ctx, p, inbox, conv)
	if e != nil || n != 2 {
		t.Fatal("cleanup", n, e)
	}
	if got, e = svc.Get(ctx, p, inbox, conv, rec.ID); e != nil || got.State != "validated" {
		t.Fatal("cleanup removed valid history", e)
	}
	limits.PlanBytes = int64(len(pdf)) + 1
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := svc.Register(ctx, p, inbox, conv, attachments.RegisterInput{RequestID: uuid.New(), Name: "last-byte", Bytes: 1, Direction: "outgoing"})
			results <- err
		}()
	}
	accepted, limited := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, attachments.ErrLimit) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || limited != 1 {
		t.Fatal("non-atomic quota", accepted, limited)
	}
	// Suspension blocks direct service calls before signing; restoration does
	// not change the attachment's stored version or grant a different session.
	if e = store.WithPlatform(ctx, func(c context.Context) error { _, e := store.SetTenantStatus(c, tenant, "suspended"); return e }); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Download(ctx, p, inbox, conv, rec.ID); e == nil {
		t.Fatal("suspended tenant")
	}
	if e = store.WithPlatform(ctx, func(c context.Context) error { _, e := store.SetTenantStatus(c, tenant, "active"); return e }); e != nil {
		t.Fatal(e)
	}
	// RLS FORCE with the actual app role: another tenant reads zero rows.
	if e = store.WithTenantTx(ctx, uuid.New(), func(c context.Context, tx pgx.Tx) error {
		var count int
		if e := tx.QueryRow(c, `SELECT count(*) FROM attachments`).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			t.Fatal("RLS leakage")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2 AND membership_id=$3`, tenant, sector, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	request("POST", base+"/"+rec.ID.String()+"/download", "", "https://panel.example", csrf, 403)
	if storage.signed != before {
		t.Fatal("revoked sector signed URL")
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `INSERT INTO sector_memberships VALUES($1,$2,$3)`, tenant, sector, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	// Direct callers cannot use a stale authenticated principal after revocation.
	if e = store.WithAuth(ctx, func(c context.Context) error {
		_, e := store.RevokeSessionForActor(c, session, tenant, user, now)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Download(ctx, p, inbox, conv, rec.ID); e == nil {
		t.Fatal("revoked session signed")
	}
	request("GET", base+"/"+rec.ID.String(), "", "", "", 401)
}
