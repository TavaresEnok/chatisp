package sectors

import (
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationAndPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
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
	pools, e := db.OpenPools(ctx, urls)
	if e != nil {
		t.Fatal(e)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	dir, e := db.MigrationsDir()
	if e != nil {
		t.Fatal(e)
	}
	tmp := t.TempDir()
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range entries {
		if f.Name() >= "00009" || filepath.Ext(f.Name()) != ".sql" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, f.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(tmp, f.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.ApplyMigrations(ctx, urls.Owner, tmp); e != nil {
		t.Fatal(e)
	}
	tenant, user, member, inbox, connector := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	contact, identity, conversation := uuid.New(), uuid.New(), uuid.New()
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
		if _, e := tx.Exec(c, `INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, connector, tenant, connector.String()); e != nil {
			return e
		}
		if _, e := tx.Exec(c, `INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, inbox, tenant, connector); e != nil {
			return e
		}
		if _, e := tx.Exec(c, `INSERT INTO contacts(id,tenant_id) VALUES($1,$2)`, contact, tenant); e != nil {
			return e
		}
		if _, e := tx.Exec(c, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, identity.String()); e != nil {
			return e
		}
		_, e := tx.Exec(c, `INSERT INTO conversations(id,tenant_id,summary,inbox_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,'legado',$3,$4,$5,'P-1','202610',1)`, conversation, tenant, inbox, contact, identity)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "00009_m3_sectors.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(tmp, "00009_m3_sectors.sql"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = db.ApplyMigrations(ctx, urls.Owner, tmp); e != nil {
		t.Fatal(e)
	}
	p := Principal{TenantID: tenant, UserID: user, MembershipID: member, Role: "agent"}
	svc := New(store)
	list, e := svc.List(ctx, p)
	if e != nil || len(list) != 1 || !list[0].LegacyDefault {
		t.Fatalf("backfill: %+v %v", list, e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		ok, e := CanAccessInbox(c, tx, p, inbox)
		if e != nil {
			return e
		}
		if !ok {
			t.Error("backfill de acesso ausente")
		}
		ok, e = CanAccessConversation(c, tx, p, inbox, conversation)
		if e != nil {
			return e
		}
		if !ok {
			t.Error("conversa legada inacessível")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Create(ctx, p, "negado"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("agent administra: %v", e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET role='admin' WHERE tenant_id=$1 AND id=$2`, tenant, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	p.Role = "admin"
	s, e := svc.Create(ctx, p, "Financeiro")
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Bind(ctx, p, s.ID, inbox, "inbox", true); e != nil {
		t.Fatal(e)
	}
	if e = svc.Bind(ctx, p, s.ID, member, "membership", true); e != nil {
		t.Fatal(e)
	}
	if e = svc.SetDefault(ctx, p, s.ID, inbox); e != nil {
		t.Fatal(e)
	}
	details, e := svc.Details(ctx, p, s.ID)
	if e != nil || len(details.InboxIDs) != 1 || len(details.MembershipIDs) != 1 || len(details.DefaultInboxIDs) != 1 {
		t.Fatalf("vínculos: %+v %v", details, e)
	}
	if e = svc.Bind(ctx, p, s.ID, uuid.New(), "membership", true); !errors.Is(e, ErrNotFound) {
		t.Fatalf("vínculo desconhecido: %v", e)
	}
	if e = svc.Bind(ctx, p, s.ID, inbox, "inbox", false); !errors.Is(e, ErrConflict) {
		t.Fatalf("remoção padrão: %v", e)
	}
	if e = svc.Bind(ctx, p, list[0].ID, member, "membership", false); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET role='agent' WHERE tenant_id=$1 AND id=$2`, tenant, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	p.Role = "agent"
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		box, e := CanAccessInbox(c, tx, p, inbox)
		if e != nil {
			return e
		}
		conv, e := CanAccessConversation(c, tx, p, inbox, conversation)
		if e != nil {
			return e
		}
		if !box || conv {
			t.Errorf("interseção caixa=%v conversa=%v", box, conv)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET role='supervisor' WHERE tenant_id=$1 AND id=$2`, tenant, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	p.Role = "supervisor"
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		ok, e := CanAccessConversation(c, tx, p, inbox, conversation)
		if e != nil {
			return e
		}
		if !ok {
			t.Error("supervisor sem acesso")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	other := p
	other.TenantID = uuid.New()
	if e = store.WithTenantTx(ctx, other.TenantID, func(c context.Context, tx pgx.Tx) error { _, e := CanAccessInbox(c, tx, other, inbox); return e }); !errors.Is(e, ErrForbidden) {
		t.Fatalf("tenant cruzado: %v", e)
	}
	p.IsSuperadmin = true
	if _, e = svc.List(ctx, p); !errors.Is(e, ErrForbidden) {
		t.Fatalf("superadmin operador: %v", e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(c, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action LIKE 'sector.%'`, tenant).Scan(&n); e != nil {
			return e
		}
		if n != 5 {
			t.Errorf("auditoria: %d", n)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// Concurrent revocation: the policy pins authorization until the consumer commits.
	legacy := list[0].ID
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(c, `UPDATE memberships SET role='agent' WHERE tenant_id=$1 AND id=$2`, tenant, member); e != nil {
			return e
		}
		_, e := tx.Exec(c, `INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, legacy, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	p.Role = "agent"
	p.IsSuperadmin = false
	revoked := make(chan error, 1)
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		if e := RequireConversation(c, tx, p, inbox, conversation); e != nil {
			return e
		}
		go func() {
			revoked <- store.WithTenantTx(ctx, tenant, func(c context.Context, tx2 pgx.Tx) error {
				_, e := tx2.Exec(c, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2 AND membership_id=$3`, tenant, legacy, member)
				return e
			})
		}()
		select {
		case e := <-revoked:
			t.Errorf("revogação não esperou o consumidor: %v", e)
		case <-time.After(400 * time.Millisecond):
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = <-revoked; e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		return RequireConversation(c, tx, p, inbox, conversation)
	}); !errors.Is(e, ErrForbidden) {
		t.Fatalf("pós-revogação: %v", e)
	}
	// Audit rows identify sector and target.
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(c, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND ((action='sector.membership.add sector='||$2 AND target_type='membership' AND target_id=$3) OR (action='sector.membership.remove sector='||$4 AND target_type='membership' AND target_id=$3) OR (action='sector.inbox.add sector='||$2 AND target_type='inbox' AND target_id=$5) OR (action='sector.default sector='||$2||' previous='||$4 AND target_type='inbox' AND target_id=$5))`, tenant, s.ID.String(), member, legacy.String(), inbox).Scan(&n); e != nil {
			return e
		}
		if n != 4 {
			t.Errorf("auditoria detalhada: %d", n)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// Suspension ordering: a mutation waits for the tenant gate and then is refused.
	if e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET role='admin' WHERE tenant_id=$1 AND id=$2`, tenant, member)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	p.Role = "admin"
	started, release, created := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	suspended := make(chan error, 1)
	go func() {
		suspended <- store.WithPlatform(ctx, func(c context.Context) error {
			if _, e := store.SetTenantStatus(c, tenant, "suspended"); e != nil {
				return e
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	go func() { _, e := svc.Create(ctx, p, "Suspenso"); created <- e }()
	select {
	case e := <-created:
		t.Errorf("mutação não esperou suspensão: %v", e)
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if e = <-suspended; e != nil {
		t.Fatal(e)
	}
	if e = <-created; !errors.Is(e, ErrForbidden) {
		t.Fatalf("mutação após suspensão: %v", e)
	}
}
