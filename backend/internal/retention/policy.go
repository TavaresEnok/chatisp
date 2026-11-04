package retention

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/sectors"
)

// GetPolicy returns the stored profile. A missing row is the default: purge off.
func (s Service) GetPolicy(ctx context.Context, p Principal) (Policy, error) {
	var out Policy
	err := s.read(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadPolicy(ctx, tx, p.TenantID)
		out = row
		return err
	})
	return out, err
}

// SavePolicy stores class durations and the provider name.
// automatic_purge true is rejected before any write.
func (s Service) SavePolicy(ctx context.Context, p Principal, in Policy) (Policy, error) {
	if in.AutomaticPurge {
		return Policy{}, ErrPurgeDisabled
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if err := validateDays(in); err != nil {
		return Policy{}, err
	}
	if in.DisplayName != "" && (len([]rune(in.DisplayName)) > 80) {
		return Policy{}, ErrInvalid
	}
	var out Policy
	err := s.read(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_retention_profiles (
				tenant_id, display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
			) VALUES ($1,$2,$3,$4,$5,$6,$7,false)
			ON CONFLICT (tenant_id) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				content_days = EXCLUDED.content_days,
				media_days = EXCLUDED.media_days,
				envelope_days = EXCLUDED.envelope_days,
				log_days = EXCLUDED.log_days,
				job_days = EXCLUDED.job_days,
				automatic_purge = false,
				updated_at = now()
		`, p.TenantID, nullName(in.DisplayName), in.ContentDays, in.MediaDays, in.EnvelopeDays, in.LogDays, in.JobDays); err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, p, "retention.policy", "tenant", p.TenantID, "success"); err != nil {
			return err
		}
		row, err := loadPolicy(ctx, tx, p.TenantID)
		out = row
		return err
	})
	return out, err
}

func loadPolicy(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Policy, error) {
	var out Policy
	var name *string
	err := tx.QueryRow(ctx, `
		SELECT display_name, content_days, media_days, envelope_days, log_days, job_days, automatic_purge
		FROM tenant_retention_profiles WHERE tenant_id = $1
	`, tenant).Scan(&name, &out.ContentDays, &out.MediaDays, &out.EnvelopeDays, &out.LogDays, &out.JobDays, &out.AutomaticPurge)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	if name != nil {
		out.DisplayName = *name
	}
	return out, nil
}

func validateDays(in Policy) error {
	for _, day := range []*int{in.ContentDays, in.MediaDays, in.EnvelopeDays, in.LogDays, in.JobDays} {
		if day != nil && (*day < 1 || *day > 36500) {
			return ErrInvalid
		}
	}
	return nil
}

func nullName(name string) any {
	if name == "" {
		return nil
	}
	return name
}

func (s Service) read(ctx context.Context, p Principal, admin bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if admin {
			if err := s.DB.LockTenant(ctx); err != nil {
				return err
			}
		}
		if err := authorize(ctx, tx, p, admin, false); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
	return mapDB(err)
}

func authorize(ctx context.Context, tx pgx.Tx, p Principal, adminOnly, allowSuspendedAdmin bool) error {
	if err := sectors.ValidatePrincipal(ctx, tx, sectors.Principal{
		TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin,
	}); err != nil {
		if allowSuspendedAdmin && p.Role == "admin" && !p.IsSuperadmin {
			return authorizeSuspendedAdmin(ctx, tx, p)
		}
		return err
	}
	if adminOnly && p.Role != "admin" {
		return ErrForbidden
	}
	if p.Role != "admin" && p.Role != "supervisor" && p.Role != "agent" {
		return ErrForbidden
	}
	return nil
}

func authorizeSuspendedAdmin(ctx context.Context, tx pgx.Tx, p Principal) error {
	if p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil || p.IsSuperadmin {
		return ErrForbidden
	}
	var role, status string
	err := tx.QueryRow(ctx, `
		SELECT m.role, t.status
		FROM memberships m
		JOIN tenants t ON t.id = m.tenant_id
		WHERE m.tenant_id = $1 AND m.id = $2 AND m.user_id = $3 AND m.status = 'active'
		FOR SHARE OF m
	`, p.TenantID, p.MembershipID, p.UserID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if role != "admin" || status != "suspended" {
		return ErrForbidden
	}
	return nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, p Principal, action, targetType string, target uuid.UUID, result string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (tenant_id, id, membership_id, actor_user_id, action, target_type, target_id, result)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, p.TenantID, uuid.New(), p.MembershipID, p.UserID, action, targetType, target, result)
	return err
}

func mapDB(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sectors.ErrForbidden) || errors.Is(err, ErrForbidden) {
		return ErrForbidden
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrPurgeDisabled) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23514" && strings.Contains(pg.Message, "expurgo") {
		return ErrPurgeDisabled
	}
	return ErrUnavailable
}
