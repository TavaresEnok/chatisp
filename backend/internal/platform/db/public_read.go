package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// WithPublicRead opens an application transaction whose only extra scope is
// app.access_scope=public_read. It does not set a tenant. Credential functions
// for the public read API refuse to run without this scope.
func (s *Store) WithPublicRead(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.app == nil {
		return errors.New("pool da aplicação indisponível")
	}
	tx, err := s.app.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireRole(ctx, tx, RoleApp); err != nil {
		return err
	}
	if err := setLocal(ctx, tx, "app.access_scope", "public_read"); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}
