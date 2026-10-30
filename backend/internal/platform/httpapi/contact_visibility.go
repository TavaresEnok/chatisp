package httpapi

import (
	"context"
	"errors"

	"chatisp/internal/contactdirectory"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ContactVisibility applies the same transactional sector policy as attendance.
type ContactVisibility struct{}

func sectorPrincipal(p auth.Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}

func visibilityError(err error) error {
	if errors.Is(err, sectors.ErrForbidden) {
		return contactdirectory.ErrForbidden
	}
	return err
}

func (ContactVisibility) Contact(ctx context.Context, tx pgx.Tx, p auth.Principal, id uuid.UUID) (bool, error) {
	if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
		return false, visibilityError(err)
	}
	var creator *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT created_by_membership_id FROM contacts WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, id).Scan(&creator); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT id, inbox_id FROM conversations WHERE tenant_id=$1 AND contact_id=$2`, p.TenantID, id)
	if err != nil {
		return false, err
	}
	type pair struct{ id, inbox uuid.UUID }
	var conversations []pair
	for rows.Next() {
		var c pair
		if err = rows.Scan(&c.id, &c.inbox); err != nil {
			break
		}
		conversations = append(conversations, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(conversations) == 0 {
		return p.Role == "admin" || creator != nil && *creator == p.MembershipID, nil
	}
	for _, c := range conversations {
		ok, err := sectors.CanAccessConversation(ctx, tx, sectorPrincipal(p), c.inbox, c.id)
		if err != nil {
			return false, visibilityError(err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (ContactVisibility) Conversation(ctx context.Context, tx pgx.Tx, p auth.Principal, contact, conversation uuid.UUID) (bool, error) {
	var inbox uuid.UUID
	err := tx.QueryRow(ctx, `SELECT inbox_id FROM conversations WHERE tenant_id=$1 AND contact_id=$2 AND id=$3`, p.TenantID, contact, conversation).Scan(&inbox)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ok, err := sectors.CanAccessConversation(ctx, tx, sectorPrincipal(p), inbox, conversation)
	return ok, visibilityError(err)
}
