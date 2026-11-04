package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/retention"
)

var (
	testStore *db.Store
	testEnv   *testdb.Env
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	testEnv = env
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
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type seeded struct {
	tenant   uuid.UUID
	contact  uuid.UUID
	admin    retention.Principal
	agent    retention.Principal
	outsider retention.Principal
	inbox    uuid.UUID
}

func seedTenant(t *testing.T) seeded {
	t.Helper()
	ctx := context.Background()
	s := seeded{tenant: uuid.New(), inbox: uuid.New(), contact: uuid.New()}
	adminUser, agentUser, outsiderUser := uuid.New(), uuid.New(), uuid.New()
	s.admin = retention.Principal{TenantID: s.tenant, UserID: adminUser, MembershipID: uuid.New(), Role: "admin"}
	s.agent = retention.Principal{TenantID: s.tenant, UserID: agentUser, MembershipID: uuid.New(), Role: "agent"}
	s.outsider = retention.Principal{TenantID: s.tenant, UserID: outsiderUser, MembershipID: uuid.New(), Role: "agent"}
	connector, identity, identity2 := uuid.New(), uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: s.tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		for _, user := range []struct {
			id   uuid.UUID
			mail string
		}{
			{adminUser, fmt.Sprintf("admin-%s@example.com", adminUser)},
			{agentUser, fmt.Sprintf("agent-%s@example.com", agentUser)},
			{outsiderUser, fmt.Sprintf("out-%s@example.com", outsiderUser)},
		} {
			if _, err := testStore.CreateUser(ctx, db.NewUser{ID: user.id, Email: user.mail, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, s.tenant, func(ctx context.Context) error {
		for _, member := range []retention.Principal{s.admin, s.agent, s.outsider} {
			if _, err := testStore.CreateMembership(ctx, db.NewMembership{ID: member.MembershipID, UserID: member.UserID, Role: member.Role, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, s.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled) VALUES ($1,$2,'meta_cloud',$3,'connected',true)`, connector, s.tenant, "origin-"+connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1,$2,$3,'WhatsApp')`, s.inbox, s.tenant, connector); err != nil {
			return err
		}
		sector := uuid.New()
		if _, err := tx.Exec(ctx, `WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Suporte',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, s.tenant, sector, s.inbox, s.agent.MembershipID); err != nil {
			return err
		}
		other := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'Outro')`, s.tenant, other); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_memberships VALUES ($1,$2,$3)`, s.tenant, other, s.outsider.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1,$2,'Cliente')`, s.contact, s.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity, s.tenant, s.contact, connector, "wa-"+identity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`, identity2, s.tenant, s.contact, connector, "wa-"+identity2.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func setLimits(t *testing.T, tenant uuid.UUID, limits string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `UPDATE tenants SET limits = $2::jsonb WHERE id = $1`, tenant, limits)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("limites: %d", tag.RowsAffected())
	}
}
