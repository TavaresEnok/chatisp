package retention

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type census struct {
	messages      int
	attachments   int
	conversations int
	cycles        int
}

// RunPurge records a refusal for every class and commits that trail.
// It does not delete messages, attachments, conversations or cycles.
// ErrPurgeDisabled is returned after the refusal is stored.
func (s Service) RunPurge(ctx context.Context, p Principal) (PurgeResult, error) {
	var result PurgeResult
	err := s.read(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		before, err := takeCensus(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_retention_profiles (tenant_id, automatic_purge)
			VALUES ($1, false)
			ON CONFLICT (tenant_id) DO UPDATE SET automatic_purge = false, updated_at = now()
		`, p.TenantID); err != nil {
			return err
		}
		for _, class := range Classes {
			if _, err := tx.Exec(ctx, `
				INSERT INTO retention_tasks (tenant_id, id, class, status, reason)
				VALUES ($1,$2,$3,'refused',$4)
			`, p.TenantID, uuid.New(), class, PurgeReason); err != nil {
				return err
			}
		}
		if err := insertAudit(ctx, tx, p, "retention.purge", "tenant", p.TenantID, "refused"); err != nil {
			return err
		}
		after, err := takeCensus(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if before != after {
			return ErrUnavailable
		}
		result = PurgeResult{Refused: true, Deleted: false, Tasks: len(Classes)}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return result, ErrPurgeDisabled
}

func takeCensus(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (census, error) {
	var out census
	err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM messages WHERE tenant_id = $1),
			(SELECT count(*) FROM attachments WHERE tenant_id = $1),
			(SELECT count(*) FROM conversations WHERE tenant_id = $1),
			(SELECT count(*) FROM conversation_cycles WHERE tenant_id = $1)
	`, tenant).Scan(&out.messages, &out.attachments, &out.conversations, &out.cycles)
	return out, err
}
