package embed

import (
	"context"
	"errors"
	"strings"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/urlguard"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Principal = sectors.Principal

type Service struct {
	DB     *db.Store
	Policy *urlguard.Policy
}

type Draft struct {
	AllowedOrigin *string `json:"allowed_origin"`
	PanelURL      *string `json:"panel_url"`
}

type Settings struct {
	Configured    bool   `json:"configured"`
	Version       int64  `json:"version"`
	AllowedOrigin string `json:"allowed_origin"`
	PanelURL      string `json:"panel_url"`
	Embeddable    bool   `json:"embeddable"`
}

type View struct {
	Embed   bool     `json:"embed"`
	URL     string   `json:"url,omitempty"`
	Origin  string   `json:"origin,omitempty"`
	Context *Context `json:"context,omitempty"`
}

func New(database *db.Store) Service {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		return Service{DB: database}
	}
	return Service{DB: database, Policy: policy}
}

func (s Service) Settings(ctx context.Context, p Principal) (Settings, error) {
	if err := authorizeSettings(p, false); err != nil {
		return Settings{}, err
	}
	if s.DB == nil || s.Policy == nil {
		return Settings{}, ErrUnavailable
	}
	var out Settings
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if p.Role != "admin" && p.Role != "supervisor" {
			return sectors.ErrForbidden
		}
		return readSettings(ctx, tx, s.Policy, p.TenantID, &out)
	})
	return out, err
}

func (s Service) Save(ctx context.Context, p Principal, draft Draft) (Settings, error) {
	if err := authorizeSettings(p, true); err != nil {
		return Settings{}, err
	}
	if draft.AllowedOrigin == nil || draft.PanelURL == nil {
		return Settings{}, ErrInvalid
	}
	origin := strings.TrimSpace(*draft.AllowedOrigin)
	panel := strings.TrimSpace(*draft.PanelURL)
	clear := origin == "" && panel == ""
	var normOrigin, normURL string
	if !clear {
		if s.Policy == nil {
			return Settings{}, ErrUnavailable
		}
		var err error
		normOrigin, normURL, err = Check(s.Policy, origin, panel)
		if err != nil {
			return Settings{}, err
		}
	}
	if s.DB == nil || s.Policy == nil {
		return Settings{}, ErrUnavailable
	}
	var out Settings
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.DB.LockTenant(ctx); err != nil {
			return err
		}
		if err := sectors.ValidatePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if p.Role != "admin" {
			return sectors.ErrForbidden
		}
		if clear {
			if _, err := tx.Exec(ctx, `DELETE FROM provider_embed_settings WHERE tenant_id=$1`, p.TenantID); err != nil {
				return err
			}
			out = Settings{}
			return nil
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO provider_embed_settings (tenant_id, version, allowed_origin, panel_url)
			VALUES ($1, 1, $2, $3)
			ON CONFLICT (tenant_id) DO UPDATE
			SET version = provider_embed_settings.version + 1,
			    allowed_origin = EXCLUDED.allowed_origin,
			    panel_url = EXCLUDED.panel_url,
			    updated_at = now()
			RETURNING version, allowed_origin, panel_url`, p.TenantID, normOrigin, normURL).Scan(&out.Version, &out.AllowedOrigin, &out.PanelURL)
		if err != nil {
			return err
		}
		out.Configured = true
		out.Embeddable = true
		return nil
	})
	return out, err
}

func (s Service) Conversation(ctx context.Context, p Principal, inbox, conversation uuid.UUID) (View, error) {
	if p.IsSuperadmin || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil || (p.Role != "admin" && p.Role != "supervisor" && p.Role != "agent") {
		return View{}, sectors.ErrForbidden
	}
	if inbox == uuid.Nil || conversation == uuid.Nil {
		return View{}, ErrInvalid
	}
	if s.DB == nil || s.Policy == nil {
		return View{}, ErrUnavailable
	}
	var view View
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		var sector uuid.UUID
		var protocol string
		err := tx.QueryRow(ctx, `SELECT sector_id, coalesce(protocol,'') FROM conversations WHERE tenant_id=$1 AND id=$2 AND inbox_id=$3`, p.TenantID, conversation, inbox).Scan(&sector, &protocol)
		if errors.Is(err, pgx.ErrNoRows) {
			return sectors.ErrForbidden
		}
		if err != nil {
			return err
		}
		var origin, panel string
		err = tx.QueryRow(ctx, `SELECT allowed_origin, panel_url FROM provider_embed_settings WHERE tenant_id=$1`, p.TenantID).Scan(&origin, &panel)
		if errors.Is(err, pgx.ErrNoRows) {
			view = View{}
			return nil
		}
		if err != nil {
			return err
		}
		view = Decide(s.Policy, origin, panel, conversation, protocol, sector, inbox)
		return nil
	})
	return view, err
}

func authorizeSettings(p Principal, write bool) error {
	if p.IsSuperadmin || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return sectors.ErrForbidden
	}
	if write {
		if p.Role != "admin" {
			return sectors.ErrForbidden
		}
		return nil
	}
	if p.Role != "admin" && p.Role != "supervisor" {
		return sectors.ErrForbidden
	}
	return nil
}

func readSettings(ctx context.Context, tx pgx.Tx, policy *urlguard.Policy, tenant uuid.UUID, out *Settings) error {
	var origin, panel string
	var version int64
	err := tx.QueryRow(ctx, `SELECT version, allowed_origin, panel_url FROM provider_embed_settings WHERE tenant_id=$1 FOR SHARE`, tenant).Scan(&version, &origin, &panel)
	if errors.Is(err, pgx.ErrNoRows) {
		*out = Settings{}
		return nil
	}
	if err != nil {
		return err
	}
	_, _, checkErr := Check(policy, origin, panel)
	*out = Settings{Configured: true, Version: version, AllowedOrigin: origin, PanelURL: panel, Embeddable: checkErr == nil}
	return nil
}
