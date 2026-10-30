package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/testdb"
)

var (
	testEnv        *testdb.Env
	testPools      *db.Pools
	testStore      *db.Store
	testJobs       *jobs.Client
	testAuth       *auth.Service
	testAttendance attendanceService.Service
	testNotifier   *PGNotifier
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

	pools, err := db.OpenPools(ctx, db.RoleURLs{
		Owner:    urls.Owner,
		App:      urls.App,
		Platform: urls.Platform,
		Ingress:  urls.Ingress,
	})
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

	store := db.NewStore(pools.App, pools.Platform)
	authSvc, err := auth.New(auth.Options{
		Store:  store,
		Mail:   &mail.Fake{},
		Logger: logging.New("error", io.Discard),
		Policy: auth.DefaultPolicy,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao montar auth service: %s\n", err)
		pools.Close()
		env.Close()
		os.Exit(1)
	}

	testEnv = env
	testPools = pools
	testStore = store
	testJobs = client
	testAuth = authSvc
	testAttendance = attendanceService.New(testStore, testJobs)
	testNotifier = NewPGNotifier(context.Background(), pools.App, logging.New("error", io.Discard))

	code := m.Run()

	testNotifier.Close()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type world struct {
	tenant       uuid.UUID
	user         uuid.UUID
	email        string
	membership   uuid.UUID
	role         string
	connector    uuid.UUID
	connector2   uuid.UUID
	inbox        uuid.UUID
	inbox2       uuid.UUID
	contact      uuid.UUID
	contact2     uuid.UUID
	identity     uuid.UUID
	identity2    uuid.UUID
	identity1B   uuid.UUID
	sessionToken string
	sessionID    uuid.UUID
}

func seedWorld(t *testing.T, role string) world {
	t.Helper()
	ctx := context.Background()

	w := world{
		tenant:     uuid.New(),
		user:       uuid.New(),
		membership: uuid.New(),
		role:       role,
		connector:  uuid.New(),
		connector2: uuid.New(),
		inbox:      uuid.New(),
		inbox2:     uuid.New(),
		contact:    uuid.New(),
		contact2:   uuid.New(),
		identity:   uuid.New(),
		identity2:  uuid.New(),
		identity1B: uuid.New(),
	}
	w.email = fmt.Sprintf("u-%s@example.com", w.user)

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
			Email:  w.email,
			Status: "active",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	password := "Senha-valida-1234"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		return testStore.SetPasswordHash(ctx, w.user, hash)
	}); err != nil {
		t.Fatal(err)
	}

	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     w.membership,
			UserID: w.user,
			Role:   role,
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
			INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled)
			VALUES ($1, $2, 'meta_cloud', $3, 'connected', true)
		`, w.connector2, w.tenant, "origin-"+w.connector2.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1, $2, $3, 'Caixa Principal')
		`, w.inbox, w.tenant, w.connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inboxes (id, tenant_id, connector_id, name) VALUES ($1, $2, $3, 'Caixa Secundaria')
		`, w.inbox2, w.tenant, w.connector2); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Contato')
		`, w.contact, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Contato 2')
		`, w.contact2, w.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, w.identity, w.tenant, w.contact, w.connector, "wa-"+w.identity.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, w.identity1B, w.tenant, w.contact2, w.connector, "wa-"+w.identity1B.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id)
			VALUES ($1, $2, $3, $4, $5)
		`, w.identity2, w.tenant, w.contact, w.connector2, "wa-"+w.identity2.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	issued, err := testAuth.Login(ctx, w.email, password, w.tenant.String(), "127.0.0.1")
	if err != nil {
		t.Fatalf("falha ao logar para seedWorld: %s", err)
	}
	w.sessionToken = issued.Token
	w.sessionID = issued.Principal.SessionID

	return w
}

func principal(w world) attendanceService.Principal {
	return attendanceService.Principal{
		UserID:       w.user,
		MembershipID: w.membership,
		TenantID:     w.tenant,
		Role:         w.role,
	}
}

func randomString() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
