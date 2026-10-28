package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/migrate"
)

// MigrationsDir finds backend/db/migrations by walking up to the module root.
func MigrationsDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", errors.New("diretório de migrações indisponível")
	}
	dir := wd
	for {
		candidate := filepath.Join(dir, "db", "migrations")
		info, statErr := os.Stat(candidate)
		if statErr == nil && info.IsDir() {
			if _, modErr := os.Stat(filepath.Join(dir, "go.mod")); modErr == nil {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("diretório de migrações indisponível")
		}
		dir = parent
	}
}

// ApplyMigrations runs versioned goose SQL as chatisp_owner.
func ApplyMigrations(ctx context.Context, ownerURL, dir string) error {
	cfg, err := config.Load([]string{
		"CHATISP_ENV=test",
		"CHATISP_DATABASE_URL=" + ownerURL,
		"CHATISP_MIGRATIONS_DIR=" + dir,
	})
	if err != nil {
		return errors.New("configuração de migração inválida")
	}
	if err := migrate.Apply(ctx, cfg, nil); err != nil {
		return err
	}
	return nil
}
