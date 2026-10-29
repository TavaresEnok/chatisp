package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db/dbgen"
)

func queries(tx pgx.Tx) *dbgen.Queries {
	return dbgen.New(tx)
}

func requireTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	if tx == nil || tenantID == uuid.Nil {
		return ErrTenantContextRequired
	}
	var got *string
	if err := tx.QueryRow(ctx, `SELECT NULLIF(current_setting('app.tenant_id', true), '')`).Scan(&got); err != nil || got == nil || *got != tenantID.String() {
		return ErrTenantContextRequired
	}
	return nil
}

func requireIngress(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return ErrIngressContextRequired
	}
	var scope *string
	if err := tx.QueryRow(ctx, `SELECT NULLIF(current_setting('app.access_scope', true), '')`).Scan(&scope); err != nil || scope == nil || *scope != "ingress" {
		return ErrIngressContextRequired
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "chatisp_ingress" {
		return ErrIngressContextRequired
	}
	return nil
}

func savepoint(ctx context.Context, tx pgx.Tx, name string) error {
	_, err := tx.Exec(ctx, "SAVEPOINT "+name)
	return err
}

func rollbackSavepoint(ctx context.Context, tx pgx.Tx, name string) {
	_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name)
}

func releaseSavepoint(ctx context.Context, tx pgx.Tx, name string) error {
	_, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

func isNoRow(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isCheck(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func incompatibleBinding(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.Message == "amarração incompatível"
}

func uncertain(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.Message == "envio incerto não se repete"
}
