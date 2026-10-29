package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db/dbgen"
)

// WithTenantTx starts one application transaction and pins app.tenant_id to
// tenantID for that transaction only. The callback receives the same
// transaction a later River InsertTx can join. This package does not import
// River and does not open the owner pool.
//
// The context also carries the tenant scope used by WithTenant methods, so
// those methods share this transaction. Do not call WithTenant inside the
// callback: that opens a second transaction. Do not perform network calls
// while the callback holds the transaction.
func (s *Store) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
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
	if err := requireRole(ctx, tx, RoleApp); err != nil {
		return err
	}
	if err := setLocal(ctx, tx, "app.tenant_id", tenantID.String()); err != nil {
		return err
	}
	scoped := context.WithValue(ctx, tenantKey{}, tenantScope{id: tenantID, tx: tx, q: dbgen.New(tx)})
	if err := fn(scoped, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

// WithIngressTx starts one ingress transaction and pins app.access_scope to
// ingress. It does not set a tenant and it does not accept a tenant from a
// request body. The pool must already be the ingress role.
func WithIngressTx(ctx context.Context, ingress *pgxpool.Pool, fn func(context.Context, pgx.Tx) error) error {
	if ingress == nil {
		return errors.New("pool de ingresso indisponível")
	}
	tx, err := ingress.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireRole(ctx, tx, RoleIngress); err != nil {
		return err
	}
	if err := setLocal(ctx, tx, "app.access_scope", "ingress"); err != nil {
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

func requireRole(ctx context.Context, tx pgx.Tx, role string) error {
	var current string
	if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&current); err != nil {
		return errors.New("falha ao confirmar o papel do banco")
	}
	if current != role {
		return errors.New("papel de banco recusado")
	}
	return nil
}
