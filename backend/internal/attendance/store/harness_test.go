package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

var (
	testEnv   *testdb.Env
	testPools *db.Pools
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
	pools, err := db.OpenPools(ctx, db.RoleURLs{
		App:      urls.App,
		Platform: urls.Platform,
		Ingress:  urls.Ingress,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	testEnv = env
	testPools = pools
	testStore = db.NewStore(pools.App, pools.Platform)

	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type world struct {
	tenant     uuid.UUID
	user       uuid.UUID
	membership uuid.UUID
	connector  uuid.UUID
	inbox      uuid.UUID
	contact    uuid.UUID
	identity   uuid.UUID
}

func seedWorld(t *testing.T) world {
	t.Helper()
	ctx := context.Background()
	w := world{
		tenant:     uuid.New(),
		user:       uuid.New(),
		membership: uuid.New(),
		connector:  uuid.New(),
		inbox:      uuid.New(),
		contact:    uuid.New(),
		identity:   uuid.New(),
	}
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       w.tenant,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
			Limits:   json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{
			ID:     w.user,
			Email:  fmt.Sprintf("u-%s@example.com", w.user),
			Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     w.membership,
			UserID: w.user,
			Role:   "agent",
			Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, w.connector, w.tenant, "origin-"+w.connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1, $2, $3, 'Caixa')
		`, w.inbox, w.tenant, w.connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Contato')
		`, w.contact, w.tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, w.identity, w.tenant, w.contact, w.connector, "wa-"+w.identity.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func pgMessage(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message
	}
	return ""
}
