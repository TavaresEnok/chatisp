package acceptance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/pipeline"
	"chatisp/internal/attendance/service"
	"chatisp/internal/businesshours"
	"chatisp/internal/cannedreplies"
	"chatisp/internal/channel/configuration"
	channelpolicy "chatisp/internal/channelpolicy"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/notifications"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	panelapi "chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// m4aClock is a local simulated object store. It is not S3 and not a media pipeline.
type m4aClock struct {
	mu      sync.Mutex
	now     time.Time
	base    string
	keys    map[string]bool
	bodies  map[string][]byte
	tickets map[string]struct {
		body []byte
		exp  time.Time
	}
}

func (s *m4aClock) advance(d time.Duration) {
	s.mu.Lock()
	s.now = s.now.Add(d)
	s.mu.Unlock()
}
func (s *m4aClock) Create(_ context.Context, key string, r io.ReadSeeker, _ int64) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys[key] {
		return "", attachments.ErrConflict
	}
	s.keys[key] = true
	s.bodies[key+"@v1"] = body
	return "v1", nil
}
func (s *m4aClock) Read(_ context.Context, key, version string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[key+"@"+version]
	if !ok {
		return nil, attachments.ErrStorage
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}
func (s *m4aClock) Sign(_ context.Context, key, version, _ string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[key+"@"+version]
	if !ok || ttl <= 0 {
		return "", attachments.ErrStorage
	}
	token := uuid.NewString()
	s.tickets[token] = struct {
		body []byte
		exp  time.Time
	}{body: body, exp: s.now.Add(ttl)}
	return s.base + "/simulation/objects/" + token, nil
}
func (s *m4aClock) Delete(_ context.Context, key, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version == "" {
		for item := range s.bodies {
			if strings.HasPrefix(item, key+"@") {
				delete(s.bodies, item)
			}
		}
		delete(s.keys, key)
		return nil
	}
	delete(s.bodies, key+"@"+version)
	return nil
}

func TestM4AIntegrated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if os.Getenv("CHATISP_TEST_DATABASE_URL") != "" {
		t.Fatal("aceitação exige PostgreSQL descartável embutido")
	}
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	password := uuid.NewString()
	var statement string
	err = conn.QueryRow(ctx, `SELECT format('CREATE ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text)`, password).Scan(&statement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`, `SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`} {
		if err = conn.QueryRow(ctx, q).Scan(&statement); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close(ctx)
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	owner, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner})
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Migrate(ctx, owner.Owner); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	owner.Close()
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	jobURL, err := url.Parse(env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	jobURL.User = url.UserPassword("chatisp_jobs", password)
	workerPool, err := pgxpool.New(ctx, jobURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer workerPool.Close()
	store := db.NewStore(pools.App, pools.Platform)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := configuration.New(store, pools.Ingress, ring)
	if err != nil {
		t.Fatal(err)
	}
	var metaCalls atomic.Int64
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metaCalls.Add(1)
		http.Error(w, "simulação Meta não deveria receber este envio", http.StatusBadGateway)
	}))
	defer meta.Close()
	inserts, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	attendance := service.New(store, inserts)
	factory, err := pipeline.NewDriverFactory(pipeline.DriverFactoryConfig{Simulation: true, StandInURL: meta.URL})
	if err != nil {
		t.Fatal(err)
	}
	server := channels.Server()
	handlers, err := pipeline.NewWorkers(pipeline.WorkersConfig{DB: store, IngressPool: pools.Ingress, JobsClient: inserts, AttendanceService: attendance, RouteResolver: server, MaterialResolver: server, CredentialReporter: server, DriverFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(workerPool, handlers, jobs.WithTestTiming(10*time.Millisecond, jobs.Backoff{}, 50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := worker.Stop(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	wh, err := pipeline.NewWebhookHandler(pipeline.WebhookConfig{AppResolver: server, VerifyTokens: pipeline.VerifyTokenFunc(func(context.Context, string) (string, error) { return "sim-verify", nil }), IngressPool: pools.Ingress, JobsClient: inserts})
	if err != nil {
		t.Fatal(err)
	}
	clock := &m4aClock{now: time.Now(), keys: map[string]bool{}, bodies: map[string][]byte{}, tickets: map[string]struct {
		body []byte
		exp  time.Time
	}{}}
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/simulation/objects/")
		clock.mu.Lock()
		ticket, ok := clock.tickets[token]
		now := clock.now
		clock.mu.Unlock()
		w.Header().Set("X-ChatISP-Simulation", "local-attachment")
		if !ok {
			http.Error(w, "simulação: URL desconhecida", http.StatusNotFound)
			return
		}
		if !now.Before(ticket.exp) {
			http.Error(w, "simulação: URL expirada", http.StatusGone)
			return
		}
		_, _ = w.Write(ticket.body)
	}))
	defer objectServer.Close()
	clock.base = objectServer.URL
	limits := func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (attachments.Limits, error) {
		var plan, reserve, file int64
		e := tx.QueryRow(ctx, `SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint,0), COALESCE((limits->>'attachment_reserve_bytes')::bigint,0), COALESCE((limits->>'attachment_file_bytes')::bigint,0) FROM tenants WHERE id=$1 AND id=NULLIF(current_setting('app.tenant_id', true), '')::uuid`, tenantID).Scan(&plan, &reserve, &file)
		if e != nil || plan <= 0 || file <= 0 || reserve < 0 {
			return attachments.Limits{}, attachments.ErrStorage
		}
		return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: reserve, FileBytes: file}, nil
	}
	policy := &channelpolicy.Service{DB: store, Driver: channelpolicy.FakeCatalog{Explicit: true, Items: []channelpolicy.Template{{
		ExternalID: "sim-fake-aviso", Name: "aviso_simulacao", Language: "pt_BR", Category: "utility", State: "APPROVED", Components: json.RawMessage(`[]`),
	}}}}
	const origin = "http://127.0.0.1:3000"
	api := panelapi.New(panelapi.Dependencies{
		Auth: identity, Attendance: attendance, Channels: channels, Sectors: sectors.New(store),
		Contacts: contactdirectory.Service{DB: store, Visibility: panelapi.ContactVisibility{}}, Notifications: notifications.New(store),
		Webhook: wh, CannedReplies: cannedreplies.New(store), ChannelPolicy: policy,
		Attachments: &attachments.Service{DB: store, Storage: clock, Limits: limits}, BusinessHours: businesshours.New(store),
		TrustedOrigins: []string{origin},
	})
	seed := func(role, timezone string, limitsJSON json.RawMessage) actor {
		t.Helper()
		a := actor{tenant: uuid.New(), user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
		if timezone == "" {
			timezone = "UTC"
		}
		if len(limitsJSON) == 0 {
			limitsJSON = json.RawMessage(`{}`)
		}
		if err := store.WithPlatform(ctx, func(c context.Context) error {
			if _, e := store.CreateTenant(c, db.NewTenant{ID: a.tenant, Timezone: timezone, Status: "active", Limits: limitsJSON}); e != nil {
				return e
			}
			_, e := store.CreateUser(c, db.NewUser{ID: a.user, Email: a.user.String() + "@example.test", Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.WithTenant(ctx, a.tenant, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.member, UserID: a.user, Role: role, Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := store.WithAuth(ctx, func(c context.Context) error {
			sealed, e := ring.Encrypt([]byte("simulation-totp-secret"), crypto.Context{TenantID: a.tenant.String(), Entity: "user", EntityID: a.user.String(), Field: "totp_secret"})
			if e != nil {
				return e
			}
			if e = store.BeginMFA(c, a.user, a.tenant, sealed.Ciphertext, sealed.Version); e != nil {
				return e
			}
			if ok, e := store.ConfirmMFA(c, a.user, now); e != nil || !ok {
				return fmt.Errorf("confirm MFA: %v", e)
			}
			session := uuid.New()
			if e = store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: a.tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
				return e
			}
			return store.SetSessionAssurance(c, session, "full")
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	admin := seed("admin", "America/Sao_Paulo", json.RawMessage(`{"attachment_plan_bytes":1048576,"attachment_reserve_bytes":65536,"attachment_file_bytes":1048576}`))
	agent := actor{tenant: admin.tenant, user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
	outsider := actor{tenant: admin.tenant, user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
	if err := store.WithPlatform(ctx, func(c context.Context) error {
		for _, user := range []actor{agent, outsider} {
			if _, e := store.CreateUser(c, db.NewUser{ID: user.user, Email: user.user.String() + "@example.test", Status: "active"}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTenant(ctx, admin.tenant, func(c context.Context) error {
		for _, user := range []actor{agent, outsider} {
			if _, e := store.CreateMembership(c, db.NewMembership{ID: user.member, UserID: user.user, Role: "agent", Status: "active"}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	enroll := func(a actor) {
		t.Helper()
		now := time.Now()
		if err := store.WithAuth(ctx, func(c context.Context) error {
			sealed, e := ring.Encrypt([]byte("simulation-totp-secret"), crypto.Context{TenantID: a.tenant.String(), Entity: "user", EntityID: a.user.String(), Field: "totp_secret"})
			if e != nil {
				return e
			}
			if e = store.BeginMFA(c, a.user, a.tenant, sealed.Ciphertext, sealed.Version); e != nil {
				return e
			}
			if ok, e := store.ConfirmMFA(c, a.user, now); e != nil || !ok {
				return fmt.Errorf("confirm MFA: %v", e)
			}
			session := uuid.New()
			if e = store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: a.tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
				return e
			}
			return store.SetSessionAssurance(c, session, "full")
		}); err != nil {
			t.Fatal(err)
		}
	}
	enroll(agent)
	enroll(outsider)
	other := seed("admin", "UTC", nil)
	request := func(method, path string, body []byte, who actor, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", who.csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: who.token})
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d != %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	var configured struct {
		InboxID string `json:"inbox_id"`
	}
	if err := json.Unmarshal(request("POST", "/api/panel/channels/meta", []byte(`{"inbox_name":"SIMULAÇÃO Atendimento","display_number":"+5511999990001","app_id":"sim-app-m4a","app_secret":"sim-secret-m4a","waba_id":"sim-waba-m4a","phone_number_id":"5511999990001","access_token":"sim-token-m4a"}`), admin, 200), &configured); err != nil || configured.InboxID == "" {
		t.Fatal(err)
	}
	inbox := uuid.MustParse(configured.InboxID)
	sectorService := sectors.New(store)
	principal := sectors.Principal{TenantID: admin.tenant, UserID: admin.user, MembershipID: admin.member, Role: "admin"}
	source, err := sectorService.Create(ctx, principal, "SIMULAÇÃO Atendimento")
	if err != nil {
		t.Fatal(err)
	}
	oncall, err := sectorService.Create(ctx, principal, "SIMULAÇÃO Destino M3B")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{source.ID, oncall.ID} {
		if err = sectorService.Bind(ctx, principal, id, inbox, "inbox", true); err != nil {
			t.Fatal(err)
		}
	}
	if err = sectorService.Bind(ctx, principal, source.ID, agent.member, "membership", true); err != nil {
		t.Fatal(err)
	}
	if err = sectorService.SetDefault(ctx, principal, source.ID, inbox); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(c, `INSERT INTO inbox_send_leases (tenant_id, inbox_id, owner_id, lease_version, expires_at) VALUES ($1,$2,$3,1, clock_timestamp() + interval '2 hours')`, admin.tenant, inbox, uuid.New())
		if e != nil || tag.RowsAffected() != 1 {
			return fmt.Errorf("lease: %v", e)
		}
		tag, e = tx.Exec(c, `INSERT INTO wa_templates (tenant_id, waba_id, external_id, name, language, category, state, components, sync_source, synced_at) SELECT $1, id, 'sim-driver-reinicio', 'reinicio_simulacao', 'pt_BR', 'utility', 'APPROVED', '[]'::jsonb, 'driver', clock_timestamp() FROM meta_wabas WHERE tenant_id=$1 AND status='active'`, admin.tenant)
		if e != nil || tag.RowsAffected() != 1 {
			return fmt.Errorf("template: %v rows=%d", e, tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"sim-waba-m4a","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"5511999990001","display_phone_number":"5511999990001"},"contacts":[{"profile":{"name":"Cliente Simulado"},"wa_id":"5511988887001"}],"messages":[{"from":"5511988887001","id":"wamid.simulation.m4a.1","timestamp":"%d","type":"text","text":{"body":"olá janela M4A"}}]}}]}]}`, time.Now().Unix())
	hook := httptest.NewRequest(http.MethodPost, "/api/webhooks/meta?app_id=sim-app-m4a", strings.NewReader(payload))
	mac := hmac.New(sha256.New, []byte("sim-secret-m4a"))
	mac.Write([]byte(payload))
	hook.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	hookRec := httptest.NewRecorder()
	api.Handler.ServeHTTP(hookRec, hook)
	if hookRec.Code != 200 {
		t.Fatalf("webhook: %d %s", hookRec.Code, hookRec.Body.String())
	}
	base := "/api/panel/inboxes/" + inbox.String() + "/conversations"
	var snap struct {
		Conversations []struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
			Version  int64  `json:"version"`
		} `json:"conversations"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := json.Unmarshal(request("GET", base, nil, admin, 200), &snap); err != nil {
			t.Fatal(err)
		}
		if len(snap.Conversations) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker não criou conversa")
		}
		time.Sleep(50 * time.Millisecond)
	}
	conv := snap.Conversations[0]
	item := base + "/" + conv.ID
	var created struct {
		ID, Shortcode, Content string
		Version                int64
	}
	if err := json.Unmarshal(request("POST", "/api/panel/canned-replies", []byte(`{"shortcode":"reiniciar","title":"Reiniciar atendimento","content":"Olá {{nome_cliente}}, protocolo {{protocolo}}."}`), admin, 201), &created); err != nil || created.ID == "" {
		t.Fatal(err)
	}
	var discarded struct {
		ID      string
		Version int64
	}
	if err := json.Unmarshal(request("POST", "/api/panel/canned-replies", []byte(`{"shortcode":"descartar","title":"Descartar","content":"Texto {{protocolo}} para exclusão."}`), admin, 201), &discarded); err != nil {
		t.Fatal(err)
	}
	request("DELETE", "/api/panel/canned-replies/"+discarded.ID+"?version=1", nil, admin, 200)
	updated := request("PUT", "/api/panel/canned-replies/"+created.ID, []byte(fmt.Sprintf(`{"shortcode":"reiniciar","title":"Reiniciar atendimento","content":"Olá {{nome_cliente}}, protocolo {{protocolo}}.","version":%d}`, created.Version)), admin, 200)
	if !bytes.Contains(updated, []byte(`"version":2`)) {
		t.Fatalf("atualização: %s", updated)
	}
	request("PUT", "/api/panel/attendant-signature", []byte(`{"enabled":true,"prefix":"{{nome_atendente}}: ","version":0}`), admin, 200)
	var assumed struct {
		Version   int64  `json:"version"`
		ContactID string `json:"contact_id"`
		Protocol  string `json:"protocol"`
	}
	if err := json.Unmarshal(request("POST", item+"/assume", []byte(fmt.Sprintf(`{"expected_version":%d}`, conv.Version)), agent, 200), &assumed); err != nil {
		t.Fatal(err)
	}
	var draft struct {
		Text    string   `json:"text"`
		Missing []string `json:"missing"`
	}
	if err := json.Unmarshal(request("POST", "/api/panel/canned-replies/"+created.ID+"/render", []byte(fmt.Sprintf(`{"inbox_id":%q,"conversation_id":%q}`, inbox, conv.ID)), agent, 200), &draft); err != nil {
		t.Fatal(err)
	}
	wantDraft := "Olá Cliente Simulado, protocolo " + assumed.Protocol + "."
	if draft.Text != wantDraft || len(draft.Missing) != 0 || strings.Contains(draft.Text, agent.user.String()) {
		t.Fatalf("rascunho: %+v", draft)
	}
	var sent struct {
		AttemptID string `json:"attempt_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(request("POST", item+"/messages", []byte(fmt.Sprintf(`{"expected_version":%d,"client_request_id":"m4a-texto","body":%q}`, assumed.Version, draft.Text)), agent, 201), &sent); err != nil || sent.State != "queued" {
		t.Fatalf("envio: %+v %v", sent, err)
	}
	history := request("GET", item+"/history", nil, agent, 200)
	signed := agent.user.String() + "@example.test: " + wantDraft
	if strings.Count(string(history), signed) != 1 {
		t.Fatalf("assinatura: %s", history)
	}
	var head struct {
		Head struct {
			Version   int64     `json:"version"`
			ContactID uuid.UUID `json:"contact_id"`
		} `json:"head"`
	}
	if err := json.Unmarshal(history, &head); err != nil {
		t.Fatal(err)
	}
	consent := fmt.Sprintf(`{"purpose":"cobranca","source":"painel","evidence":"evento:simulacao-m4a","revoke":%%t}`)
	request("POST", fmt.Sprintf("/api/panel/inboxes/%s/channel-policy/contacts/%s/consents", inbox, head.Head.ContactID), []byte(fmt.Sprintf(consent, false)), agent, 200)
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(c, `UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours' WHERE tenant_id=$1 AND direction='inbound' AND NOT historical`, admin.tenant)
		if e != nil || tag.RowsAffected() < 1 {
			return fmt.Errorf("âncora antes do catálogo: %v rows=%d", e, tag.RowsAffected())
		}
		var closed bool
		e = tx.QueryRow(c, `SELECT COALESCE(max(m.trusted_at) < clock_timestamp() - interval '24 hours', false) FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id WHERE m.tenant_id=$1 AND c.id=$2 AND m.direction='inbound' AND m.origin='customer' AND NOT m.historical`, admin.tenant, uuid.MustParse(conv.ID)).Scan(&closed)
		if e != nil || !closed {
			return fmt.Errorf("janela não fechou antes do catálogo: %v closed=%v", e, closed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var queued struct {
		AttemptID string `json:"attempt_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(request("POST", item+"/template-messages", []byte(fmt.Sprintf(`{"expected_version":%d,"client_request_id":"m4a-template","template_id":"sim-driver-reinicio","language":"pt_BR","purpose":"cobranca"}`, head.Head.Version)), agent, 201), &queued); err != nil || queued.State != "queued" {
		t.Fatalf("template autorizado com janela fechada e consentimento válido: %+v %v", queued, err)
	}
	var approvedReason string
	var approvedAllowed bool
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT allowed, reason FROM dispatch_policy_decisions WHERE tenant_id=$1 AND attempt_id=$2 AND phase='enqueue'`, admin.tenant, uuid.MustParse(queued.AttemptID)).Scan(&approvedAllowed, &approvedReason)
	}); err != nil || !approvedAllowed || approvedReason != "approved_template" {
		t.Fatalf("controle positivo: allowed=%v reason=%s err=%v", approvedAllowed, approvedReason, err)
	}
	attempt := func(id string) (state, code string) {
		t.Helper()
		err := store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
			return tx.QueryRow(c, `SELECT state, COALESCE(error_code,'') FROM outbound_attempts WHERE tenant_id=$1 AND id=$2`, admin.tenant, uuid.MustParse(id)).Scan(&state, &code)
		})
		if err != nil {
			t.Fatal(err)
		}
		return state, code
	}
	if metaCalls.Load() != 0 {
		t.Fatalf("Meta simulada chamada antes do relógio: %d", metaCalls.Load())
	}
	request("POST", "/api/panel/inboxes/"+inbox.String()+"/channel-policy/templates/sync", nil, admin, 204)
	catalog := request("GET", "/api/panel/inboxes/"+inbox.String()+"/channel-policy/templates", nil, agent, 200)
	if !bytes.Contains(catalog, []byte(`"external_id":"sim-fake-aviso"`)) || !bytes.Contains(catalog, []byte(`"source":"fake"`)) || bytes.Contains(catalog, []byte("sim-driver-reinicio")) {
		t.Fatalf("catálogo simulado: %s", catalog)
	}
	var consentGranted bool
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT revoked_at IS NULL FROM contact_consents WHERE tenant_id=$1 AND contact_id=$2 AND inbox_id=$3 AND purpose='cobranca' ORDER BY sequence DESC LIMIT 1`, admin.tenant, head.Head.ContactID, inbox).Scan(&consentGranted)
	}); err != nil || !consentGranted {
		t.Fatalf("consentimento não estava válido antes do template fake: %v granted=%v", err, consentGranted)
	}
	fresh := request("GET", item+"/history", nil, agent, 200)
	var again struct {
		Head struct {
			Version int64 `json:"version"`
		} `json:"head"`
	}
	if err := json.Unmarshal(fresh, &again); err != nil {
		t.Fatal(err)
	}
	denied := request("POST", item+"/template-messages", []byte(fmt.Sprintf(`{"expected_version":%d,"client_request_id":"m4a-fake","template_id":"sim-fake-aviso","language":"pt_BR","purpose":"cobranca"}`, again.Head.Version)), agent, 422)
	if !bytes.Contains(denied, []byte(`"detail":"política de canal recusou o envio: template_not_authorized"`)) {
		t.Fatalf("fake com consentimento válido: %s", denied)
	}
	revoked := request("POST", fmt.Sprintf("/api/panel/inboxes/%s/channel-policy/contacts/%s/consents", inbox, head.Head.ContactID), []byte(fmt.Sprintf(consent, true)), agent, 200)
	if !bytes.Contains(revoked, []byte(queued.AttemptID)) {
		t.Fatalf("revogação não cancelou a tentativa: %s", revoked)
	}
	if state, code := attempt(queued.AttemptID); state != "cancelled" || code != "consent_revoked" {
		t.Fatalf("opt-out: %s/%s", state, code)
	}
	if state, code := attempt(sent.AttemptID); state != "queued" || code != "" {
		t.Fatalf("texto livre após opt-out: %s/%s", state, code)
	}
	afterRevoke := request("GET", item+"/history", nil, agent, 200)
	var revokedHead struct {
		Head struct {
			Version int64 `json:"version"`
		} `json:"head"`
	}
	if err := json.Unmarshal(afterRevoke, &revokedHead); err != nil {
		t.Fatal(err)
	}
	optout := request("POST", item+"/template-messages", []byte(fmt.Sprintf(`{"expected_version":%d,"client_request_id":"m4a-optout","template_id":"sim-fake-aviso","language":"pt_BR","purpose":"cobranca"}`, revokedHead.Head.Version)), agent, 422)
	if !bytes.Contains(optout, []byte(`"detail":"política de canal recusou o envio: consent_required"`)) {
		t.Fatalf("opt-out: %s", optout)
	}
	htmlBody := []byte("<!DOCTYPE html><html><body>disfarce</body></html>")
	pdfBody := []byte("%PDF-1.7\nsimulacao\n")
	upload := func(name string, body []byte) string {
		t.Helper()
		var rec struct {
			ID string `json:"id"`
		}
		raw := request("POST", item+"/attachments", []byte(fmt.Sprintf(`{"request_id":%q,"name":%q,"bytes":%d}`, uuid.New(), name, len(body))), agent, 200)
		if err := json.Unmarshal(raw, &rec); err != nil || rec.ID == "" {
			t.Fatalf("registro: %s", raw)
		}
		request("PUT", item+"/attachments/"+rec.ID+"/content", body, agent, 204)
		return rec.ID
	}
	hidden := upload("foto.jpg", htmlBody)
	invalid := request("POST", item+"/attachments/"+hidden+"/validate", nil, agent, 422)
	if !bytes.Contains(invalid, []byte("anexo inválido")) {
		t.Fatalf("disfarce: %s", invalid)
	}
	if !bytes.Contains(request("GET", item+"/attachments/"+hidden, nil, agent, 200), []byte(`"state":"failed"`)) {
		t.Fatal("disfarce não ficou failed")
	}
	pdf := upload("nota.pdf", pdfBody)
	if !bytes.Contains(request("POST", item+"/attachments/"+pdf+"/validate", nil, agent, 200), []byte(`"state":"validated"`)) {
		t.Fatal("pdf não validou")
	}
	var link struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(request("POST", item+"/attachments/"+pdf+"/download", nil, agent, 200), &link); err != nil || link.ExpiresIn != 60 || !strings.Contains(link.URL, "/simulation/objects/") {
		t.Fatalf("download: %+v %v", link, err)
	}
	got, err := http.Get(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	gotBody, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != 200 || !bytes.Equal(gotBody, pdfBody) || got.Header.Get("X-ChatISP-Simulation") != "local-attachment" {
		t.Fatalf("objeto simulado: %d %q", got.StatusCode, gotBody)
	}
	request("POST", item+"/attachments/"+pdf+"/download", nil, outsider, 403)
	request("POST", item+"/attachments/"+pdf+"/download", nil, other, 403)
	hoursBody := fmt.Sprintf(`{"request_id":%q,"expected_version":0,"config":{"intervals":[{"day":1,"start":540,"end":720}],"welcome":"Bem-vindo simulação","outside_hours":"Fora do expediente simulação","nobody_available":"Ninguém disponível simulação","on_call_sector_id":%q}}`, uuid.New(), oncall.ID)
	request("PUT", "/api/panel/inboxes/"+inbox.String()+"/business-hours", []byte(hoursBody), agent, 403)
	saved := request("PUT", "/api/panel/inboxes/"+inbox.String()+"/business-hours", []byte(hoursBody), admin, 200)
	if !bytes.Contains(saved, []byte(`"timezone":"America/Sao_Paulo"`)) || !bytes.Contains(saved, []byte("Bem-vindo simulação")) {
		t.Fatalf("horário: %s", saved)
	}
	decide := func(instant time.Time, open bool, destination uuid.UUID) {
		t.Helper()
		raw := request("GET", "/api/panel/inboxes/"+inbox.String()+"/business-hours/evaluation?instant="+url.QueryEscape(instant.Format(time.RFC3339)), nil, admin, 200)
		var decision struct {
			Open                bool    `json:"open"`
			Timezone            string  `json:"timezone"`
			DestinationSectorID *string `json:"destination_sector_id"`
		}
		if err := json.Unmarshal(raw, &decision); err != nil {
			t.Fatal(err)
		}
		if decision.Open != open || decision.Timezone != "America/Sao_Paulo" || decision.DestinationSectorID == nil || *decision.DestinationSectorID != destination.String() {
			t.Fatalf("decisão %s: %+v", instant, decision)
		}
	}
	decide(time.Date(2026, 3, 2, 13, 0, 0, 0, time.UTC), true, source.ID)
	decide(time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC), false, oncall.ID)
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(c, `UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours' WHERE tenant_id=$1 AND direction='inbound' AND NOT historical`, admin.tenant)
		if e != nil || tag.RowsAffected() < 1 {
			return fmt.Errorf("âncora: %v rows=%d", e, tag.RowsAffected())
		}
		tag, e = tx.Exec(c, `UPDATE inbox_send_leases SET expires_at = clock_timestamp() - interval '1 second' WHERE tenant_id=$1 AND inbox_id=$2 AND expires_at > clock_timestamp()`, admin.tenant, inbox)
		if e != nil || tag.RowsAffected() != 1 {
			return fmt.Errorf("soltura: %v rows=%d", e, tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clock.advance(25 * time.Hour)
	deadline = time.Now().Add(15 * time.Second)
	for {
		state, code := attempt(sent.AttemptID)
		if state == "cancelled" && code == "window_closed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("janela no despacho: %s/%s", state, code)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metaCalls.Load() != 0 {
		t.Fatalf("despacho chamou a simulação Meta: %d", metaCalls.Load())
	}
	var decisions int
	if err = store.WithTenantTx(ctx, admin.tenant, func(c context.Context, tx pgx.Tx) error {
		if e := tx.QueryRow(c, `SELECT count(*) FROM dispatch_policy_decisions WHERE tenant_id=$1 AND attempt_id=$2 AND phase='dispatch' AND allowed=false AND reason='window_closed'`, admin.tenant, uuid.MustParse(sent.AttemptID)).Scan(&decisions); e != nil {
			return e
		}
		var templateDecisions int
		if e := tx.QueryRow(c, `SELECT count(*) FROM dispatch_policy_decisions WHERE tenant_id=$1 AND attempt_id=$2 AND phase='dispatch'`, admin.tenant, uuid.MustParse(queued.AttemptID)).Scan(&templateDecisions); e != nil {
			return e
		}
		if templateDecisions != 0 {
			return fmt.Errorf("template cancelado gerou decisão de despacho: %d", templateDecisions)
		}
		return nil
	}); err != nil || decisions != 1 {
		t.Fatalf("decisão de janela: %d %v", decisions, err)
	}
	expired, err := http.Get(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	expired.Body.Close()
	if expired.StatusCode != http.StatusGone {
		t.Fatalf("URL assinada após o relógio simulado: %d", expired.StatusCode)
	}
}
