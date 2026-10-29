// Command devdb prepares a local PostgreSQL for scripts/dev.sh.
// It prints KEY=VALUE lines on stdout. Secrets stay out of stderr.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "devdb: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "prepare" {
		return errors.New("uso: devdb prepare -url postgresql://postgres@127.0.0.1:porta/postgres?sslmode=disable")
	}
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	adminURL := flags.String("url", "", "DSN do superusuário local")
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("argumentos inválidos")
	}
	if *adminURL == "" {
		return errors.New("informe -url")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	secrets, err := db.NewRoleSecrets()
	if err != nil {
		return err
	}
	urls, err := db.Bootstrap(ctx, *adminURL, secrets)
	if err != nil {
		return err
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		return err
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		return err
	}
	email, password, seeded, err := ensureDevUser(ctx, urls)
	if err != nil {
		return err
	}
	fmt.Printf("CHATISP_DATABASE_URL=%s\n", urls.Owner)
	fmt.Printf("CHATISP_APP_DATABASE_URL=%s\n", urls.App)
	fmt.Printf("CHATISP_PLATFORM_DATABASE_URL=%s\n", urls.Platform)
	fmt.Printf("CHATISP_DEV_EMAIL=%s\n", email)
	if seeded {
		fmt.Printf("CHATISP_DEV_PASSWORD=%s\n", password)
		fmt.Printf("CHATISP_DEV_SEEDED=1\n")
	} else {
		fmt.Printf("CHATISP_DEV_SEEDED=0\n")
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

	password, err := randomPassword()
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

func randomPassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("falha ao gerar senha local")
	}
	return hex.EncodeToString(buf), nil
}
