package auditview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

const (
	secretText = "mensagem-sigilosa-52998224725"
	password   = "senha-correta-10"
	day        = "2024-06-15"
)

var (
	testEnv   *testdb.Env
	testStore *db.Store
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	testEnv = env
	testStore = db.NewStore(pools.App, pools.Platform)
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type member struct {
	user       uuid.UUID
	membership uuid.UUID
	email      string
	role       string
	token      string
}

type fixture struct {
	tenant uuid.UUID
	admin  member
	super  member
	lead   member
	agent  member
	other  member
}

func TestAdminSeesOwnTenantAudit(t *testing.T) {
	fix := seedAudit(t)
	query := "from=" + day + "&to=" + day
	first := call(t, fix.admin.token, query+"&limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), secretText) {
		t.Fatal("a primeira página expôs texto de mensagem")
	}
	page := decode(t, first.Body.Bytes())
	if page.From != day || page.To != day || len(page.Events) != 2 || page.NextCursor == "" {
		t.Fatalf("página %+v", page)
	}
	if page.Events[0].Action != "audit.fim-do-dia" || page.Events[1].Action != "audit.tres" {
		t.Fatalf("ordem %+v", page.Events)
	}
	assertEventShape(t, first.Body.Bytes())

	second := call(t, fix.admin.token, query+"&limit=2&cursor="+page.NextCursor)
	if second.Code != http.StatusOK || strings.Contains(second.Body.String(), secretText) {
		t.Fatalf("segunda: %d %s", second.Code, second.Body.String())
	}
	rest := decode(t, second.Body.Bytes())
	again := call(t, fix.admin.token, query+"&limit=2&cursor="+page.NextCursor)
	if again.Body.String() != second.Body.String() {
		t.Fatalf("página instável\n%s\n%s", second.Body.String(), again.Body.String())
	}
	if len(rest.Events) != 2 || rest.NextCursor == "" {
		t.Fatalf("segunda página %+v", rest)
	}
	pair := []string{rest.Events[0].Action, rest.Events[1].Action}
	sort.Strings(pair)
	if pair[0] != "audit.dois-a" || pair[1] != "audit.dois-b" {
		t.Fatalf("empate no mesmo instante %+v", rest.Events)
	}
	last := decode(t, call(t, fix.admin.token, query+"&limit=2&cursor="+rest.NextCursor).Body.Bytes())
	if len(last.Events) != 1 || last.Events[0].Action != "audit.um" || last.NextCursor != "" {
		t.Fatalf("última página %+v", last)
	}

	insertAudit(t, fix.tenant, fix.admin, "audit.novo", "tenant", time.Date(2024, 6, 16, 2, 45, 0, 0, time.UTC))
	after := call(t, fix.admin.token, query+"&limit=2&cursor="+page.NextCursor)
	if after.Body.String() != second.Body.String() {
		t.Fatal("inserir evento mais novo moveu a página seguinte")
	}
	newest := decode(t, call(t, fix.admin.token, query+"&limit=1").Body.Bytes())
	if len(newest.Events) != 1 || newest.Events[0].Action != "audit.novo" {
		t.Fatalf("evento novo %+v", newest.Events)
	}

	foreign := decode(t, call(t, fix.other.token, query+"&limit=50").Body.Bytes())
	if len(foreign.Events) != 1 || foreign.Events[0].Action != "audit.outro-provedor" || strings.Contains(mustJSON(foreign), secretText) {
		t.Fatalf("outro provedor %+v", foreign.Events)
	}
}

func TestOtherRolesCannotReadProviderAudit(t *testing.T) {
	fix := seedAudit(t)
	query := "from=" + day + "&to=" + day
	for _, item := range []member{fix.lead, fix.agent, fix.super} {
		response := call(t, item.token, query)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", item.role, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), secretText) || strings.Contains(response.Body.String(), "audit.") {
			t.Fatalf("%s vazou auditoria: %s", item.role, response.Body.String())
		}
	}
	missing := call(t, "", query)
	if missing.Code != http.StatusUnauthorized || strings.Contains(missing.Body.String(), secretText) {
		t.Fatalf("anônimo: %d %s", missing.Code, missing.Body.String())
	}
	leaky := call(t, fix.admin.token, "from="+secretText+"&to="+day)
	if leaky.Code != http.StatusUnprocessableEntity || strings.Contains(leaky.Body.String(), secretText) {
		t.Fatalf("período: %d %s", leaky.Code, leaky.Body.String())
	}
}

func TestSameInstantOrdersByID(t *testing.T) {
	fix := seedAudit(t)
	query := "from=" + day + "&to=" + day
	full := decode(t, call(t, fix.admin.token, query+"&limit=50").Body.Bytes())
	var walked []Event
	cursor := ""
	for {
		raw := query + "&limit=1"
		if cursor != "" {
			raw += "&cursor=" + cursor
		}
		response := call(t, fix.admin.token, raw)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secretText) {
			t.Fatalf("página: %d %s", response.Code, response.Body.String())
		}
		page := decode(t, response.Body.Bytes())
		if len(page.Events) != 1 {
			t.Fatalf("página unitária %+v", page.Events)
		}
		walked = append(walked, page.Events[0])
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(walked) != len(full.Events) {
		t.Fatalf("anda %d lista %d", len(walked), len(full.Events))
	}
	for i := range walked {
		if walked[i].Action != full.Events[i].Action || !walked[i].CreatedAt.Equal(full.Events[i].CreatedAt) {
			t.Fatalf("posição %d andou %s lista %s", i, walked[i].Action, full.Events[i].Action)
		}
	}
	seen := map[string]int{}
	for _, event := range walked {
		seen[event.Action]++
	}
	if seen["audit.dois-a"] != 1 || seen["audit.dois-b"] != 1 || seen["audit.madrugada"] != 0 || seen["audit.fora"] != 0 || seen["audit.outro-provedor"] != 0 {
		t.Fatalf("contagem %+v", seen)
	}
}

func seedAudit(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	fix := fixture{tenant: uuid.New()}
	otherTenant := uuid.New()
	fix.admin = newMember("admin")
	fix.super = newMember("admin")
	fix.lead = newMember("supervisor")
	fix.agent = newMember("agent")
	fix.other = newMember("admin")
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		for _, tenant := range []uuid.UUID{fix.tenant, otherTenant} {
			if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
				return err
			}
		}
		for _, item := range []*member{&fix.admin, &fix.super, &fix.lead, &fix.agent, &fix.other} {
			if _, err := testStore.CreateUser(ctx, db.NewUser{ID: item.user, Email: item.email, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, fix.tenant, func(ctx context.Context) error {
		for _, item := range []*member{&fix.admin, &fix.super, &fix.lead, &fix.agent} {
			if _, err := testStore.CreateMembership(ctx, db.NewMembership{ID: item.membership, UserID: item.user, Role: item.role, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, otherTenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: fix.other.membership, UserID: fix.other.user, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		for _, item := range []member{fix.admin, fix.super, fix.lead, fix.agent, fix.other} {
			if err := testStore.SetPasswordHash(ctx, item.user, hash); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	markSuperadmin(t, fix.super.user)
	messageID := seedMessage(t, fix.tenant, fix.admin.membership)
	insertAudit(t, fix.tenant, fix.admin, "audit.madrugada", "message", time.Date(2024, 6, 15, 2, 30, 0, 0, time.UTC))
	insertLinked(t, fix.tenant, fix.admin, "audit.um", messageID, time.Date(2024, 6, 15, 15, 0, 1, 0, time.UTC))
	insertLinked(t, fix.tenant, fix.admin, "audit.dois-a", messageID, time.Date(2024, 6, 15, 15, 0, 2, 0, time.UTC))
	insertLinked(t, fix.tenant, fix.admin, "audit.dois-b", messageID, time.Date(2024, 6, 15, 15, 0, 2, 0, time.UTC))
	insertLinked(t, fix.tenant, fix.admin, "audit.tres", messageID, time.Date(2024, 6, 15, 15, 0, 3, 0, time.UTC))
	insertLinked(t, fix.tenant, fix.admin, "audit.fim-do-dia", messageID, time.Date(2024, 6, 16, 2, 30, 0, 0, time.UTC))
	insertAudit(t, fix.tenant, fix.admin, "audit.fora", "tenant", time.Date(2024, 6, 14, 15, 0, 0, 0, time.UTC))
	insertAudit(t, otherTenant, fix.other, "audit.outro-provedor", "tenant", time.Date(2024, 6, 15, 15, 0, 0, 0, time.UTC))
	service := newService(t)
	for _, item := range []*member{&fix.admin, &fix.super, &fix.lead, &fix.agent, &fix.other} {
		issued, err := service.Login(ctx, item.email, password, "", "203.0.113.10")
		if err != nil {
			t.Fatal(err)
		}
		item.token = issued.Token
	}
	return fix
}

func newMember(role string) member {
	user := uuid.New()
	return member{user: user, membership: uuid.New(), email: user.String() + "@example.com", role: role}
}

func newService(t *testing.T) *auth.Service {
	t.Helper()
	service, err := auth.New(auth.Options{Store: testStore})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func markSuperadmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `UPDATE users SET is_superadmin = true WHERE id = $1`, userID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("superadmin: %v", err)
	}
}

func seedMessage(t *testing.T, tenant, membership uuid.UUID) uuid.UUID {
	t.Helper()
	connector, inbox, contact, identity := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	conversation, message, sector := uuid.New(), uuid.New(), uuid.New()
	err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled) VALUES ($1,$2,'meta_cloud',$3,'connected',true)`, connector, tenant, "origin-"+connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,'WhatsApp')`, inbox, tenant, connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Suporte',true)`, tenant, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES ($1,$2,$3)`, tenant, sector, inbox); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inbox_sector_defaults VALUES ($1,$2,$3)`, tenant, inbox, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1,$2,'Cliente')`, contact, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, "wa-"+identity.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversations (id, tenant_id, inbox_id, contact_id, contact_identity_id, status, controller, protocol, protocol_period, cycle_number, summary) VALUES ($1,$2,$3,$4,$5,'open','menu',$6,'202406',1,$7)`, conversation, tenant, inbox, contact, identity, "P-"+conversation.String()[:8], secretText); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO messages (id, tenant_id, conversation_id, connector_id, direction, origin, visibility, body, author_membership_id) VALUES ($1,$2,$3,$4,'inbound','customer','public',$5,$6)`, message, tenant, conversation, connector, secretText, membership)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func insertLinked(t *testing.T, tenant uuid.UUID, actor member, action string, message uuid.UUID, at time.Time) {
	t.Helper()
	err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (id, tenant_id, membership_id, actor_user_id, action, target_type, target_id, result, correlation_id, created_at)
			VALUES ($1,$2,$3,$4,$5,'message',$6,'success',$7,$8)
		`, uuid.New(), tenant, actor.membership, actor.user, action, message, secretText, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertAudit(t *testing.T, tenant uuid.UUID, actor member, action, target string, at time.Time) {
	t.Helper()
	err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (id, tenant_id, membership_id, actor_user_id, action, target_type, result, correlation_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,'success',$7,$8)
		`, uuid.New(), tenant, actor.membership, actor.user, action, target, secretText, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, token, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	Mount(router, Dependencies{Auth: newService(t), Store: testStore})
	req := httptest.NewRequest(http.MethodGet, Path+"?"+rawQuery, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type body struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor"`
}

func decode(t *testing.T, raw []byte) body {
	t.Helper()
	var page body
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func assertEventShape(t *testing.T, raw []byte) {
	t.Helper()
	var page map[string]json.RawMessage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal(page["events"], &events); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"created_at": true, "action": true, "target_type": true, "result": true}
	for _, event := range events {
		if len(event) != len(allowed) {
			t.Fatalf("evento com campos a mais: %v", event)
		}
		for key := range event {
			if !allowed[key] {
				t.Fatalf("campo %s", key)
			}
		}
	}
}

func mustJSON(page body) string {
	raw, _ := json.Marshal(page)
	return string(raw)
}
