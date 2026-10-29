package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ErrTenantUnchanged is returned when a provider is already in the requested status.
var ErrTenantUnchanged = errTenantUnchanged{}

type errTenantUnchanged struct{}

func (errTenantUnchanged) Error() string { return "o provedor já está nesse estado" }

// GetTenantByID reads one provider through the platform role.
func (s *Store) GetTenantByID(ctx context.Context, id uuid.UUID) (Tenant, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return Tenant{}, err
	}
	if id == uuid.Nil {
		return Tenant{}, ErrNotFound
	}
	return scanTenant(scope.tx.QueryRow(ctx, `
		SELECT id, timezone, status, limits, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`, id))
}

// ListTenants returns provider registry rows. It does not read attendance data.
func (s *Store) ListTenants(ctx context.Context) ([]Tenant, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.tx.Query(ctx, `
		SELECT id, timezone, status, limits, created_at, updated_at
		FROM tenants
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]Tenant, 0)
	for rows.Next() {
		item, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

// SetTenantStatus moves a provider between active and suspended.
// The row, memberships and attendance stay in place.
func (s *Store) SetTenantStatus(ctx context.Context, id uuid.UUID, status string) (Tenant, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return Tenant{}, err
	}
	if id == uuid.Nil || (status != "active" && status != "suspended") {
		return Tenant{}, ErrInvalidTenantStatus
	}
	current, err := scanTenant(scope.tx.QueryRow(ctx, `
		SELECT id, timezone, status, limits, created_at, updated_at
		FROM tenants
		WHERE id = $1
		FOR UPDATE
	`, id))
	if err != nil {
		return Tenant{}, err
	}
	if current.Status == status {
		return current, ErrTenantUnchanged
	}
	updated, err := scanTenant(scope.tx.QueryRow(ctx, `
		UPDATE tenants
		SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, timezone, status, limits, created_at, updated_at
	`, id, status))
	if err != nil {
		return Tenant{}, err
	}
	return updated, nil
}

// LockTenant serializes plan checks for the active tenant.
// The application role cannot lock the tenant row itself: it has no UPDATE there.
func (s *Store) LockTenant(ctx context.Context) error {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return err
	}
	if _, err := scope.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chatisp-tenant'), hashtext($1::text))`, scope.id.String()); err != nil {
		return mapErr(err)
	}
	return nil
}

// CountLiveRole counts invited and active memberships of one role in the active tenant.
func (s *Store) CountLiveRole(ctx context.Context, role string) (int, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return 0, err
	}
	if role != "admin" && role != "supervisor" && role != "agent" {
		return 0, ErrInvalidMembershipRole
	}
	var n int
	if err := scope.tx.QueryRow(ctx, `
		SELECT count(*)
		FROM memberships
		WHERE tenant_id = $1
		  AND role = $2
		  AND status IN ('invited', 'active')
	`, scope.id, role).Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// FindMembership reads one membership of the active tenant.
func (s *Store) FindMembership(ctx context.Context, id uuid.UUID) (Membership, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return Membership{}, err
	}
	if id == uuid.Nil {
		return Membership{}, ErrNotFound
	}
	var row Membership
	err = scope.tx.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, role, status, created_at
		FROM memberships
		WHERE tenant_id = $1 AND id = $2
	`, scope.id, id).Scan(&row.ID, &row.TenantID, &row.UserID, &row.Role, &row.Status, &row.CreatedAt)
	if err != nil {
		return Membership{}, mapErr(err)
	}
	row.CreatedAt = row.CreatedAt.UTC()
	return row, nil
}

// InsertConversation stores one attendance row for the active tenant.
func (s *Store) InsertConversation(ctx context.Context, id uuid.UUID, summary string) error {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return err
	}
	if id == uuid.Nil || summary == "" || len(summary) > 4000 {
		return ErrInvalidAudit
	}
	_, err = scope.tx.Exec(ctx, `
		INSERT INTO conversations (id, tenant_id, summary)
		VALUES ($1, $2, $3)
	`, id, scope.id, summary)
	return mapErr(err)
}

// ListConversationSummaries reads attendance of the active tenant.
func (s *Store) ListConversationSummaries(ctx context.Context) ([]string, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.tx.Query(ctx, `
		SELECT summary
		FROM conversations
		WHERE tenant_id = $1
		ORDER BY created_at, id
	`, scope.id)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

type tenantScanner interface {
	Scan(dest ...any) error
}

func scanTenant(row tenantScanner) (Tenant, error) {
	var item Tenant
	var created, updated time.Time
	if err := row.Scan(&item.ID, &item.Timezone, &item.Status, &item.Limits, &created, &updated); err != nil {
		return Tenant{}, mapErr(err)
	}
	item.CreatedAt = created.UTC()
	item.UpdatedAt = updated.UTC()
	return item, nil
}
