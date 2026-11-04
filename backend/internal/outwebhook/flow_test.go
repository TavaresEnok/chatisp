package outwebhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/platform/urlguard"
)

func TestDeliveryFlowSignsOnceAndRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
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
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	jobsPassword, err := randomHex(24)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureJobsRole(ctx, env.AdminURL, jobsPassword); err != nil {
		t.Fatal(err)
	}
	jobsURL, err := withUser(env.AdminURL, "chatisp_jobs", jobsPassword)
	if err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	if err = jobs.Migrate(ctx, pools.Owner); err != nil {
		t.Fatal(err)
	}
	jobsPool, err := pgxpool.New(ctx, jobsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer jobsPool.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	tenant, user, member := uuid.New(), uuid.New(), uuid.New()
	connector, inbox, sector, contact, identity, conv, message := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `INSERT INTO users(id,email,status) VALUES($1,$2,'active')`, user, user.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	secretText := "TEXTO-SECRETO-NAO-ENVIAR"
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,'admin','active')`, member, tenant, user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled) VALUES($1,$2,'meta_cloud',$3,'connected',true)`, connector, tenant, connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'WhatsApp')`, inbox, tenant, connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, tenant, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, tenant, sector, inbox); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_memberships VALUES($1,$2,$3)`, tenant, sector, member); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, contact, tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, identity.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	key := bytes.Repeat([]byte{7}, 32)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		t.Fatal(err)
	}
	rawSecret := make([]byte, 32)
	if _, err = rand.Read(rawSecret); err != nil {
		t.Fatal(err)
	}
	shown := hex.EncodeToString(rawSecret)
	receiver := NewReceiver([]byte(shown))
	receiver.Failures = 1
	capture := &bodyCapture{inner: receiver}
	server := httptest.NewServer(capture)
	defer server.Close()

	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{
		ID: uuid.New(), TenantID: tenant, URL: server.URL, Secret: []byte(shown), Enabled: true,
		Events: map[string]bool{
			EventConversationCreated: true, EventConversationAssigned: true,
			EventConversationClosed: true, EventMessageCreated: true,
		},
	}
	stored, err := SealSubscription(ring, sub)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return InsertSubscription(ctx, tx, stored)
	}); err != nil {
		t.Fatal(err)
	}

	hook := JobsHandler{Deps: Deps{Store: store, Ring: ring, Policy: policy, Mode: "test", Poster: HTTPPoster{Policy: policy, Mode: "test"}}}
	client, err := jobs.New(jobsPool, jobs.Handlers{Webhook: hook}, jobs.WithLogger(slog.New(slog.DiscardHandler)), jobs.WithTestTiming(20*time.Millisecond, jobs.Backoff{}, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := client.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()

	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number)
			VALUES($1,$2,$3,$4,$5,$6,'open','menu',$7,'202611',1)`,
			conv, tenant, inbox, sector, contact, identity, "P-"+conv.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3, controller='human' WHERE tenant_id=$1 AND id=$2`, tenant, conv, member)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO messages(id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body)
			VALUES($1,$2,$3,$4,'inbound','customer','public',$5)`,
			message, tenant, conv, connector, secretText)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET status='closed' WHERE tenant_id=$1 AND id=$2`, tenant, conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var rows []deliveryRow
	for time.Now().Before(deadline) {
		rows = loadFlowDeliveries(t, ctx, store, tenant)
		if receiver.Applied() == 4 && len(rows) == 4 && allDelivered(rows) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if receiver.Applied() != 4 {
		t.Fatalf("aplicados %d, entregas %+v, corpos %d", receiver.Applied(), rows, capture.count())
	}
	if len(rows) != 4 || !allDelivered(rows) {
		t.Fatalf("entregas %+v", rows)
	}
	seen := map[string]uuid.UUID{}
	for _, row := range rows {
		if _, ok := seen[row.Type]; ok {
			t.Fatalf("evento duplicado %s", row.Type)
		}
		seen[row.Type] = row.Source
	}
	for _, kind := range []string{EventConversationCreated, EventConversationAssigned, EventConversationClosed, EventMessageCreated} {
		if _, ok := seen[kind]; !ok {
			t.Fatalf("faltou %s em %+v", kind, rows)
		}
	}
	if seen[EventMessageCreated] != message || seen[EventConversationCreated] != conv {
		t.Fatalf("origens %+v", seen)
	}
	bodies := capture.snapshot()
	ids := map[string]map[string]struct{}{}
	for _, body := range bodies {
		if bytes.Contains(body, []byte(secretText)) {
			t.Fatal("corpo do webhook contém o texto da mensagem")
		}
		var env Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		if env.ConversationID != conv.String() {
			t.Fatalf("conversa %s no evento %s", env.ConversationID, env.Type)
		}
		if (env.Type == EventConversationAssigned || env.Type == EventConversationClosed) && env.SourceID == conv.String() {
			t.Fatalf("ocorrência %s reutilizou a conversa", env.Type)
		}
		if ids[env.Type] == nil {
			ids[env.Type] = map[string]struct{}{}
		}
		ids[env.Type][env.EventID] = struct{}{}
	}
	for _, kind := range []string{EventConversationCreated, EventConversationAssigned, EventConversationClosed, EventMessageCreated} {
		if len(ids[kind]) != 1 {
			t.Fatalf("ids de %s: %d", kind, len(ids[kind]))
		}
	}
	applied := receiver.Applied()
	bodyCount := capture.count()
	if err = DeliverSource(ctx, Deps{Store: store, Ring: ring, Policy: policy, Mode: "test", Poster: HTTPPoster{Policy: policy, Mode: "test"}}, tenant, EventMessageCreated, message); err != nil {
		t.Fatal(err)
	}
	if receiver.Applied() != applied || capture.count() != bodyCount {
		t.Fatalf("sucesso reenviou: aplicados %d corpos %d", receiver.Applied(), capture.count())
	}

	router := chi.NewRouter()
	MountPanel(router, PanelDeps{
		Auth:  stubAuth{principal: auth.Principal{TenantID: tenant, Role: "admin"}},
		Store: store, Ring: ring, Policy: policy, Mode: "test",
		TrustedOrigins: []string{"https://panel.example"},
	})
	listReq := httptest.NewRequest(http.MethodGet, "/api/panel/outbound-webhooks/"+sub.ID.String()+"/deliveries", nil)
	listReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sessao"})
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("lista de entregas %d %s", listRec.Code, listRec.Body.String())
	}
	privateReq := httptest.NewRequest(http.MethodPost, "/api/panel/outbound-webhooks", bytes.NewReader([]byte(`{"url":"http://10.1.2.3/hook","events":["message.created"]}`)))
	privateReq.Header.Set("Origin", "https://panel.example")
	privateReq.Header.Set(auth.CSRFHeader, "csrf")
	privateReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sessao"})
	privateRec := httptest.NewRecorder()
	router.ServeHTTP(privateRec, privateReq)
	if privateRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("destino privado %d %s", privateRec.Code, privateRec.Body.String())
	}
}

type deliveryRow struct {
	Type   string
	Status string
	Source uuid.UUID
}

func loadFlowDeliveries(t *testing.T, ctx context.Context, store *db.Store, tenant uuid.UUID) []deliveryRow {
	t.Helper()
	var rows []deliveryRow
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		listed, err := tx.Query(ctx, `SELECT event_type, status, source_id FROM webhook_deliveries WHERE tenant_id=$1`, tenant)
		if err != nil {
			return err
		}
		defer listed.Close()
		for listed.Next() {
			var row deliveryRow
			if err := listed.Scan(&row.Type, &row.Status, &row.Source); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return listed.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func allDelivered(rows []deliveryRow) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if row.Status != StatusDelivered {
			return false
		}
	}
	return true
}

type bodyCapture struct {
	inner http.Handler
	mu    sync.Mutex
	body  [][]byte
}

func (c *bodyCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	r.Body = io.NopCloser(bytes.NewReader(body))
	c.mu.Lock()
	c.body = append(c.body, append([]byte(nil), body...))
	c.mu.Unlock()
	c.inner.ServeHTTP(w, r)
}

func (c *bodyCapture) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.body))
	copy(out, c.body)
	return out
}

func (c *bodyCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.body)
}

type stubAuth struct{ principal auth.Principal }

func (s stubAuth) Authenticate(context.Context, string, string, bool) (auth.Principal, error) {
	return s.principal, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func ensureJobsRole(ctx context.Context, adminURL, password string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'chatisp_jobs')`).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format('%s ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text, $2::text)
	`, verb, password).Scan(&stmt); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return err
	}
	for _, query := range []string{
		`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`,
	} {
		var grant string
		if err := conn.QueryRow(ctx, query).Scan(&grant); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, grant); err != nil {
			return err
		}
	}
	return nil
}

func withUser(raw, user, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String(), nil
}
