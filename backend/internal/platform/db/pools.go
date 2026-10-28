package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pools keeps one limited pool per database function.
// The normal API process leaves Owner nil. That credential belongs to migrations.
type Pools struct {
	Owner    *pgxpool.Pool
	App      *pgxpool.Pool
	Platform *pgxpool.Pool
	Ingress  *pgxpool.Pool
}

// OpenPools connects each non-empty URL. Passwords never appear in errors.
func OpenPools(ctx context.Context, urls RoleURLs) (*Pools, error) {
	pools := &Pools{}
	var err error
	if pools.Owner, err = maybeOpen(ctx, urls.Owner, RoleOwner); err != nil {
		pools.Close()
		return nil, err
	}
	if pools.App, err = maybeOpen(ctx, urls.App, RoleApp); err != nil {
		pools.Close()
		return nil, err
	}
	if pools.Platform, err = maybeOpen(ctx, urls.Platform, RolePlatform); err != nil {
		pools.Close()
		return nil, err
	}
	if pools.Ingress, err = maybeOpen(ctx, urls.Ingress, RoleIngress); err != nil {
		pools.Close()
		return nil, err
	}
	if pools.Owner == nil && pools.App == nil && pools.Platform == nil && pools.Ingress == nil {
		return nil, errors.New("nenhum pool de banco configurado")
	}
	return pools, nil
}

// Close releases every opened pool.
func (p *Pools) Close() {
	if p == nil {
		return
	}
	if p.Owner != nil {
		p.Owner.Close()
	}
	if p.App != nil {
		p.App.Close()
	}
	if p.Platform != nil {
		p.Platform.Close()
	}
	if p.Ingress != nil {
		p.Ingress.Close()
	}
}

func maybeOpen(ctx context.Context, rawURL, applicationName string) (*pgxpool.Pool, error) {
	if rawURL == "" {
		return nil, nil
	}
	return openPool(ctx, rawURL, applicationName)
}

func openPool(ctx context.Context, rawURL, applicationName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, errors.New("url de banco inválida")
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("falha ao abrir pool do banco")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errors.New("falha ao conectar ao banco")
	}
	return pool, nil
}
