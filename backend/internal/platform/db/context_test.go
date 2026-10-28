package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db"
)

func TestDomainRejectsMissingContextBeforeQuery(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://chatisp_app:unused@127.0.0.1:1/chatisp?sslmode=disable")
	if err != nil {
		t.Fatal("url de teste inválida")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("pool de teste inválido")
	}
	t.Cleanup(pool.Close)

	store := db.NewStore(pool, pool)
	ctx := context.Background()

	if _, err := store.ListMemberships(ctx); !errors.Is(err, db.ErrTenantContextRequired) {
		t.Fatal("listagem sem tenant deveria falhar antes de consultar o banco")
	}
	if _, err := store.ListAuditLogs(ctx); !errors.Is(err, db.ErrTenantContextRequired) {
		t.Fatal("auditoria sem tenant deveria falhar antes de consultar o banco")
	}
	if _, err := store.GetTenant(ctx); !errors.Is(err, db.ErrTenantContextRequired) {
		t.Fatal("leitura de tenant sem contexto deveria falhar antes de consultar o banco")
	}
	if err := store.WithTenant(ctx, uuid.Nil, func(context.Context) error { return nil }); !errors.Is(err, db.ErrTenantContextRequired) {
		t.Fatal("tenant vazio deveria ser rejeitado antes de abrir transação")
	}
	if _, err := store.CreateTenant(ctx, db.NewTenant{}); !errors.Is(err, db.ErrPlatformContextRequired) {
		t.Fatal("cadastro de tenant sem contexto de plataforma deveria falhar antes de consultar o banco")
	}
	if _, err := store.CreateUser(ctx, db.NewUser{}); !errors.Is(err, db.ErrPlatformContextRequired) {
		t.Fatal("cadastro de usuário sem contexto de plataforma deveria falhar antes de consultar o banco")
	}
	if err := store.SetPasswordHash(ctx, uuid.New(), "hash-for-tests"); !errors.Is(err, db.ErrAuthContextRequired) {
		t.Fatal("gravação de hash sem contexto de autenticação deveria falhar antes de consultar o banco")
	}
	if _, err := store.PasswordHashByEmail(ctx, "person@example.com"); !errors.Is(err, db.ErrAuthContextRequired) {
		t.Fatal("leitura de hash sem contexto de autenticação deveria falhar antes de consultar o banco")
	}
	if _, err := store.ListPlatformAuditLogs(ctx); !errors.Is(err, db.ErrPlatformContextRequired) {
		t.Fatal("auditoria de plataforma sem contexto deveria falhar antes de consultar o banco")
	}
}
