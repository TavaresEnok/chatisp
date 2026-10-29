package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db/dbgen"
)

type tenantKey struct{}
type platformKey struct{}
type authKey struct{}

type tenantScope struct {
	id uuid.UUID
	tx pgx.Tx
	q  *dbgen.Queries
}

type platformScope struct {
	tx pgx.Tx
	q  *dbgen.Queries
}

type authScope struct {
	tx pgx.Tx
}

// Store is the normal process boundary. It has no migration/owner pool.
type Store struct {
	app      *pgxpool.Pool
	platform *pgxpool.Pool
}

// NewStore binds the application and platform pools.
func NewStore(app, platform *pgxpool.Pool) *Store {
	return &Store{app: app, platform: platform}
}

// WithTenant opens a transaction on the application pool and sets app.tenant_id
// for that transaction only. Domain methods refuse to run without this scope.
func (s *Store) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(context.Context) error) error {
	if tenantID == uuid.Nil {
		return ErrTenantContextRequired
	}
	if s == nil || s.app == nil {
		return errors.New("pool da aplicação indisponível")
	}
	tx, err := s.app.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setLocal(ctx, tx, "app.tenant_id", tenantID.String()); err != nil {
		return err
	}
	scoped := context.WithValue(ctx, tenantKey{}, tenantScope{id: tenantID, tx: tx, q: dbgen.New(tx)})
	if err := fn(scoped); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

// WithAuth opens a transaction on the application pool and sets app.access_scope
// to auth. Credential functions refuse to run without this scope. It does not
// grant tenant or platform access.
func (s *Store) WithAuth(ctx context.Context, fn func(context.Context) error) error {
	if s == nil || s.app == nil {
		return errors.New("pool da aplicação indisponível")
	}
	tx, err := s.app.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setLocal(ctx, tx, "app.access_scope", "auth"); err != nil {
		return err
	}
	scoped := context.WithValue(ctx, authKey{}, authScope{tx: tx})
	if err := fn(scoped); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

// WithPlatform opens a transaction on the platform pool and sets the platform scope.
// It does not grant a tenant context.
func (s *Store) WithPlatform(ctx context.Context, fn func(context.Context) error) error {
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
	if err := fn(scoped); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

func setLocal(ctx context.Context, tx pgx.Tx, key, value string) error {
	var got string
	if err := tx.QueryRow(ctx, `SELECT set_config($1, $2, true)`, key, value).Scan(&got); err != nil {
		return errors.New("falha ao fixar o contexto da transação")
	}
	if got != value {
		return errors.New("falha ao fixar o contexto da transação")
	}
	return nil
}

func tenantFrom(ctx context.Context) (tenantScope, error) {
	scope, ok := ctx.Value(tenantKey{}).(tenantScope)
	if !ok || scope.id == uuid.Nil || scope.q == nil {
		return tenantScope{}, ErrTenantContextRequired
	}
	return scope, nil
}

func platformFrom(ctx context.Context) (platformScope, error) {
	scope, ok := ctx.Value(platformKey{}).(platformScope)
	if !ok || scope.q == nil {
		return platformScope{}, ErrPlatformContextRequired
	}
	return scope, nil
}

func authFrom(ctx context.Context) (authScope, error) {
	scope, ok := ctx.Value(authKey{}).(authScope)
	if !ok || scope.tx == nil {
		return authScope{}, ErrAuthContextRequired
	}
	return scope, nil
}
