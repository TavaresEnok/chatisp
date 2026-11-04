package publicapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// Issue stores a read-scoped token and returns the raw secret once.
// The database keeps only the SHA-256 image used for panel session secrets.
func Issue(ctx context.Context, store *db.Store, tenant uuid.UUID, limit int) (string, uuid.UUID, error) {
	if store == nil {
		return "", uuid.Nil, errors.New("serviço indisponível")
	}
	if tenant == uuid.Nil || limit < 1 || limit > 6000 {
		return "", uuid.Nil, errors.New("limite inválido")
	}
	raw, hash, err := auth.NewSecret()
	if err != nil {
		return "", uuid.Nil, err
	}
	id := uuid.New()
	err = store.WithPublicRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT public_read_issue($1, $2, $3, $4)`, id, tenant, hash, limit)
		return err
	})
	if err != nil {
		return "", uuid.Nil, errors.New("falha ao emitir token")
	}
	return raw, id, nil
}

// Revoke marks a read token unusable. A later bearer request does not return data.
func Revoke(ctx context.Context, store *db.Store, id uuid.UUID) error {
	if store == nil || id == uuid.Nil {
		return errors.New("token inválido")
	}
	err := store.WithPublicRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT public_read_revoke($1, $2)`, id, time.Now().UTC())
		return err
	})
	if err != nil {
		return errors.New("falha ao revogar token")
	}
	return nil
}
