package publicapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

var allowedScopes = []string{
	"read",
	"contacts.read",
	"contacts.write",
	"conversations.read",
	"messages.read",
	"templates.send",
	"directory.read",
}

// IssueScoped stores a scoped API token and returns the raw secret once.
func IssueScoped(ctx context.Context, store *db.Store, tenant uuid.UUID, scopes []string, limit int) (string, uuid.UUID, error) {
	if store == nil || tenant == uuid.Nil {
		return "", uuid.Nil, errors.New("serviço indisponível")
	}
	clean, err := normalizeScopes(scopes)
	if err != nil {
		return "", uuid.Nil, err
	}
	if limit < 1 || limit > 6000 {
		return "", uuid.Nil, errors.New("limite inválido")
	}
	raw, hash, err := auth.NewSecret()
	if err != nil {
		return "", uuid.Nil, err
	}
	id := uuid.New()
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT api_token_issue($1, $2, $3, $4, $5)`, id, tenant, hash, clean, limit)
		return err
	})
	if err != nil {
		return "", uuid.Nil, err
	}
	return raw, id, nil
}

// RevokeScoped marks one token of the tenant unusable.
func RevokeScoped(ctx context.Context, store *db.Store, tenant, id uuid.UUID) error {
	if store == nil || tenant == uuid.Nil || id == uuid.Nil {
		return errors.New("token inválido")
	}
	return store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT api_token_revoke($1, $2)`, id, time.Now().UTC())
		return err
	})
}

type storedToken struct {
	ID           uuid.UUID
	Scopes       []string
	RequestLimit int
	Revoked      bool
	CreatedAt    time.Time
}

func listScoped(ctx context.Context, store *db.Store, tenant uuid.UUID) ([]storedToken, error) {
	if store == nil || tenant == uuid.Nil {
		return nil, errors.New("serviço indisponível")
	}
	var out []storedToken
	err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, scopes, request_limit, revoked, created_at FROM api_token_list()`)
		if err != nil {
			return err
		}
		defer rows.Close()
		out = []storedToken{}
		for rows.Next() {
			var item storedToken
			if err := rows.Scan(&item.ID, &item.Scopes, &item.RequestLimit, &item.Revoked, &item.CreatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, errors.New("escopo inválido")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if seen[scope] || !knownScope(scope) {
			return nil, errors.New("escopo inválido")
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out, nil
}

func knownScope(scope string) bool {
	for _, item := range allowedScopes {
		if item == scope {
			return true
		}
	}
	return false
}

func tokenStatus(err error) (int, string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42501":
			return 403, "acesso negado"
		case "P0002":
			return 404, "token não encontrado"
		case "22023":
			return 422, "token inválido"
		}
	}
	if err != nil && (err.Error() == "escopo inválido" || err.Error() == "limite inválido") {
		return 422, err.Error()
	}
	return 500, "ocorreu um erro interno"
}
