package sectors

import (
	"context"
	"errors"

	attendanceStore "chatisp/internal/attendance/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Atomic authorization protocol.
//
// Every function below validates the principal and then reads the authorizing
// rows (memberships, sectors, sector_memberships, sector_inboxes and, for
// conversations, the conversation row itself) with FOR SHARE. The row locks are
// held until the caller's transaction ends, so revoking a membership, removing
// an inbox from a sector, deactivating a sector, changing a role or moving a
// conversation to another sector blocks until the consumer commits. Reads and
// actions performed in the same transaction after a successful call therefore
// observe an authorization that cannot be revoked concurrently. The tenant
// status advisory gate is also taken in shared mode, coordinating with
// suspension. Consumers MUST keep the policy call and the data read/write in the
// same pgx.Tx and SHOULD prefer RequireInbox / RequireConversation.

// AuthorizedInboxIDs lists inbox IDs the principal may access and locks the authorizing rows.
func AuthorizedInboxIDs(ctx context.Context, tx pgx.Tx, p Principal) ([]uuid.UUID, error) {
	return authorizedInboxes(ctx, tx, p, uuid.Nil)
}

func authorizedInboxes(ctx context.Context, tx pgx.Tx, p Principal, only uuid.UUID) ([]uuid.UUID, error) {
	if err := ValidatePrincipal(ctx, tx, p); err != nil {
		return nil, err
	}
	if p.Role == "admin" || p.Role == "supervisor" {
		return ids(ctx, tx, `SELECT i.id FROM inboxes i WHERE i.tenant_id=$1 AND $2::text<>'' AND $3::uuid IS NOT NULL AND ($4::uuid IS NULL OR i.id=$4) ORDER BY i.id FOR SHARE OF i`, p, nilNull(only))
	}
	list, err := ids(ctx, tx, `SELECT si.inbox_id FROM sector_memberships sm JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active JOIN sector_inboxes si ON si.tenant_id=sm.tenant_id AND si.sector_id=sm.sector_id WHERE sm.tenant_id=$1 AND $2::text<>'' AND sm.membership_id=$3 AND ($4::uuid IS NULL OR si.inbox_id=$4) ORDER BY si.inbox_id FOR SHARE OF sm, s, si`, p, nilNull(only))
	return dedupe(list), err
}

// AuthorizedConversationIDs lists conversation IDs of one inbox the principal may access and locks the authorizing rows.
func AuthorizedConversationIDs(ctx context.Context, tx pgx.Tx, p Principal, inboxID uuid.UUID) ([]uuid.UUID, error) {
	return authorizedConversations(ctx, tx, p, inboxID, uuid.Nil)
}

func authorizedConversations(ctx context.Context, tx pgx.Tx, p Principal, inboxID, only uuid.UUID) ([]uuid.UUID, error) {
	if err := ValidatePrincipal(ctx, tx, p); err != nil {
		return nil, err
	}
	if p.Role == "admin" || p.Role == "supervisor" {
		return ids(ctx, tx, `SELECT c.id FROM conversations c JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id WHERE c.tenant_id=$1 AND $2::text<>'' AND $3::uuid IS NOT NULL AND c.inbox_id=$4 AND ($5::uuid IS NULL OR c.id=$5) ORDER BY c.id FOR SHARE OF c, si`, p, inboxID, nilNull(only))
	}
	return ids(ctx, tx, `SELECT c.id FROM conversations c JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active JOIN sector_memberships sm ON sm.tenant_id=c.tenant_id AND sm.sector_id=c.sector_id AND sm.membership_id=$3 WHERE c.tenant_id=$1 AND $2::text<>'' AND c.inbox_id=$4 AND ($5::uuid IS NULL OR c.id=$5) ORDER BY c.id FOR SHARE OF c, si, s, sm`, p, inboxID, nilNull(only))
}

func nilNull(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func dedupe(in []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// CanAccessInbox reports access and keeps the authorizing rows locked until the transaction ends.
func CanAccessInbox(ctx context.Context, tx pgx.Tx, p Principal, inboxID uuid.UUID) (bool, error) {
	if inboxID == uuid.Nil {
		return false, ValidatePrincipal(ctx, tx, p)
	}
	list, e := authorizedInboxes(ctx, tx, p, inboxID)
	return len(list) > 0, e
}

// CanAccessConversation reports access and keeps the authorizing rows (including the conversation row) locked until the transaction ends.
func CanAccessConversation(ctx context.Context, tx pgx.Tx, p Principal, inboxID, conversationID uuid.UUID) (bool, error) {
	if conversationID == uuid.Nil {
		return false, ValidatePrincipal(ctx, tx, p)
	}
	list, e := authorizedConversations(ctx, tx, p, inboxID, conversationID)
	return len(list) > 0, e
}

// RequireInbox returns ErrForbidden unless the principal may access the inbox; on success the authorization is pinned for the rest of the tx.
func RequireInbox(ctx context.Context, tx pgx.Tx, p Principal, inboxID uuid.UUID) error {
	ok, e := CanAccessInbox(ctx, tx, p, inboxID)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

// RequireConversation pins authorization on success. Wrong inbox for an otherwise
// authorized conversation is attendanceStore.ErrNotFound; other denials are ErrForbidden.
func RequireConversation(ctx context.Context, tx pgx.Tx, p Principal, inboxID, conversationID uuid.UUID) error {
	ok, e := CanAccessConversation(ctx, tx, p, inboxID, conversationID)
	if e != nil {
		return e
	}
	if !ok {
		// Preserve M2's not-found response for a wrong inbox only when the
		// conversation is independently authorized in its actual inbox. Missing,
		// cross-tenant and revoked conversations remain forbidden. Return the
		// attendance storage sentinel understood by existing consumer adapters;
		// this is still a denial, never permission to act through another inbox.
		var actualInbox uuid.UUID
		err := tx.QueryRow(ctx, `SELECT inbox_id FROM conversations WHERE tenant_id=$1 AND id=$2 AND inbox_id IS NOT NULL FOR SHARE`, p.TenantID, conversationID).Scan(&actualInbox)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && actualInbox != inboxID {
			visible, err := CanAccessConversation(ctx, tx, p, actualInbox, conversationID)
			if err != nil {
				return err
			}
			if visible {
				return attendanceStore.ErrNotFound
			}
		}
		return ErrForbidden
	}
	return nil
}

// WithConversation runs fn in a tenant transaction after RequireConversation, atomically with the consumer's read or action.
func (s Service) WithConversation(ctx context.Context, p Principal, inboxID, conversationID uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrForbidden
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := RequireConversation(ctx, tx, p, inboxID, conversationID); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func ids(ctx context.Context, tx pgx.Tx, q string, p Principal, extra ...any) ([]uuid.UUID, error) {
	args := []any{p.TenantID, p.Role, p.MembershipID}
	args = append(args, extra...)
	rows, e := tx.Query(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
