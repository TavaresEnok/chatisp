package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DirectoryEntry is the minimal operational identity projection of a member.
type DirectoryEntry struct {
	MembershipID uuid.UUID `json:"membership_id"`
	Role         string    `json:"role"`
	Label        string    `json:"label"`
}

// OperatorDirectory reads one page through the restricted SECURITY DEFINER
// function. The function re-validates tenant, membership, role and inbox scope
// from persisted rows; the arguments never grant access. It returns up to limit
// entries ordered by membership id after the optional cursor.
func OperatorDirectory(ctx context.Context, tx pgx.Tx, tenant, user, member, inbox uuid.UUID, after *uuid.UUID, limit int) ([]DirectoryEntry, error) {
	rows, err := tx.Query(ctx, `SELECT membership_id, role, label FROM operator_directory($1,$2,$3,$4,$5,$6)`, tenant, user, member, inbox, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectoryEntry{}
	for rows.Next() {
		var e DirectoryEntry
		if err := rows.Scan(&e.MembershipID, &e.Role, &e.Label); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
