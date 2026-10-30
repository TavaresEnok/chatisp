package acceptance

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
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatisp/internal/attendance/pipeline"
	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/notifications"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	panel "chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/realtime"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestM3AIntegrated uses the production HTTP composition and policy, real sessions,
// disposable PostgreSQL and persisted events. No external channel is contacted.
func TestM3AIntegrated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if os.Getenv("CHATISP_TEST_DATABASE_URL") != "" {
		t.Fatal("aceitação exige banco descartável")
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
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	adminDB, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	var grant string
	if err = adminDB.QueryRow(ctx, `SELECT format('GRANT CREATE ON DATABASE %I TO chatisp_owner', current_database())`).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if _, err = adminDB.Exec(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = adminDB.Exec(ctx, `CREATE ROLE chatisp_jobs NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	adminDB.Close(ctx)
	owner, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner})
	if err != nil {
		t.Fatal(err)
	}
	err = jobs.Migrate(ctx, owner.Owner)
	owner.Close()
	if err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
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
	sectorsService := sectors.New(store)
	const origin = "http://127.0.0.1:3000"
	api := panel.New(panel.Dependencies{Auth: identity, Attendance: attendance, Channels: channels, Sectors: sectorsService, Contacts: contactdirectory.Service{DB: store, Visibility: panel.ContactVisibility{}}, Notifications: notifications.New(store), TrustedOrigins: []string{origin}})
	server := httptest.NewServer(api.Handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 10 * time.Second
	call := func(a actor, method, path, body string, want int) []byte {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", a.csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: a.token})
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, resp.StatusCode, want, data)
		}
		return data
	}
	tenants := map[uuid.UUID]bool{}
	seed := func(tenant uuid.UUID, role string) actor {
		a := actor{tenant: tenant, user: uuid.New(), member: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
		if e := store.WithPlatform(ctx, func(c context.Context) error {
			if !tenants[tenant] {
				if _, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
					return e
				}
			}
			_, e := store.CreateUser(c, db.NewUser{ID: a.user, Email: a.user.String() + "@example.test", Status: "active"})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		tenants[tenant] = true
		if e := store.WithTenant(ctx, tenant, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.member, UserID: a.user, Role: role, Status: "active"})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		now := time.Now()
		session := uuid.New()
		if e := store.WithAuth(ctx, func(c context.Context) error {
			sealed, e := ring.Encrypt([]byte("simulation-secret"), crypto.Context{TenantID: tenant.String(), Entity: "user", EntityID: a.user.String(), Field: "totp_secret"})
			if e != nil {
				return e
			}
			if e = store.BeginMFA(c, a.user, tenant, sealed.Ciphertext, sealed.Version); e != nil {
				return e
			}
			if ok, e := store.ConfirmMFA(c, a.user, now); e != nil || !ok {
				return fmt.Errorf("confirm MFA: %v", e)
			}
			if e = store.InsertSession(c, db.NewSession{ID: session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}); e != nil {
				return e
			}
			return store.SetSessionAssurance(c, session, "full")
		}); e != nil {
			t.Fatal(e)
		}
		return a
	}
	tenant := uuid.New()
	admin, agent, supervisor, other := seed(tenant, "admin"), seed(tenant, "agent"), seed(tenant, "supervisor"), seed(uuid.New(), "admin")
	idFrom := func(data []byte, field string) uuid.UUID {
		t.Helper()
		var m map[string]json.RawMessage
		if e := json.Unmarshal(data, &m); e != nil {
			t.Fatal(e)
		}
		var id uuid.UUID
		if e := json.Unmarshal(m[field], &id); e != nil || id == uuid.Nil {
			t.Fatalf("missing %s: %s", field, data)
		}
		return id
	}
	configure := func(a actor) uuid.UUID {
		key := uuid.NewString()
		return idFrom(call(a, "POST", "/api/panel/channels/meta", fmt.Sprintf(`{"inbox_name":"SIMULAÇÃO","display_number":"+5511999990001","app_id":%q,"app_secret":"sim-secret","waba_id":%q,"phone_number_id":%q,"access_token":"sim-token"}`, "app-"+key, "waba-"+key, "phone-"+key), 200), "inbox_id")
	}
	box, box2, foreign := configure(admin), configure(admin), configure(other)
	createSector := func(a actor, name string) uuid.UUID {
		return idFrom(call(a, "POST", "/api/panel/sectors", fmt.Sprintf(`{"name":%q}`, name), 201), "id")
	}
	s1, s2 := createSector(admin, "Suporte"), createSector(admin, "Financeiro")
	foreignSector := createSector(other, "Outro tenant")
	bind := func(sector, target uuid.UUID, kind string, add bool) {
		t.Helper()
		method := "PUT"
		if !add {
			method = "DELETE"
		}
		call(admin, method, "/api/panel/sectors/"+sector.String()+"/"+kind+"/"+target.String(), "", 200)
	}
	for _, s := range []uuid.UUID{s1, s2} {
		bind(s, agent.member, "memberships", true)
		bind(s, box, "inboxes", true)
	}
	bind(s2, box2, "inboxes", true)
	call(admin, "PUT", "/api/panel/sectors/"+s1.String()+"/inboxes/"+box.String()+"/default", "", 200)
	call(admin, "PUT", "/api/panel/sectors/"+s2.String()+"/inboxes/"+box2.String()+"/default", "", 200)
	call(agent, "POST", "/api/panel/sectors", `{"name":"Negado"}`, 403)
	contact := idFrom(call(admin, "POST", "/api/panel/contacts", `{"name":"Pessoa comum","phone":"+5511988887777","notes":"Cadastro M3A"}`, 201), "id")
	contactPath := "/api/panel/contacts/" + contact.String()
	call(admin, "PUT", contactPath, `{"name":"Pessoa atualizada","phone":"+5511988887777","email":"pessoa@example.test","notes":"Observação atualizada"}`, 200)
	// A channel identity is fixture data; reception uses the real attendance service.
	receive := func(inbox uuid.UUID, external string) service.ReceiveResult {
		t.Helper()
		var connector uuid.UUID
		identityID := uuid.New()
		if e := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
			if e := tx.QueryRow(c, `SELECT connector_id FROM inboxes WHERE id=$1`, inbox).Scan(&connector); e != nil {
				return e
			}
			_, e := tx.Exec(c, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identityID, tenant, contact, connector, external)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		result, e := attendance.ReceiveText(ctx, service.Principal{TenantID: tenant, UserID: admin.user, MembershipID: admin.member, Role: "admin"}, service.ReceiveInput{InboxID: inbox, ContactID: contact, IdentityID: identityID, ConnectorID: connector, ExternalMessageID: external, Body: external, Period: "202610", TrustedAt: time.Now(), OpenedAt: time.Now()})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	first, second := receive(box, "suporte-autorizado"), receive(box2, "financeiro-restrito")
	base := "/api/panel/inboxes/" + box.String()
	base2 := "/api/panel/inboxes/" + box2.String()
	item := base + "/conversations/" + first.Conversation.ID.String()
	item2 := base2 + "/conversations/" + second.Conversation.ID.String()
	contains := func(data []byte, value string, want bool) {
		t.Helper()
		if bytes.Contains(data, []byte(value)) != want {
			t.Fatalf("presence %q want %v: %s", value, want, data)
		}
	}
	contains(call(agent, "GET", "/api/panel/inboxes", "", 200), foreign.String(), false)
	call(other, "GET", item+"/history", "", 403)
	call(agent, "GET", base2+"/conversations/"+first.Conversation.ID.String()+"/history", "", 404)
	contains(call(agent, "GET", contactPath+"/history", "", 200), second.Conversation.ID.String(), true)
	// Persisted events exist before revocation and must be filtered when consumed later.
	contains(call(agent, "GET", base2+"/events?after=0", "", 200), second.Conversation.ID.String(), true)
	bind(s2, agent.member, "memberships", false)
	call(agent, "GET", base2+"/conversations", "", 403)
	call(agent, "GET", item2+"/history", "", 403)
	call(agent, "POST", item2+"/assume", fmt.Sprintf(`{"expected_version":%d}`, second.Conversation.Version), 403)
	call(agent, "GET", base2+"/events?after=0", "", 403)
	contains(call(agent, "GET", contactPath+"/history", "", 200), second.Conversation.ID.String(), false)
	contains(call(agent, "GET", contactPath+"/history", "", 200), first.Conversation.ID.String(), true)
	contains(call(supervisor, "GET", contactPath+"/history", "", 200), second.Conversation.ID.String(), true)
	// A default stays protected, even when populated. Switching it explicitly
	// permits revocation without moving or deleting historical conversations.
	bind(s2, agent.member, "memberships", true)
	call(admin, "DELETE", "/api/panel/sectors/"+s2.String()+"/inboxes/"+box2.String(), "", 409)
	bind(s1, box2, "inboxes", true)
	call(admin, "PUT", "/api/panel/sectors/"+s1.String()+"/inboxes/"+box2.String()+"/default", "", 200)
	testM3AAssignmentRaces(t, ctx, store, env.AdminURL, tenant, box2, s1, s2, second.Conversation.ID)
	bind(s2, box2, "inboxes", false)
	if err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		var sector uuid.UUID
		var protocol, body string
		if err := tx.QueryRow(c, `SELECT c.sector_id,c.protocol,m.body FROM conversations c JOIN messages m ON m.tenant_id=c.tenant_id AND m.conversation_id=c.id WHERE c.tenant_id=$1 AND c.id=$2`, tenant, second.Conversation.ID).Scan(&sector, &protocol, &body); err != nil {
			return err
		}
		if sector != s2 || protocol != second.Conversation.Protocol || body != "financeiro-restrito" {
			return fmt.Errorf("histórico alterado após revogação: %s %s %q", sector, protocol, body)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Membership in both sectors and access to the box via Suporte do not
	// authorize the historical Financeiro conversation after link revocation.
	contains(call(agent, "GET", base2+"/conversations", "", 200), second.Conversation.ID.String(), false)
	call(agent, "GET", item2+"/history", "", 403)
	call(agent, "POST", item2+"/assume", fmt.Sprintf(`{"expected_version":%d}`, second.Conversation.Version), 403)
	call(supervisor, "GET", item2+"/history", "", 403)
	call(admin, "GET", item2+"/history", "", 403)
	call(agent, "GET", base+"/conversations/"+second.Conversation.ID.String()+"/history", "", 403)
	contains(call(agent, "GET", base2+"/events?after=0", "", 200), second.Conversation.ID.String(), false)
	contains(call(agent, "GET", contactPath+"/history", "", 200), second.Conversation.ID.String(), false)
	for _, target := range []uuid.UUID{s2, foreignSector, uuid.New()} {
		testM3ARejectAssignment(t, ctx, store, tenant, second.Conversation.ID, s1, target)
	}
	inactive := createSector(admin, "Inativo")
	bind(inactive, box2, "inboxes", true)
	call(admin, "PATCH", "/api/panel/sectors/"+inactive.String(), `{"active":false}`, 200)
	testM3ARejectAssignment(t, ctx, store, tenant, second.Conversation.ID, s1, inactive)
	testM3AHistoricalIntegrity(t, ctx, store, tenant, s2, second.Conversation.ID)
	bounded := realtime.NewBoundedAttendance(store, attendance)
	page, e := bounded.Events(ctx, service.Principal{TenantID: tenant, UserID: agent.user, MembershipID: agent.member, Role: "agent"}, box2, 0)
	if e != nil || len(page.Events) != 0 || page.NextCursor == 0 {
		t.Fatalf("queued socket page: %+v %v", page, e)
	}
	// Removing an empty link is supported and revokes the inbox on the next call.
	emptyBox := configure(admin)
	bind(s1, emptyBox, "inboxes", true)
	contains(call(agent, "GET", "/api/panel/inboxes", "", 200), emptyBox.String(), true)
	bind(s1, emptyBox, "inboxes", false)
	call(agent, "GET", "/api/panel/inboxes/"+emptyBox.String()+"/conversations", "", 403)
	// Blocking keeps existing history but refuses fresh inbound and outbound.
	var assumed struct {
		Version int64 `json:"version"`
	}
	if e := json.Unmarshal(call(agent, "POST", item+"/assume", fmt.Sprintf(`{"expected_version":%d}`, first.Conversation.Version), 200), &assumed); e != nil {
		t.Fatal(e)
	}
	call(admin, "PUT", contactPath+"/block", `{"blocked":true,"reason":"abuso"}`, 200)
	// The API queues the intent; the dispatch boundary must stop it before I/O.
	attempt := idFrom(call(agent, "POST", item+"/messages", fmt.Sprintf(`{"expected_version":%d,"client_request_id":"blocked-send","body":"não distribuir"}`, assumed.Version), 201), "attempt_id")
	var called atomic.Bool
	standIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		http.Error(w, "dispatch proibido", 500)
	}))
	defer standIn.Close()
	factory, err := pipeline.NewDriverFactory(pipeline.DriverFactoryConfig{Simulation: true, StandInURL: standIn.URL})
	if err != nil {
		t.Fatal(err)
	}
	channelServer := channels.Server()
	if err := pipeline.NewOutboundHandler(store, attendance, channelServer, channelServer, factory).DispatchOutbound(ctx, tenant, attempt); err != nil {
		t.Fatal(err)
	}
	if called.Load() {
		t.Fatal("contato bloqueado chegou ao driver")
	}
	if err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(c, `SELECT state FROM outbound_attempts WHERE tenant_id=$1 AND id=$2`, tenant, attempt).Scan(&state); err != nil {
			return err
		}
		if state != "failed" {
			return fmt.Errorf("blocked attempt state=%s", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var connector, identityID uuid.UUID
	if e := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		return tx.QueryRow(c, `SELECT ci.connector_id,ci.id FROM contact_channel_identities ci JOIN inboxes i ON i.tenant_id=ci.tenant_id AND i.connector_id=ci.connector_id WHERE ci.tenant_id=$1 AND ci.contact_id=$2 AND i.id=$3 LIMIT 1`, tenant, contact, box).Scan(&connector, &identityID)
	}); e != nil {
		t.Fatal(e)
	}
	_, e = attendance.ReceiveText(ctx, service.Principal{TenantID: tenant, UserID: admin.user, MembershipID: admin.member, Role: "admin"}, service.ReceiveInput{InboxID: box, ContactID: contact, IdentityID: identityID, ConnectorID: connector, ExternalMessageID: "blocked-new", Body: "blocked", Period: "202610", TrustedAt: time.Now(), OpenedAt: time.Now()})
	if !errors.Is(e, service.ErrForbidden) {
		t.Fatalf("blocked inbound: %v", e)
	}
	contains(call(agent, "GET", item+"/history", "", 200), "suporte-autorizado", true)
	call(agent, "PATCH", "/api/panel/preferences/notifications", `{"sound":false,"browser":true,"silent":true}`, 200)
	contains(call(agent, "GET", "/api/panel/preferences/notifications", "", 200), `"silent":true`, true)
	contains(call(supervisor, "GET", "/api/panel/preferences/notifications", "", 200), `"silent":false`, true)
	contains(call(other, "GET", "/api/panel/preferences/notifications", "", 200), `"silent":false`, true)
}
