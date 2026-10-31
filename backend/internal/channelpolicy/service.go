package channelpolicy

import (
	"chatisp/internal/sectors"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s Service) RecordConsent(ctx context.Context, p sectors.Principal, in ConsentInput) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if s.DB == nil {
		return nil, sectors.ErrForbidden
	}
	e := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		ids, e = s.RecordConsentTx(ctx, tx, p, in)
		return e
	})
	return ids, e
}
func (s Service) Evaluate(ctx context.Context, p sectors.Principal, in Dispatch) (Decision, error) {
	var d Decision
	if s.DB == nil {
		return d, sectors.ErrForbidden
	}
	e := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		d, e = s.EvaluateDispatchTx(ctx, tx, p, in)
		return e
	})
	return d, e
}
func (s Service) ListTemplates(ctx context.Context, p sectors.Principal, inbox uuid.UUID) ([]Template, error) {
	out := []Template{}
	if s.DB == nil {
		return nil, sectors.ErrForbidden
	}
	e := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		c, e := loadChannel(ctx, tx, p, inbox)
		if e != nil {
			return e
		}
		if c.driver != "meta_cloud" {
			return ErrUnsupported
		}
		rows, e := tx.Query(ctx, `SELECT external_id,name,language,category,state,components,sync_source FROM wa_templates WHERE tenant_id=$1 AND waba_id=$2 ORDER BY name,language`, p.TenantID, c.waba)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var t Template
			if e = rows.Scan(&t.ExternalID, &t.Name, &t.Language, &t.Category, &t.State, &t.Components, &t.Source); e != nil {
				return e
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, e
}

// SyncTemplates fetches outside transactions, then pins authorization and WABA again.
// Supervisors can inspect/use the catalog but cannot configure or synchronize the provider.
func (s Service) SyncTemplates(ctx context.Context, p sectors.Principal, inbox uuid.UUID) error {
	if s.DB == nil || s.Driver == nil {
		return ErrUnsupported
	}
	var waba uuid.UUID
	var remote string
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		c, e := loadChannel(ctx, tx, p, inbox)
		if e != nil {
			return e
		}
		if p.Role != "admin" {
			return sectors.ErrForbidden
		}
		if c.driver != "meta_cloud" {
			return ErrUnsupported
		}
		if waba != uuid.Nil && waba != c.waba {
			return sectors.ErrForbidden
		}
		waba = c.waba
		return tx.QueryRow(ctx, `SELECT waba_id FROM meta_wabas WHERE tenant_id=$1 AND id=$2`, p.TenantID, waba).Scan(&remote)
	}
	var generation int64
	if e := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if e := authorize(ctx, tx); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO channel_policy_catalog_sync(tenant_id,waba_id,requested_generation) VALUES($1,$2,1) ON CONFLICT(tenant_id,waba_id) DO UPDATE SET requested_generation=channel_policy_catalog_sync.requested_generation+1 RETURNING requested_generation`, p.TenantID, waba).Scan(&generation)
	}); e != nil {
		return e
	}
	items, e := s.Driver.Templates(ctx, remote)
	if e != nil {
		return errors.New("template driver failed")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, t := range items {
		var components []json.RawMessage
		key := t.Name + "\x00" + t.Language
		if t.ExternalID == "" || len(t.ExternalID) > 256 || t.Name == "" || len(t.Name) > 512 || t.Language == "" || len(t.Language) > 40 || t.Category == "" || len(t.Category) > 80 || t.State == "" || len(t.State) > 80 || seen[t.ExternalID] || names[key] || len(t.Components) > 65536 || json.Unmarshal(t.Components, &components) != nil || components == nil {
			return ErrInvalid
		}
		seen[t.ExternalID] = true
		names[key] = true
	}
	return s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Pin the reservation through replacement, including empty snapshots.
		if e := authorize(ctx, tx); e != nil {
			return e
		}
		var current int64
		if e := tx.QueryRow(ctx, `SELECT requested_generation FROM channel_policy_catalog_sync WHERE tenant_id=$1 AND waba_id=$2 FOR UPDATE`, p.TenantID, waba).Scan(&current); e != nil {
			return e
		}
		if current != generation {
			return ErrStaleCatalog
		}
		n, e := s.now(ctx, tx)
		if e != nil {
			return e
		}
		source := "driver"
		if s.Driver.IsFake() {
			source = "fake"
		}
		if _, e = tx.Exec(ctx, `DELETE FROM wa_templates WHERE tenant_id=$1 AND waba_id=$2`, p.TenantID, waba); e != nil {
			return e
		}
		for _, t := range items {
			if _, e = tx.Exec(ctx, `INSERT INTO wa_templates(tenant_id,waba_id,external_id,name,language,category,state,components,sync_source,synced_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, p.TenantID, waba, t.ExternalID, t.Name, t.Language, t.Category, t.State, t.Components, source, n); e != nil {
				return e
			}
		}
		_, e = tx.Exec(ctx, `UPDATE channel_policy_catalog_sync SET applied_generation=$3 WHERE tenant_id=$1 AND waba_id=$2`, p.TenantID, waba, generation)
		return e
	})
}

// SetPendingStateTx mirrors the outbox transition in the same transaction.
// Never moves an uncertain send back into the pending/cancellable set.
func SetPendingStateTx(ctx context.Context, tx pgx.Tx, p sectors.Principal, v Scope, id uuid.UUID, state string) error {
	if state != "uncertain" && state != "dispatched" && state != "cancelled" {
		return ErrInvalid
	}
	if e := lockScope(ctx, tx, p, v); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE channel_policy_pending SET state=$6 WHERE tenant_id=$1 AND id=$2 AND contact_id=$3 AND inbox_id=$4 AND purpose=$5 AND (state='pending' OR state=$6 OR (state='uncertain' AND $6='dispatched'))`, p.TenantID, id, v.ContactID, v.InboxID, v.Purpose, state)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalid
	}
	return nil
}
