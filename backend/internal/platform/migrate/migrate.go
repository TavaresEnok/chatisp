// Package migrate applies versioned SQL with goose.
// Connection failures are reported without the database URL.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/logging"
)

// Apply checks the migration directory, connects, and runs pending SQL files.
// An empty directory is a successful no-op after the database accepts a ping.
func Apply(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if log == nil {
		log = logging.New(cfg.LogLevel, ioDiscard())
	}
	info, err := os.Stat(cfg.MigrationsDir)
	if err != nil || !info.IsDir() || logging.Sensitive(cfg.MigrationsDir) {
		return errors.New("diretório de migrações indisponível")
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Error("falha ao abrir conexão com o banco", "component", "migrate")
		return errors.New("falha ao conectar ao banco para migração")
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		log.Error("falha ao conectar ao banco para migração", "component", "migrate")
		return errors.New("falha ao conectar ao banco para migração")
	}

	count, err := countSQL(os.DirFS(cfg.MigrationsDir))
	if err != nil {
		return errors.New("diretório de migrações indisponível")
	}
	if count == 0 {
		log.Info("nenhuma migração para aplicar", "component", "migrate")
		return nil
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		os.DirFS(cfg.MigrationsDir),
		goose.WithSlog(log),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		log.Error("falha ao preparar migrações", "component", "migrate")
		return errors.New("falha ao aplicar migrações")
	}
	results, err := provider.Up(ctx)
	if err != nil {
		log.Error("falha ao aplicar migrações", "component", "migrate")
		return errors.New("falha ao aplicar migrações")
	}
	log.Info("migrações aplicadas", "component", "migrate", "count", len(results))

	return nil
}

func countSQL(fsys fs.FS) (int, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			count++
		}
	}
	return count, nil
}

func ioDiscard() *discard {
	return &discard{}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
