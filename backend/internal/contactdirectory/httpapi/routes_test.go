package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatisp/internal/contactdirectory"
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

// testVisibility is deliberately only a test double, never a product policy.
type testVisibility struct{ denied uuid.UUID }

func (v testVisibility) Contact(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID) (bool, error) {
	var yes bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE tenant_id=$1 AND id=$2 AND created_by_membership_id=$3)`, p.TenantID, id, p.MembershipID).Scan(&yes)
	return yes, e
}
func (v testVisibility) Conversation(_ context.Context, _ pgx.Tx, _ auth.Principal, _ uuid.UUID, id uuid.UUID) (bool, error) {
	return id != v.denied, nil
}

type actor struct {
	p           auth.Principal
	token, csrf string
}

func TestDirectoryPGHTTP(t *testing.T) {
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
	seed := func() actor {
		a := actor{p: auth.Principal{TenantID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), Role: "agent"}, token: uuid.NewString(), csrf: uuid.NewString()}
		e := store.WithPlatform(ctx, func(c context.Context) error {
			if _, e := store.CreateTenant(c, db.NewTenant{ID: a.p.TenantID, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
				return e
			}
			_, e := store.CreateUser(c, db.NewUser{ID: a.p.UserID, Email: fmt.Sprintf("%s@example.com", a.p.UserID), Status: "active"})
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		e = store.WithTenant(ctx, a.p.TenantID, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: a.p.MembershipID, UserID: a.p.UserID, Role: "agent", Status: "active"})
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now()
		e = store.WithAuth(ctx, func(c context.Context) error {
			return store.InsertSession(c, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(a.token), UserID: a.p.UserID, TenantID: a.p.TenantID, MembershipID: a.p.MembershipID, CSRFHash: auth.HashSecret(a.csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
		})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a := seed()
	other := seed()
	denied := uuid.New()
	svc := contactdirectory.Service{DB: store, Visibility: testVisibility{denied}}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	api := humachi.New(router, cfg)
	Register(api, Dependencies{Auth: identity, Contacts: svc, TrustedOrigins: []string{"https://panel.example"}})
	call := func(method, path, body string, who actor, origin, csrf string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: who.token})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d, expected %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	base := "/api/panel/contacts"
	origin := "https://panel.example"
	body := `{"name":"Ana Silva","phone":"+55 (11) 99999-9999","email":"ANA@example.com","notes":"privado"}`
	call("POST", base, body, a, "https://evil.example", a.csrf, 403)
	call("POST", base, body, a, origin, "bad", 403)
	var c contactdirectory.Contact
	if e = json.Unmarshal(call("POST", base, body, a, origin, a.csrf, 201), &c); e != nil {
		t.Fatal(e)
	}
	if c.Phone == nil || *c.Phone != "+5511999999999" || c.Email == nil || *c.Email != "ana@example.com" {
		t.Fatalf("normalização: %+v", c)
	}
	item := base + "/" + c.ID.String()
	call("GET", item, "", other, "", "", 404)
	call("PUT", item, `{"name":"Ana Nova","phone":"+5511999999999","notes":"nova"}`, a, origin, a.csrf, 200)
	call("PUT", item+"/block", `{"blocked":true,"reason":"abuso"}`, a, origin, a.csrf, 200)
	e = store.WithTenantTx(ctx, a.p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		b, e := contactdirectory.IsBlocked(ctx, tx, a.p.TenantID, c.ID)
		if e != nil {
			return e
		}
		if !b {
			return errors.New("bloqueio ausente")
		}
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND target_id=$2`, a.p.TenantID, c.ID).Scan(&n); e != nil {
			return e
		}
		if n != 3 {
			return fmt.Errorf("audit count %d", n)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	call("PUT", item+"/block", `{"blocked":false,"reason":"revisão"}`, a, origin, a.csrf, 200)
	var page contactdirectory.Page
	if e = json.Unmarshal(call("GET", base+"?q=99999", "", a, "", "", 200), &page); e != nil || len(page.Items) != 1 {
		t.Fatalf("busca: %+v %v", page, e)
	}
	var duplicate contactdirectory.Contact
	if e = json.Unmarshal(call("POST", base, body, a, origin, a.csrf, 201), &duplicate); e != nil || duplicate.ID == c.ID {
		t.Fatalf("contatos distintos: %+v %v", duplicate, e)
	}
	e = store.WithTenantTx(ctx, a.p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var conversations, identities int
		if e := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM conversations), (SELECT count(*) FROM contact_channel_identities)`).Scan(&conversations, &identities); e != nil {
			return e
		}
		if conversations != 0 || identities != 0 {
			return errors.New("cadastro iniciou conversa ou identidade")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = (contactdirectory.Service{DB: store}).Get(ctx, a.p, c.ID); !errors.Is(e, contactdirectory.ErrUnavailable) {
		t.Fatalf("policy ausente: %v", e)
	}
	// Two real conversation rows on one contact; the double excludes one independently.
	visible := uuid.New()
	e = store.WithTenantTx(ctx, a.p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		conn, inbox, ident := uuid.New(), uuid.New(), uuid.New()
		queries := []struct {
			sql  string
			args []any
		}{{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{conn, a.p.TenantID, conn.String()}}, {`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'test')`, []any{inbox, a.p.TenantID, conn}}, {`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,'test')`, []any{ident, a.p.TenantID, c.ID, conn}}, {`INSERT INTO conversations(id,tenant_id,inbox_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,'closed','one','202610',1),($6,$2,$3,$4,$5,'closed','two','202610',1)`, []any{visible, a.p.TenantID, inbox, c.ID, ident, denied}}}
		for _, q := range queries {
			if _, e := tx.Exec(ctx, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Channel contact without phone stays editable and keeps phone absent.
	chID := uuid.New()
	e = store.WithTenantTx(ctx, a.p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name,created_by_membership_id) VALUES($1,$2,'Canal',$3)`, chID, a.p.TenantID, a.p.MembershipID)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	var ch contactdirectory.Contact
	if e = json.Unmarshal(call("PUT", base+"/"+chID.String(), `{"name":"Canal","email":"c@example.com","notes":"obs"}`, a, origin, a.csrf, 200), &ch); e != nil || ch.Phone != nil || ch.Email == nil || ch.Notes != "obs" {
		t.Fatalf("contato de canal: %+v %v", ch, e)
	}
	call("PUT", base+"/"+chID.String(), `{"name":"Canal","phone":"abc"}`, a, origin, a.csrf, 422)
	// Missing/null blocked must not mutate nor audit.
	call("PUT", item+"/block", `{"blocked":true,"reason":"abuso"}`, a, origin, a.csrf, 200)
	call("PUT", item+"/block", `{"reason":"abuso"}`, a, origin, a.csrf, 422)
	call("PUT", item+"/block", `{"blocked":null,"reason":"abuso"}`, a, origin, a.csrf, 422)
	call("PUT", item+"/block", `{"blocked":false}`, a, origin, a.csrf, 422)
	call("PUT", item+"/block", `{"blocked":false,"reason":""}`, a, origin, a.csrf, 422)
	e = store.WithTenantTx(ctx, a.p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		b, e := contactdirectory.IsBlocked(ctx, tx, a.p.TenantID, c.ID)
		if e != nil || !b {
			return fmt.Errorf("bloqueio perdido %v %v", b, e)
		}
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_id=$1 AND action='contact.unblock'`, c.ID).Scan(&n); e != nil || n != 1 {
			return fmt.Errorf("auditoria unblock %d %v", n, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	var history contactdirectory.HistoryPage
	if e = json.Unmarshal(call("GET", item+"/history", "", a, "", "", 200), &history); e != nil || len(history.Items) != 1 || history.Items[0].ID != visible {
		t.Fatalf("histórico: %+v %v", history, e)
	}
}
