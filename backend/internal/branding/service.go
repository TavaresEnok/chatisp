package branding

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/tenantcache"
)

const identityCacheID = "identity"

// Service reads and writes the provider mark for one tenant.
type Service struct {
	DB    *db.Store
	Cache *tenantcache.Cache
}

// Get returns the retention name and logo metadata for the caller's tenant.
func (s Service) Get(ctx context.Context, p Principal) (Identity, error) {
	var out Identity
	err := s.withTx(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		if s.Cache != nil {
			var hit Identity
			if s.Cache.Get(p.TenantID.String(), tenantcache.KindConfig, identityCacheID, 0, &hit) {
				out = hit
				return nil
			}
		}
		row, err := loadIdentity(ctx, tx, p)
		out = row
		if err == nil {
			s.rememberIdentity(p, row)
		}
		return err
	})
	return out, err
}

// SaveName updates only display_name. Deadlines and automatic_purge stay as stored.
func (s Service) SaveName(ctx context.Context, p Principal, name string) (Identity, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return Identity{}, ErrInvalid
	}
	var out Identity
	err := s.withTx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		var stored any
		if name != "" {
			stored = name
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_retention_profiles (tenant_id, display_name, automatic_purge)
			VALUES ($1, $2, false)
			ON CONFLICT (tenant_id) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				updated_at = now()
		`, p.TenantID, stored); err != nil {
			return err
		}
		if err := audit(ctx, tx, p, "branding.name"); err != nil {
			return err
		}
		row, err := loadIdentity(ctx, tx, p)
		out = row
		return err
	})
	if err == nil {
		s.forgetIdentity(p)
	}
	return out, err
}

// SaveLogo stores a re-encoded PNG or JPEG for the caller's tenant.
func (s Service) SaveLogo(ctx context.Context, p Principal, declared string, raw []byte) (Identity, error) {
	img, err := Normalize(declared, raw)
	if err != nil {
		return Identity{}, err
	}
	var out Identity
	err = s.withTx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_provider_logos (tenant_id, content_type, image, width, height, byte_size)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id) DO UPDATE SET
				content_type = EXCLUDED.content_type,
				image = EXCLUDED.image,
				width = EXCLUDED.width,
				height = EXCLUDED.height,
				byte_size = EXCLUDED.byte_size,
				updated_at = now()
		`, p.TenantID, img.ContentType, img.Bytes, img.Width, img.Height, len(img.Bytes)); err != nil {
			return err
		}
		if err := audit(ctx, tx, p, "branding.logo.replace"); err != nil {
			return err
		}
		row, err := loadIdentity(ctx, tx, p)
		out = row
		return err
	})
	if err == nil {
		s.forgetIdentity(p)
	}
	return out, err
}

// RemoveLogo deletes the logo row and leaves the retention profile in place.
func (s Service) RemoveLogo(ctx context.Context, p Principal) (Identity, error) {
	var out Identity
	err := s.withTx(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM tenant_provider_logos WHERE tenant_id = $1`, p.TenantID); err != nil {
			return err
		}
		if err := audit(ctx, tx, p, "branding.logo.remove"); err != nil {
			return err
		}
		row, err := loadIdentity(ctx, tx, p)
		out = row
		return err
	})
	if err == nil {
		s.forgetIdentity(p)
	}
	return out, err
}

// Logo returns the stored image after checking its magic bytes again.
func (s Service) Logo(ctx context.Context, p Principal) (Image, error) {
	var out Image
	err := s.withTx(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		var contentType string
		var raw []byte
		var width, height int
		err := tx.QueryRow(ctx, `
			SELECT content_type, image, width, height
			FROM tenant_provider_logos
			WHERE tenant_id = $1
		`, p.TenantID).Scan(&contentType, &raw, &width, &height)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !Matches(contentType, raw) || !edgeOK(width, height) || len(raw) == 0 || len(raw) > MaxLogoBytes {
			return ErrUnavailable
		}
		out = Image{ContentType: contentType, Bytes: raw, Width: width, Height: height}
		return nil
	})
	return out, err
}

func loadIdentity(ctx context.Context, tx pgx.Tx, p Principal) (Identity, error) {
	var name *string
	var contentType *string
	var width, height *int
	var updated *time.Time
	err := tx.QueryRow(ctx, `
		SELECT p.display_name, l.content_type, l.width, l.height, GREATEST(p.updated_at, l.updated_at)
		FROM (SELECT $1::uuid AS tenant_id) AS anchor
		LEFT JOIN tenant_retention_profiles AS p ON p.tenant_id = anchor.tenant_id
		LEFT JOIN tenant_provider_logos AS l ON l.tenant_id = anchor.tenant_id
	`, p.TenantID).Scan(&name, &contentType, &width, &height, &updated)
	if err != nil {
		return Identity{}, err
	}
	out := Identity{FallbackName: FallbackName}
	if name != nil {
		out.DisplayName = *name
	}
	if contentType != nil && width != nil && height != nil {
		out.HasLogo = true
		out.ContentType = *contentType
		out.Width = *width
		out.Height = *height
	}
	if updated != nil {
		out.UpdatedAt = updated.UTC().Format(time.RFC3339Nano)
	}
	return out, nil
}

func (s Service) withTx(ctx context.Context, p Principal, write bool, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrUnavailable
	}
	if p.IsSuperadmin || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	if p.Role != "admin" && p.Role != "supervisor" && p.Role != "agent" {
		return ErrForbidden
	}
	if write && (p.Role != "admin" || p.EnrollmentOnly) {
		return ErrForbidden
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if write {
			if err := s.DB.LockTenant(ctx); err != nil {
				return err
			}
		}
		if err := authorize(ctx, tx, p, write); err != nil {
			return err
		}
		s.rememberPermission(p)
		return fn(ctx, tx)
	})
	return mapDB(err)
}

func authorize(ctx context.Context, tx pgx.Tx, p Principal, write bool) error {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT m.role
		FROM memberships AS m
		JOIN tenants AS t ON t.id = m.tenant_id
		WHERE m.tenant_id = $1 AND m.id = $2 AND m.user_id = $3
		  AND m.status = 'active' AND t.status = 'active'
		FOR SHARE OF m
	`, p.TenantID, p.MembershipID, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if role != p.Role || (write && role != "admin") {
		return ErrForbidden
	}
	return nil
}

func (s Service) rememberIdentity(p Principal, row Identity) {
	if s.Cache == nil || p.TenantID == uuid.Nil {
		return
	}
	_ = s.Cache.Set(p.TenantID.String(), tenantcache.KindConfig, identityCacheID, 0, row)
}

func (s Service) forgetIdentity(p Principal) {
	if s.Cache == nil || p.TenantID == uuid.Nil {
		return
	}
	s.Cache.Invalidate(p.TenantID.String(), tenantcache.KindConfig, identityCacheID)
}

func (s Service) rememberPermission(p Principal) {
	if s.Cache == nil || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return
	}
	version, ok := tenantcache.PermissionVersion(p.Role)
	if !ok {
		return
	}
	_ = s.Cache.Set(p.TenantID.String(), tenantcache.KindPermission, p.MembershipID.String(), version, p.Role)
}

func audit(ctx context.Context, tx pgx.Tx, p Principal, action string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (tenant_id, id, membership_id, actor_user_id, action, target_type, target_id, result)
		VALUES ($1, $2, $3, $4, $5, 'tenant', $1, 'success')
	`, p.TenantID, uuid.New(), p.MembershipID, p.UserID, action)
	return err
}

func mapDB(err error) error {
	if err == nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23514" {
		return ErrInvalid
	}
	return ErrUnavailable
}
