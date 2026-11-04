package outwebhook

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Materialize writes one signed delivery per enabled subscription for a source
// captured in the attendance transaction. The body does not include message text.
// A source that was already materialized returns the stored rows.
func Materialize(ctx context.Context, tx pgx.Tx, ring *crypto.Ring, tenant uuid.UUID, eventType string, source uuid.UUID) ([]Delivery, error) {
	if tx == nil || ring == nil || tenant == uuid.Nil || source == uuid.Nil || !knownEvent(eventType) {
		return nil, ErrEvent
	}
	var occurred time.Time
	var conversation uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT occurred_at, conversation_id
		FROM webhook_event_sources
		WHERE tenant_id = $1 AND event_type = $2 AND source_id = $3
	`, tenant, eventType, source).Scan(&occurred, &conversation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStored
	}
	if err != nil {
		return nil, err
	}
	subs, err := ListSubscriptions(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	for _, stored := range subs {
		if !stored.Enabled || !stored.Events[eventType] {
			continue
		}
		secret, err := OpenSecret(ring, stored)
		if err != nil {
			return nil, err
		}
		delivery, err := Enqueue(Subscription{
			ID: stored.ID, TenantID: stored.TenantID, URL: stored.URL,
			Secret: secret, Events: stored.Events, Enabled: stored.Enabled,
		}, eventType, source, conversation, occurred)
		if err != nil {
			return nil, err
		}
		if _, err = InsertDelivery(ctx, tx, delivery); err != nil {
			return nil, err
		}
	}
	return ListDeliveriesBySource(ctx, tx, tenant, eventType, source)
}

// ListSubscriptions returns every subscription of the tenant, enabled or not.
func ListSubscriptions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]StoredSubscription, error) {
	rows, err := tx.Query(ctx, `
		SELECT tenant_id, id, url, secret_ciphertext, secret_key_version, secret_aad, events, enabled
		FROM webhook_subscriptions
		WHERE tenant_id = $1
		ORDER BY created_at, id
	`, tenant)
	if err != nil {
		return nil, mapWH(err)
	}
	defer rows.Close()
	out := []StoredSubscription{}
	for rows.Next() {
		row, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mapWH(err)
	}
	return out, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSubscription(row scannable) (StoredSubscription, error) {
	var stored StoredSubscription
	var names []string
	if err := row.Scan(&stored.TenantID, &stored.ID, &stored.URL, &stored.Ciphertext, &stored.Version, &stored.AAD, &names, &stored.Enabled); err != nil {
		return StoredSubscription{}, mapWH(err)
	}
	stored.Events = map[string]bool{}
	for _, name := range names {
		stored.Events[name] = true
	}
	return stored, nil
}

// ListDeliveriesBySource returns the deliveries of one captured source.
func ListDeliveriesBySource(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, eventType string, source uuid.UUID) ([]Delivery, error) {
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM webhook_deliveries
		WHERE tenant_id = $1 AND event_type = $2 AND source_id = $3
		ORDER BY id
	`, tenant, eventType, source)
	if err != nil {
		return nil, mapWH(err)
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapWH(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapWH(err)
	}
	out := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		item, err := LoadDelivery(ctx, tx, tenant, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// ListDeliveries returns attempts for one subscription, newest first.
func ListDeliveries(ctx context.Context, tx pgx.Tx, tenant, subscription uuid.UUID) ([]Delivery, error) {
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM webhook_deliveries
		WHERE tenant_id = $1 AND subscription_id = $2
		ORDER BY created_at DESC, id
		LIMIT 100
	`, tenant, subscription)
	if err != nil {
		return nil, mapWH(err)
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapWH(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapWH(err)
	}
	out := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		item, err := LoadDelivery(ctx, tx, tenant, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// SetEnabled turns a subscription on or off. The signing secret stays stored.
func SetEnabled(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, enabled bool) error {
	tag, err := tx.Exec(ctx, `
		UPDATE webhook_subscriptions
		SET enabled = $3
		WHERE tenant_id = $1 AND id = $2
	`, tenant, id, enabled)
	if err != nil {
		return mapWH(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStored
	}
	return nil
}
