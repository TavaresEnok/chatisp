package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
)

var (
	testEnv   *testdb.Env
	testPools *db.Pools
	testStore *db.Store
	testJobs  *jobs.Client
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
	password, err := randomPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papel de jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := ensureJobsRole(ctx, env.AdminURL, password); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao criar papel de jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{Owner: urls.Owner, App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}
	if err := jobs.Migrate(ctx, pools.Owner); err != nil {
		fmt.Fprintf(os.Stderr, "falha na migração River: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}
	client, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao montar jobs: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}
	testEnv = env
	testPools = pools
	testStore = db.NewStore(pools.App, pools.Platform)
	testJobs = client
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
		tenant: uuid.New(), user: uuid.New(), membership: uuid.New(),
		connector: uuid.New(), inbox: uuid.New(), contact: uuid.New(), identity: uuid.New(),
	}
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID: w.tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: w.user, Email: fmt.Sprintf("u-%s@example.com", w.user), Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: w.membership, UserID: w.user, Role: "agent", Status: "active"})
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
		if _, err := tx.Exec(ctx, `WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Padrão',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, w.tenant, uuid.New(), w.inbox, w.membership); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Contato')`, w.contact, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, w.identity, w.tenant, w.contact, w.connector, "wa-"+w.identity.String()); err != nil {
			return err
		}
		return seedMetaCloud(ctx, tx, w.tenant, w.connector, w.inbox)
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func seedMember(t *testing.T, tenant uuid.UUID, role string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	user := uuid.New()
	membership := uuid.New()
	ctx := context.Background()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: membership, UserID: user, Role: role, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sector_memberships SELECT tenant_id,id,$2 FROM sectors WHERE tenant_id=$1 AND legacy_default`, tenant, membership)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return user, membership
}

func principal(w world) Principal {
	return Principal{UserID: w.user, MembershipID: w.membership, TenantID: w.tenant, Role: "agent"}
}

func live(queuer Queuer) Service {
	if queuer == nil {
		queuer = testJobs
	}
	return New(testStore, queuer)
}

// fixtureBase stays inside the official 24h window for the whole process.
// at remains comparable across calls. It is not time.Now() on every call.
var fixtureBase = time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)

func at(minute int) time.Time {
	return fixtureBase.Add(time.Duration(minute) * time.Minute)
}

func seedMetaCloud(ctx context.Context, tx pgx.Tx, tenant, connector, inbox uuid.UUID) error {
	app := uuid.New()
	waba := uuid.New()
	aad := "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"
	if _, err := tx.Exec(ctx, `SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, app.String(), app, []byte{1}, aad); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO meta_wabas (tenant_id, id, meta_app_id, waba_id, status) VALUES ($1, $2, $3, $4, 'active')`, tenant, waba, app, waba.String()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO meta_phone_numbers (tenant_id, id, meta_waba_id, phone_number_id, status, connector_id, inbox_id) VALUES ($1, $2, $3, $4, 'active', $5, $6)`, tenant, uuid.New(), waba, uuid.NewString(), connector, inbox)
	return err
}

func countQuery(t *testing.T, ctx context.Context, tenant uuid.UUID, query string, args ...any) int {
	t.Helper()
	var n int
	err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, args...).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func countJobs(t *testing.T, ctx context.Context, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := testPools.App.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE args->>'tenant_id' = $1`, tenant.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type failQueue struct{}

func (failQueue) EnqueueOutbound(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, errors.New("fila indisponível")
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

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
