package pipeline

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/channel/whatsapp/metacloud"
)

// Quarantine reasons for a limit change that does not name one number.
const (
	reasonUnknownDisplay   = "unknown_display_number"
	reasonAmbiguousDisplay = "ambiguous_display_number"
)

// routeNumberLimit binds a phone_number_quality_update to one number.
// The webhook carries the WABA (entry.id) and the display number only. The
// trusted app, that WABA and the digits of the display number must match
// exactly one active number; zero or several matches are quarantined, never
// guessed. It returns the quarantine reason, or "" when the row was stored.
func routeNumberLimit(ctx context.Context, tx pgx.Tx, deliveryID uuid.UUID, item metacloud.Item) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT phone.tenant_id, phone.id
		FROM meta_phone_numbers AS phone
		JOIN meta_wabas AS waba
		  ON waba.tenant_id = phone.tenant_id AND waba.id = phone.meta_waba_id
		JOIN meta_apps AS app
		  ON app.tenant_id = waba.tenant_id AND app.id = waba.meta_app_id
		WHERE app.app_id = $1
		  AND waba.waba_id = $2
		  AND regexp_replace(coalesce(phone.display_number, ''), '[^0-9]', '', 'g') = $3
		  AND phone.status = 'active' AND waba.status = 'active' AND app.status = 'active'
		  AND current_setting('app.access_scope', true) = 'ingress'
		LIMIT 2
	`, item.AppID, item.WABAID, item.DisplayPhone)
	if err != nil {
		return "", err
	}
	type match struct{ tenant, phone uuid.UUID }
	var found []match
	for rows.Next() {
		var m match
		if err := rows.Scan(&m.tenant, &m.phone); err != nil {
			rows.Close()
			return "", err
		}
		found = append(found, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	reason := ""
	switch len(found) {
	case 0:
		reason = reasonUnknownDisplay
	case 1:
	default:
		reason = reasonAmbiguousDisplay
	}
	if reason != "" {
		ref := clipRef(item.WABAID + ":" + item.DisplayPhone)
		_, err := tx.Exec(ctx, `
			INSERT INTO ingress_quarantine (id, delivery_id, reason, remote_ref)
			VALUES ($1, $2, $3, $4)
		`, uuid.New(), deliveryID, reason, ref)
		return reason, err
	}
	var current, old *string
	if item.Limit.CurrentLimit != "" {
		current = &item.Limit.CurrentLimit
	}
	if item.Limit.OldLimit != "" {
		old = &item.Limit.OldLimit
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO meta_number_health (
			tenant_id, id, phone_id, kind, dedup_key,
			event, current_limit, old_limit, max_daily, received_at
		) VALUES ($1, $2, $3, 'limit', $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, dedup_key) DO NOTHING
	`, found[0].tenant, uuid.New(), found[0].phone, stableEventKey(item.DedupKey),
		item.Limit.Event, current, old, item.Limit.MaxDaily, item.IngestedAt)
	return "", err
}
