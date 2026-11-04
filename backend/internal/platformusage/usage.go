// Package platformusage reports technical counts for the platform owner.
// Queries select counts only. They do not read message text, protocol,
// contact fields or attachment names.
package platformusage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

const (
	auditAction = "platform.usage.read"
	auditTarget = "platform_usage"
	maxWindow   = 366 * 24 * time.Hour
)

// ErrInvalidPeriod means the requested interval cannot be counted.
var ErrInvalidPeriod = errors.New("período inválido")

// Snapshot is the platform usage panel. Every number is a count or a byte total.
type Snapshot struct {
	From               time.Time       `json:"from"`
	To                 time.Time       `json:"to"`
	ProvidersActive    int64           `json:"providers_active"`
	ProvidersSuspended int64           `json:"providers_suspended"`
	Providers          []ProviderUsage `json:"providers"`
}

// ProviderUsage is one provider's counts. It has no conversation payload.
type ProviderUsage struct {
	TenantID          uuid.UUID `json:"tenant_id"`
	Timezone          string    `json:"timezone"`
	Status            string    `json:"status"`
	ActiveAgents      int64     `json:"active_agents"`
	Inboxes           int64     `json:"inboxes"`
	OpenConversations int64     `json:"open_conversations"`
	Messages          int64     `json:"messages"`
	AttachmentBytes   *int64    `json:"attachment_bytes"`
}

// Service reads usage through the platform role and writes platform audit.
type Service struct {
	store *db.Store
}

// New binds the platform store. A nil store answers as unavailable.
func New(store *db.Store) Service {
	return Service{store: store}
}

// Read returns counts for [from, to). The caller must be a superadmin.
// A successful read writes one platform audit row in the same transaction.
func (s Service) Read(ctx context.Context, actor auth.Principal, from, to time.Time) (Snapshot, error) {
	if actor.EnrollmentOnly || !actor.IsSuperadmin || actor.UserID == uuid.Nil {
		return Snapshot{}, auth.ErrForbidden
	}
	if !to.After(from) || to.Sub(from) > maxWindow {
		return Snapshot{}, ErrInvalidPeriod
	}
	if s.store == nil {
		return Snapshot{}, auth.ErrUnavailable
	}
	var snap Snapshot
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		loaded, err := load(ctx, tx, from, to)
		if err != nil {
			return err
		}
		target := from.UTC().Format(time.RFC3339) + "/" + to.UTC().Format(time.RFC3339)
		if _, err := s.store.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
			Actor:      actor.UserID.String(),
			Action:     auditAction,
			TargetType: auditTarget,
			TargetID:   &target,
			Result:     "success",
		}); err != nil {
			return err
		}
		snap = loaded
		return nil
	})
	if err != nil {
		return Snapshot{}, auth.ErrUnavailable
	}
	return snap, nil
}

func load(ctx context.Context, tx pgx.Tx, from, to time.Time) (Snapshot, error) {
	snap := Snapshot{From: from.UTC(), To: to.UTC(), Providers: []ProviderUsage{}}
	rows, err := tx.Query(ctx, `
		SELECT id, timezone, status
		FROM tenants
		ORDER BY created_at, id
	`)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var item ProviderUsage
		if err := rows.Scan(&item.TenantID, &item.Timezone, &item.Status); err != nil {
			return Snapshot{}, err
		}
		switch item.Status {
		case "active":
			snap.ProvidersActive++
		case "suspended":
			snap.ProvidersSuspended++
		}
		index[item.TenantID] = len(snap.Providers)
		snap.Providers = append(snap.Providers, item)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}

	if err := scanCounts(ctx, tx, index, snap.Providers, `
		SELECT tenant_id, count(id) FILTER (WHERE role = 'agent' AND status = 'active')
		FROM memberships
		GROUP BY tenant_id
	`, func(item *ProviderUsage, n int64) { item.ActiveAgents = n }); err != nil {
		return Snapshot{}, err
	}
	if err := scanCounts(ctx, tx, index, snap.Providers, `
		SELECT tenant_id, count(tenant_id)
		FROM inboxes
		GROUP BY tenant_id
	`, func(item *ProviderUsage, n int64) { item.Inboxes = n }); err != nil {
		return Snapshot{}, err
	}
	if err := scanCounts(ctx, tx, index, snap.Providers, `
		SELECT tenant_id, count(tenant_id) FILTER (WHERE status = 'open')
		FROM conversations
		GROUP BY tenant_id
	`, func(item *ProviderUsage, n int64) { item.OpenConversations = n }); err != nil {
		return Snapshot{}, err
	}
	if err := scanCounts(ctx, tx, index, snap.Providers, `
		SELECT tenant_id, count(tenant_id)
		FROM messages
		WHERE ingested_at >= $1 AND ingested_at < $2
		GROUP BY tenant_id
	`, func(item *ProviderUsage, n int64) { item.Messages = n }, from, to); err != nil {
		return Snapshot{}, err
	}

	var attachments bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('public.attachments') IS NOT NULL`).Scan(&attachments); err != nil {
		return Snapshot{}, err
	}
	if attachments {
		for i := range snap.Providers {
			n := int64(0)
			snap.Providers[i].AttachmentBytes = &n
		}
		if err := scanCounts(ctx, tx, index, snap.Providers, `
			SELECT tenant_id, coalesce(sum(expected_bytes), 0)::bigint
			FROM attachments
			GROUP BY tenant_id
		`, func(item *ProviderUsage, n int64) {
			item.AttachmentBytes = &n
		}); err != nil {
			return Snapshot{}, err
		}
	}
	return snap, nil
}

func scanCounts(ctx context.Context, tx pgx.Tx, index map[uuid.UUID]int, items []ProviderUsage, query string, apply func(*ProviderUsage, int64), args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tenantID uuid.UUID
		var n int64
		if err := rows.Scan(&tenantID, &n); err != nil {
			return err
		}
		if at, ok := index[tenantID]; ok {
			apply(&items[at], n)
		}
	}
	return rows.Err()
}
