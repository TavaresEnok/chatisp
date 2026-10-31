package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
)

const (
	DirectoryDefaultLimit = 50
	DirectoryMaxLimit     = 100
)

// TransferSector projects only destinations accepted by TransferToSector.
// Listing a destination grants no access to its conversations or identities.
type TransferSector struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (s Service) TransferSectors(ctx context.Context, p Principal, inbox, conversation uuid.UUID) ([]TransferSector, error) {
	out := []TransferSector{}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT s.id,s.name FROM sectors s JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id WHERE s.tenant_id=$1 AND si.inbox_id=$2 AND s.active ORDER BY s.name,s.id`, p.TenantID, inbox)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry TransferSector
			if err := rows.Scan(&entry.ID, &entry.Name); err != nil {
				return err
			}
			out = append(out, entry)
		}
		return rows.Err()
	})
	return out, err
}

// OperatorDirectoryPage is one explicit page of the operational directory.
type OperatorDirectoryPage struct {
	Items      []store.DirectoryEntry `json:"items"`
	NextCursor *uuid.UUID             `json:"next_cursor" nullable:"true"`
}

// Directory lists labels of active members who can operate the inbox. It only
// resolves labels: it never widens transfer eligibility (candidates endpoint).
func (s Service) Directory(ctx context.Context, p Principal, inbox uuid.UUID, after *uuid.UUID, limit int) (OperatorDirectoryPage, error) {
	if limit <= 0 {
		limit = DirectoryDefaultLimit
	}
	if limit > DirectoryMaxLimit {
		limit = DirectoryMaxLimit
	}
	if p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil || p.IsSuperadmin || inbox == uuid.Nil {
		return OperatorDirectoryPage{}, ErrForbidden
	}
	page := OperatorDirectoryPage{Items: []store.DirectoryEntry{}}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		items, err := store.OperatorDirectory(ctx, tx, p.TenantID, p.UserID, p.MembershipID, inbox, after, limit+1)
		if err != nil {
			return err
		}
		if len(items) > limit {
			items = items[:limit]
			next := items[limit-1].MembershipID
			page.NextCursor = &next
		}
		page.Items = items
		return nil
	})
	return page, err
}
