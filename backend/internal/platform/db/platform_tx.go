package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// WithPlatformTx opens one platform transaction and sets app.access_scope to
// platform. The callback shares that transaction with platform store methods.
// It does not set a tenant and it does not grant attendance text columns.
func (s *Store) WithPlatformTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.platform == nil {
		return errors.New("pool de plataforma indisponível")
	}
	tx, err := s.platform.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setLocal(ctx, tx, "app.access_scope", "platform"); err != nil {
		return err
	}
	scoped := context.WithValue(ctx, platformKey{}, platformScope{tx: tx, q: dbgen.New(tx)})
	if err := fn(scoped, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}
