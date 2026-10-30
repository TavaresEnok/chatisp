package notifications

import (
	"context"
	"errors"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrForbidden = errors.New("acesso negado")

type Preferences struct {
	Sound   bool `json:"sound"`
	Browser bool `json:"browser"`
	Silent  bool `json:"silent"`
}
type Patch struct {
	Sound   *bool `json:"sound,omitempty"`
	Browser *bool `json:"browser,omitempty"`
	Silent  *bool `json:"silent,omitempty"`
}

var ErrInvalid = errors.New("preferências inválidas")

type Service struct{ store *db.Store }

func New(store *db.Store) *Service {
	if store == nil {
		return nil
	}
	return &Service{store}
}
func valid(p auth.Principal) bool {
	return !p.IsSuperadmin && !p.EnrollmentOnly && p.TenantID != uuid.Nil && p.MembershipID != uuid.Nil && p.UserID != uuid.Nil
}
func (s *Service) Get(ctx context.Context, p auth.Principal) (Preferences, error) {
	var out = Preferences{Sound: true}
	if !valid(p) {
		return out, ErrForbidden
	}
	err := s.store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT sound, browser, silent FROM notification_preferences WHERE tenant_id=$1 AND membership_id=$2`, p.TenantID, p.MembershipID).Scan(&out.Sound, &out.Browser, &out.Silent)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return out, err
}
func (s *Service) Update(ctx context.Context, p auth.Principal, patch Patch) (Preferences, error) {
	var out Preferences
	if !valid(p) {
		return out, ErrForbidden
	}
	if patch.Sound == nil && patch.Browser == nil && patch.Silent == nil {
		return out, ErrInvalid
	}
	err := s.store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO notification_preferences (tenant_id,membership_id,sound,browser,silent) VALUES ($1,$2,COALESCE($3,true),COALESCE($4,false),COALESCE($5,false)) ON CONFLICT (tenant_id,membership_id) DO UPDATE SET sound=COALESCE($3,notification_preferences.sound), browser=COALESCE($4,notification_preferences.browser), silent=COALESCE($5,notification_preferences.silent), updated_at=now() RETURNING sound,browser,silent`, p.TenantID, p.MembershipID, patch.Sound, patch.Browser, patch.Silent).Scan(&out.Sound, &out.Browser, &out.Silent)
		if err != nil {
			return err
		}
		_, err = s.store.CreateAuditLog(ctx, db.NewAuditLog{MembershipID: &p.MembershipID, ActorUserID: &p.UserID, Action: "notification_preferences.update", TargetType: "membership", TargetID: &p.MembershipID, Result: "success"})
		return err
	})
	return out, err
}
