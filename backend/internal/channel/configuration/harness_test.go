package configuration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
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
	if err := jobs.Migrate(ctx, pools.Owner); err != nil {
		fmt.Fprintf(os.Stderr, "falha na migração River: %s\n", err)
		pools.Close()
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

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testEnv.AdminURL)
	if err != nil {
		t.Fatal("falha ao conectar como administrador do teste")
	}
	return conn
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
