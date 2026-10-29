package catalog

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testEnv.AdminURL)
	if err != nil {
		t.Fatal("falha ao conectar como administrador do teste")
	}
	return conn
}
