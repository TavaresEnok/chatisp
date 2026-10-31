package service

import (
	"context"
	"errors"

	"chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
	"github.com/jackc/pgx/v5"
)

// Queue queries only conversations visible to the authenticated principal.
func (s Service) Queue(ctx context.Context, p Principal, in store.QueueInput) (store.QueuePage, error) {
	var out store.QueuePage
	if s.DB == nil {
		return out, errors.New("attendance unavailable")
	}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			if errors.Is(err, sectors.ErrForbidden) {
				return ErrForbidden
			}
			return err
		}
		in.TenantID = p.TenantID
		in.UserID = p.UserID
		in.MembershipID = p.MembershipID
		in.Role = p.Role
		var err error
		out, err = store.Queue(ctx, tx, in)
		return err
	})
	return out, err
}
