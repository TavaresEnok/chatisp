package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Inbox struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	PresentedNumber *string   `json:"presented_number"`
	ConnectorType   string    `json:"connector_type"`
	ChannelStatus   string    `json:"channel_status"`
	Enabled         bool      `json:"enabled"`
}

func (s Service) ListInboxes(ctx context.Context, p Principal) ([]Inbox, error) {
	if p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil || p.IsSuperadmin {
		return nil, ErrForbidden
	}
	out := make([]Inbox, 0)
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizePrincipal(ctx, tx, p); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT i.id, i.name, i.presented_number, c.connector_type, c.status, c.enabled FROM inboxes i JOIN channel_connectors c ON c.tenant_id = i.tenant_id AND c.id = i.connector_id WHERE i.tenant_id = $1 ORDER BY i.name, i.id`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v Inbox
			if err := rows.Scan(&v.ID, &v.Name, &v.PresentedNumber, &v.ConnectorType, &v.ChannelStatus, &v.Enabled); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}
