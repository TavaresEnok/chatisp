package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
)

const (
	defaultDevDataKeys       = "1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE="
	defaultDevDataKeyVersion = "1"
)

func devPrepare(parent context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("dev-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	adminURL := flags.String("url", "", "DSN do superusuário local")
	if err := flags.Parse(args); err != nil {
		return errors.New("argumentos inválidos para dev-prepare")
	}
	if *adminURL == "" {
		return errors.New("informe -url para dev-prepare")
	}

	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	urls, jobsURL, err := prepareDevDatabase(ctx, *adminURL)
	if err != nil {
		return err
	}

	email, password, seeded, err := ensureDevUser(ctx, urls)
	if err != nil {
		return fmt.Errorf("falha ao configurar usuário de desenvolvimento: %w", err)
	}

	fmt.Fprintf(stdout, "CHATISP_APP_DATABASE_URL=%s\n", urls.App)
	fmt.Fprintf(stdout, "CHATISP_PLATFORM_DATABASE_URL=%s\n", urls.Platform)
	fmt.Fprintf(stdout, "CHATISP_INGRESS_DATABASE_URL=%s\n", urls.Ingress)
	fmt.Fprintf(stdout, "CHATISP_JOBS_DATABASE_URL=%s\n", jobsURL)
	fmt.Fprintf(stdout, "CHATISP_DATA_KEYS=%s\n", defaultDevDataKeys)
	fmt.Fprintf(stdout, "CHATISP_DATA_KEY_VERSION=%s\n", defaultDevDataKeyVersion)
	fmt.Fprintf(stdout, "CHATISP_DEV_EMAIL=%s\n", email)
	if seeded {
		fmt.Fprintf(stdout, "CHATISP_DEV_PASSWORD=%s\n", password)
		fmt.Fprintf(stdout, "CHATISP_DEV_SEEDED=1\n")
	} else {
		fmt.Fprintf(stdout, "CHATISP_DEV_SEEDED=0\n")
	}
	return nil
}

func ensureDevUser(ctx context.Context, urls db.RoleURLs) (string, string, bool, error) {
	const email = "dev.local@example.com"
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform})
	if err != nil {
		return "", "", false, err
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)

	var exists bool
	err = store.WithPlatform(ctx, func(ctx context.Context) error {
		_, findErr := store.FindUserByEmail(ctx, email)
		if findErr == nil {
			exists = true
			return nil
		}
		if !errors.Is(findErr, db.ErrNotFound) {
			return findErr
		}
		return nil
	})
	if err != nil {
		return "", "", false, err
	}
	if exists {
		return email, "", false, nil
	}

	password, err := randomHex(12)
	if err != nil {
		return "", "", false, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", "", false, err
	}
	tenantID := uuid.New()
	userID := uuid.New()
	if err := store.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := store.CreateTenant(ctx, db.NewTenant{
			ID:       tenantID,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
		}); err != nil {
			return err
		}
		_, err := store.CreateUser(ctx, db.NewUser{ID: userID, Email: email, Status: "active"})
		return err
	}); err != nil {
		return "", "", false, err
	}
	if err := store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := store.CreateMembership(ctx, db.NewMembership{
			ID:     uuid.New(),
			UserID: userID,
			Role:   "agent",
			Status: "active",
		})
		return err
	}); err != nil {
		return "", "", false, err
	}
	if err := store.WithAuth(ctx, func(ctx context.Context) error {
		return store.SetPasswordHash(ctx, userID, hash)
	}); err != nil {
		return "", "", false, err
	}
	return email, password, true, nil
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("falha ao gerar segredo aleatório")
	}
	return hex.EncodeToString(buf), nil
}

// prepareDevDatabase creates the roles with fresh passwords, applies the migrations and returns
// the role URLs and the River URL. It does not create any user.
func prepareDevDatabase(ctx context.Context, adminURL string) (db.RoleURLs, string, error) {
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		return db.RoleURLs{}, "", err
	}
	jobsPassword, err := randomHex(16)
	if err != nil {
		return db.RoleURLs{}, "", err
	}

	urls, err := db.Bootstrap(ctx, adminURL, secrets)
	if err != nil {
		return db.RoleURLs{}, "", fmt.Errorf("falha ao preparar papéis de banco: %w", err)
	}

	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return db.RoleURLs{}, "", fmt.Errorf("falha ao conectar como superusuário: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'chatisp_jobs')`).Scan(&exists); err != nil {
		return db.RoleURLs{}, "", err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format('%s ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text, $2::text)
	`, verb, jobsPassword).Scan(&stmt); err != nil {
		return db.RoleURLs{}, "", err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return db.RoleURLs{}, "", fmt.Errorf("falha ao provisionar papel chatisp_jobs: %w", err)
	}
	for _, query := range []string{
		`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`,
	} {
		var grant string
		if err := conn.QueryRow(ctx, query).Scan(&grant); err != nil {
			return db.RoleURLs{}, "", err
		}
		if _, err := conn.Exec(ctx, grant); err != nil {
			return db.RoleURLs{}, "", err
		}
	}

	u, err := url.Parse(urls.App)
	if err != nil {
		return db.RoleURLs{}, "", err
	}
	u.User = url.UserPassword("chatisp_jobs", jobsPassword)
	jobsURL := u.String()

	migrationsDir, err := db.MigrationsDir()
	if err != nil {
		return db.RoleURLs{}, "", err
	}
	migrateCfg, err := config.Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=" + urls.Owner,
		"CHATISP_MIGRATIONS_DIR=" + migrationsDir,
	})
	if err != nil {
		return db.RoleURLs{}, "", err
	}
	if err := applyMigrations(ctx, migrateCfg, nil); err != nil {
		return db.RoleURLs{}, "", fmt.Errorf("falha ao aplicar migrações (domínio e River): %w", err)
	}

	return urls, jobsURL, nil
}
