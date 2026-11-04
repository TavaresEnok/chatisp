package publicapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

var (
	testStore *db.Store
	testAPI   http.Handler
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
	testStore = db.NewStore(pools.App, pools.Platform)
	testAPI = NewHandler(testStore)
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type fixture struct {
	tenant   uuid.UUID
	conv     uuid.UUID
	contact  uuid.UUID
	protocol string
	secret   string
	tokenID  uuid.UUID
}

func seedConversation(t *testing.T, limit int) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{
		tenant:   uuid.New(),
		conv:     uuid.New(),
		contact:  uuid.New(),
		protocol: "P-" + uuid.NewString()[:8],
	}
	userID, memberID := uuid.New(), uuid.New()
	connector, inbox, sector, identity := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: f.tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: userID, Email: fmt.Sprintf("pub-%s@example.com", userID), Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, f.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: memberID, UserID: userID, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	note := "nota-interna-secreta"
	publicBody := "mensagem-publica-visivel"
	obs := "observacao-interna-do-contato"
	credential := "credencial-secreta-do-canal"
	document := []byte("DOCUMENTO-COMPLETO-NAO-EXIBIR!!")
	hmac := sha256.Sum256([]byte(f.contact.String()))
	if err := testStore.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled, credential_ref) VALUES ($1,$2,'meta_cloud',$3,'connected',true,$4)`, connector, f.tenant, "origin-"+connector.String(), credential); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,'WhatsApp')`, inbox, f.tenant, connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Suporte',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, f.tenant, sector, inbox, memberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name, notes) VALUES ($1,$2,'Cliente',$3)`, f.contact, f.tenant, obs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity, f.tenant, f.contact, connector, "wa-"+identity.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversations (id,tenant_id,inbox_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES ($1,$2,$3,$4,$5,'open','menu',$6,'202611',1)`, f.conv, f.tenant, inbox, f.contact, identity, f.protocol); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body) VALUES ($1,$2,$3,$4,'inbound','customer','public',$5)`, uuid.New(), f.tenant, f.conv, connector, publicBody); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id,tenant_id,conversation_id,connector_id,direction,origin,visibility,body) VALUES ($1,$2,$3,$4,'outbound','note','internal',$5)`, uuid.New(), f.tenant, f.conv, connector, note); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_documents (tenant_id,contact_id,kind,key_version,ciphertext,document_hmac,version,updated_by_membership_id) VALUES ($1,$2,'cpf','v1',$3,$4,1,$5)`, f.tenant, f.contact, document, hmac[:], memberID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	secret, id, err := Issue(ctx, testStore, f.tenant, limit)
	if err != nil {
		t.Fatal(err)
	}
	f.secret, f.tokenID = secret, id
	return f
}

func call(method, path, secret string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "198.51.100.20:1234"
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-do-painel-nao-serve"})
	}
	rec := httptest.NewRecorder()
	testAPI.ServeHTTP(rec, req)
	return rec
}

func detail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Detail
}

func TestTokenRevogado(t *testing.T) {
	f := seedConversation(t, 30)
	if err := Revoke(context.Background(), testStore, f.tokenID); err != nil {
		t.Fatal(err)
	}
	rec := call(http.MethodGet, "/v1/conversations/"+f.conv.String(), f.secret, true)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d corpo %s", rec.Code, rec.Body.String())
	}
	if detail(t, rec) != "token revogado" {
		t.Fatalf("detalhe %s", rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, f.protocol) || strings.Contains(body, "mensagem-publica-visivel") || strings.Contains(body, "nota-interna-secreta") {
		t.Fatalf("resposta de token revogado devolveu dado: %s", body)
	}
}

func TestTenantSuspenso(t *testing.T) {
	f := seedConversation(t, 30)
	if err := testStore.WithPlatform(context.Background(), func(ctx context.Context) error {
		_, err := testStore.SetTenantStatus(ctx, f.tenant, "suspended")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/conversations/" + f.protocol,
		"/v1/conversations/" + f.conv.String() + "/messages",
		"/v1/conversations/" + f.conv.String() + "/contact",
	} {
		rec := call(http.MethodGet, path, f.secret, false)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status %d corpo %s", path, rec.Code, rec.Body.String())
		}
		if detail(t, rec) != "provedor suspenso" {
			t.Fatalf("%s detalhe %s", path, rec.Body.String())
		}
		body := rec.Body.String()
		for _, leak := range []string{f.protocol, "mensagem-publica-visivel", "nota-interna-secreta", "observacao-interna-do-contato", "DOCUMENTO-COMPLETO-NAO-EXIBIR", "credencial-secreta-do-canal", "Cliente"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s vazou %q em %s", path, leak, body)
			}
		}
	}
}

func TestNotaInternaAusente(t *testing.T) {
	f := seedConversation(t, 30)
	paths := []string{
		"/v1/conversations/" + f.conv.String(),
		"/v1/conversations/" + f.protocol + "/messages",
		"/v1/conversations/" + f.protocol + "/contact",
	}
	var sawPublic bool
	for _, path := range paths {
		rec := call(http.MethodGet, path, f.secret, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status %d corpo %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, leak := range []string{"nota-interna-secreta", "observacao-interna-do-contato", "DOCUMENTO-COMPLETO-NAO-EXIBIR", "credencial-secreta-do-canal", "ciphertext", "document_hmac"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s contém %q: %s", path, leak, body)
			}
		}
		if strings.Contains(path, "/messages") {
			if !strings.Contains(body, "mensagem-publica-visivel") {
				t.Fatalf("mensagem pública ausente: %s", body)
			}
			sawPublic = true
			var list messageList
			if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Messages) != 1 || list.Messages[0].Body != "mensagem-publica-visivel" {
				t.Fatalf("mensagens %#v", list.Messages)
			}
		}
		if strings.Contains(path, "/contact") {
			var view contactView
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if view.ID != f.contact || view.Name != "Cliente" {
				t.Fatalf("contato %#v", view)
			}
		}
	}
	if !sawPublic {
		t.Fatal("não leu mensagens")
	}
}

func TestLimite429(t *testing.T) {
	f := seedConversation(t, 1)
	path := "/v1/conversations/" + f.conv.String() + "/messages"
	first := call(http.MethodGet, path, f.secret, false)
	if first.Code != http.StatusOK {
		t.Fatalf("primeira %d %s", first.Code, first.Body.String())
	}
	second := call(http.MethodGet, path, f.secret, false)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("segunda %d %s", second.Code, second.Body.String())
	}
	if detail(t, second) != "limite do token excedido" {
		t.Fatalf("detalhe %s", second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After ausente")
	}
	if strings.Contains(second.Body.String(), "mensagem-publica-visivel") || strings.Contains(second.Body.String(), "nota-interna-secreta") {
		t.Fatalf("429 devolveu dado: %s", second.Body.String())
	}
}

func TestCookieNaoSubstituiBearer(t *testing.T) {
	f := seedConversation(t, 10)
	rec := call(http.MethodGet, "/v1/conversations/"+f.conv.String(), "", true)
	if rec.Code != http.StatusUnauthorized || detail(t, rec) != "credencial ausente" {
		t.Fatalf("cookie sozinho: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), f.protocol) {
		t.Fatal("cookie devolveu conversa")
	}
}
