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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	attendanceapi "chatisp/internal/attendance/httpapi"
	"chatisp/internal/attendance/pipeline"
	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/configuration"
	channelapi "chatisp/internal/channel/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type actor struct {
	tenant, user, member uuid.UUID
	token, csrf          string
}

func TestVertical(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// A test must never accidentally migrate an external database.
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
	u, err := url.Parse(env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("chatisp_jobs", password)
	workerPool, err := pgxpool.New(ctx, u.String())
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
	inserts, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	attendance := service.New(store, inserts)
	sent := make(chan string, 2)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Callback string `json:"biz_opaque_callback_data"`
			Text     struct {
				Body string `json:"body"`
			} `json:"text"`
		}
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/messages") || json.NewDecoder(r.Body).Decode(&body) != nil || body.Text.Body != "resposta simulada" {
			http.Error(w, "inválido", 400)
			return
		}
		sent <- body.Callback
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.simulation.outbound.1"}]}`)
	}))
	defer fake.Close()
	factory, err := pipeline.NewDriverFactory(pipeline.DriverFactoryConfig{Simulation: true, StandInURL: fake.URL})
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
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("acceptance", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	const origin = "http://127.0.0.1:3000"
	attendanceapi.Register(api, attendanceapi.Dependencies{Auth: identity, Attendance: attendance, TrustedOrigins: []string{origin}})
	channelapi.Register(api, channelapi.Dependencies{Auth: identity, Channels: channels, TrustedOrigins: []string{origin}})
	router.Handle("/api/webhooks/meta", wh)
	seed := func() actor {
		a := actor{tenant: uuid.New(), user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
		if err := store.WithPlatform(ctx, func(c context.Context) error {
			if _, e := store.CreateTenant(c, db.NewTenant{ID: a.tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
				return e
			}
			_, e := store.CreateUser(c, db.NewUser{ID: a.user, Email: a.user.String() + "@example.test", Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.WithTenant(ctx, a.tenant, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.member, UserID: a.user, Role: "admin", Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		session := uuid.New()
		if err := store.WithAuth(ctx, func(c context.Context) error {
			if e := store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: a.tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
				return e
			}
			return store.SetSessionAssurance(c, session, "full")
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	a, other := seed(), seed()
	request := func(method, path, body string, actor actor, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", actor.csrf)
		if actor.token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: actor.token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d != %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	configure := func(a actor, suffix string) string {
		b := fmt.Sprintf(`{"inbox_name":"SIMULAÇÃO %s","display_number":"+551199999000%s","app_id":"sim-app-%s","app_secret":"sim-secret-%s","waba_id":"sim-waba-%s","phone_number_id":"551199999000%s","access_token":"sim-token-%s"}`, suffix, suffix, suffix, suffix, suffix, suffix, suffix)
		var out struct {
			InboxID string `json:"inbox_id"`
		}
		if err := json.Unmarshal(request("POST", "/api/panel/channels/meta", b, a, 200), &out); err != nil || out.InboxID == "" {
			t.Fatalf("configuração: %+v %v", out, err)
		}
		// New boxes require explicit operational configuration before ingress.
		sectorService := sectors.New(store)
		principal := sectors.Principal{TenantID: a.tenant, UserID: a.user, MembershipID: a.member, Role: "admin"}
		sector, err := sectorService.Create(ctx, principal, "Atendimento "+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := sectorService.Bind(ctx, principal, sector.ID, a.member, "membership", true); err != nil {
			t.Fatal(err)
		}
		if err := sectorService.Bind(ctx, principal, sector.ID, uuid.MustParse(out.InboxID), "inbox", true); err != nil {
			t.Fatal(err)
		}
		if err := sectorService.SetDefault(ctx, principal, sector.ID, uuid.MustParse(out.InboxID)); err != nil {
			t.Fatal(err)
		}
		return out.InboxID
	}
	inbox := configure(a, "1")
	inbox2 := configure(a, "2")
	_ = configure(other, "3")
	_ = configure(other, "4")
	webhookBox := func(suffix, value string) {
		t.Helper()
		payload := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"sim-waba-%s","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"551199999000%s","display_phone_number":"551199999000%s"},%s}}]}]}`, suffix, suffix, suffix, value)
		req := httptest.NewRequest("POST", "/api/webhooks/meta?app_id=sim-app-"+suffix, strings.NewReader(payload))
		mac := hmac.New(sha256.New, []byte("sim-secret-"+suffix))
		mac.Write([]byte(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
		}
		sum := sha256.Sum256([]byte(payload))
		var count int
		if err := db.WithIngressTx(ctx, pools.Ingress, func(c context.Context, tx pgx.Tx) error {
			return tx.QueryRow(c, `SELECT count(*) FROM ingress_deliveries WHERE app_id=$1 AND body_sha256=$2 AND body=$3`, "sim-app-"+suffix, sum[:], []byte(payload)).Scan(&count)
		}); err != nil || count != 1 {
			t.Fatalf("envelope após ACK: %d %v", count, err)
		}
	}
	webhook := func(value string) { webhookBox("1", value) }
	webhook(fmt.Sprintf(`"messages":[{"from":"5511988887777","id":"wamid.simulation.inbound.1","timestamp":"%d","type":"text","text":{"body":"olá simulação"}}]`, time.Now().Unix()))
	webhookBox("2", fmt.Sprintf(`"messages":[{"from":"5511988887777","id":"wamid.simulation.inbound.2","timestamp":"%d","type":"text","text":{"body":"segunda caixa"}}]`, time.Now().Unix()))
	base := "/api/panel/inboxes/" + inbox + "/conversations"
	var snap struct {
		Conversations []struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
			Version  int64  `json:"version"`
		} `json:"conversations"`
		NextCursor int64 `json:"next_cursor"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		data := request("GET", base, "", a, 200)
		if err := json.Unmarshal(data, &snap); err != nil {
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
	if snap.Conversations[0].Protocol == "" || snap.NextCursor == 0 {
		t.Fatalf("snapshot incompleto: %+v", snap)
	}
	item := base + "/" + snap.Conversations[0].ID
	if !bytes.Contains(request("GET", item+"/history", "", a, 200), []byte("olá simulação")) {
		t.Fatal("histórico ausente")
	}
	request("GET", base, "", other, 403)
	base2 := "/api/panel/inboxes/" + inbox2 + "/conversations"
	var snap2 struct {
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	for {
		if err := json.Unmarshal(request("GET", base2, "", a, 200), &snap2); err != nil {
			t.Fatal(err)
		}
		if len(snap2.Conversations) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("segunda caixa sem conversa")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if snap2.Conversations[0].ID == snap.Conversations[0].ID || !bytes.Contains(request("GET", base2+"/"+snap2.Conversations[0].ID+"/history", "", a, 200), []byte("segunda caixa")) {
		t.Fatal("isolamento ou histórico da segunda caixa")
	}
	request("GET", base2+"/"+snap.Conversations[0].ID+"/history", "", a, 404)
	request("GET", base2, "", other, 403)
	var assumed struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(request("POST", item+"/assume", fmt.Sprintf(`{"expected_version":%d}`, snap.Conversations[0].Version), a, 200), &assumed); err != nil {
		t.Fatal(err)
	}
	// Deliver the receipt before the response exists and wait until it is persisted pending.
	webhook(fmt.Sprintf(`"statuses":[{"id":"wamid.simulation.outbound.1","status":"delivered","timestamp":"%d","recipient_id":"5511988887777"}]`, time.Now().Unix()))
	waitCount := func(query string, expected int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var count int
			err := store.WithTenantTx(ctx, a.tenant, func(c context.Context, tx pgx.Tx) error { return tx.QueryRow(c, query, a.tenant).Scan(&count) })
			if err != nil {
				t.Fatal(err)
			}
			if count >= expected {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("condição de recibos não atingida: %d/%d", count, expected)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitCount(`SELECT count(*) FROM message_status_events WHERE tenant_id=$1 AND external_message_id='wamid.simulation.outbound.1' AND pending`, 1)
	send := fmt.Sprintf(`{"expected_version":%d,"client_request_id":"sim-request-1","body":"resposta simulada"}`, assumed.Version)
	first := request("POST", item+"/messages", send, a, 201)
	second := request("POST", item+"/messages", send, a, 201)
	var f, s struct {
		MessageID string `json:"message_id"`
		Replayed  bool   `json:"replayed"`
	}
	json.Unmarshal(first, &f)
	json.Unmarshal(second, &s)
	if f.MessageID == "" || f.MessageID != s.MessageID || !s.Replayed {
		t.Fatalf("replay %s %s", first, second)
	}
	var callback string
	select {
	case callback = <-sent:
	case <-time.After(15 * time.Second):
		t.Fatal("Meta simulado não recebeu resposta")
	}
	if callback == "" {
		t.Fatal("callback ausente")
	}
	for _, status := range []string{"delivered", "read"} {
		webhook(fmt.Sprintf(`"statuses":[{"id":"wamid.simulation.outbound.1","status":"%s","timestamp":"%d","recipient_id":"5511988887777","biz_opaque_callback_data":"%s"}]`, status, time.Now().Unix(), callback))
	}
	waitCount(`SELECT count(DISTINCT kind) FROM message_status_events WHERE tenant_id=$1 AND external_message_id='wamid.simulation.outbound.1' AND kind IN ('delivered', 'read') AND NOT pending AND message_id IS NOT NULL`, 2)
	if err := json.Unmarshal(request("GET", base, "", a, 200), &snap); err != nil || len(snap.Conversations) != 1 {
		t.Fatalf("snapshot após recibos: %+v %v", snap, err)
	}
	request("POST", item+"/close", fmt.Sprintf(`{"expected_version":%d,"client_request_id":"sim-close-1","reason":"fim","result":"resolved","summary":"resolvido"}`, snap.Conversations[0].Version), a, 200)
}
