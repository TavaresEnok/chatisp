package outwebhook

import (
	"context"
	"errors"
	"strings"
	"time"

	"chatisp/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrStored = errors.New("webhook não encontrado")

// SecretContext binds the signing secret to one subscription.
func SecretContext(tenant, subscription uuid.UUID) crypto.Context {
	return crypto.Context{
		TenantID: tenant.String(),
		Entity:   "webhook_subscription",
		EntityID: subscription.String(),
		Field:    "signing_secret",
	}
}

func aadText(c crypto.Context) string {
	return strings.Join([]string{c.TenantID, c.Entity, c.EntityID, c.Field}, "\x1f")
}

// StoredSubscription is the sealed row. The plaintext is not a column.
type StoredSubscription struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	URL        string
	Version    string
	Ciphertext []byte
	AAD        string
	Events     map[string]bool
	Enabled    bool
}

// SealSubscription encrypts the signing secret for the row.
func SealSubscription(ring *crypto.Ring, sub Subscription) (StoredSubscription, error) {
	if ring == nil || sub.ID == uuid.Nil || sub.TenantID == uuid.Nil || len(sub.Secret) == 0 || sub.URL == "" {
		return StoredSubscription{}, ErrEvent
	}
	names := make([]string, 0, len(sub.Events))
	for name, on := range sub.Events {
		if on {
			if !knownEvent(name) {
				return StoredSubscription{}, ErrEvent
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return StoredSubscription{}, ErrEvent
	}
	aad := SecretContext(sub.TenantID, sub.ID)
	sealed, err := ring.Encrypt(sub.Secret, aad)
	if err != nil {
		return StoredSubscription{}, err
	}
	return StoredSubscription{
		ID: sub.ID, TenantID: sub.TenantID, URL: sub.URL,
		Version: sealed.Version, Ciphertext: sealed.Ciphertext, AAD: aadText(aad),
		Events: sub.Events, Enabled: sub.Enabled,
	}, nil
}

// InsertSubscription stores one sealed subscription.
func InsertSubscription(ctx context.Context, tx pgx.Tx, row StoredSubscription) error {
	names := make([]string, 0, len(row.Events))
	for name, on := range row.Events {
		if on {
			names = append(names, name)
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO webhook_subscriptions (
			tenant_id, id, url, secret_ciphertext, secret_key_version, secret_aad, events, enabled
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		row.TenantID, row.ID, row.URL, row.Ciphertext, row.Version, row.AAD, names, row.Enabled)
	return mapWH(err)
}

// LoadSubscription reads the sealed row.
func LoadSubscription(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (StoredSubscription, error) {
	var row StoredSubscription
	var names []string
	err := tx.QueryRow(ctx, `
		SELECT tenant_id, id, url, secret_ciphertext, secret_key_version, secret_aad, events, enabled
		FROM webhook_subscriptions
		WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(
		&row.TenantID, &row.ID, &row.URL, &row.Ciphertext, &row.Version, &row.AAD, &names, &row.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredSubscription{}, ErrStored
	}
	if err != nil {
		return StoredSubscription{}, mapWH(err)
	}
	row.Events = map[string]bool{}
	for _, name := range names {
		row.Events[name] = true
	}
	return row, nil
}

// OpenSecret decrypts the signing secret. A transplanted AAD does not open.
func OpenSecret(ring *crypto.Ring, row StoredSubscription) ([]byte, error) {
	aad := SecretContext(row.TenantID, row.ID)
	if row.AAD != aadText(aad) {
		return nil, errors.New("não foi possível ler o segredo")
	}
	return ring.Decrypt(row.Version, row.Ciphertext, aad)
}

// InsertDelivery inserts the stable event or returns the row already stored.
func InsertDelivery(ctx context.Context, tx pgx.Tx, d Delivery) (Delivery, error) {
	if d.ID == uuid.Nil || d.TenantID == uuid.Nil || len(d.Body) == 0 {
		return Delivery{}, ErrEvent
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (
			tenant_id, id, subscription_id, event_type, source_id, body, status, attempt_count, next_attempt_at, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (tenant_id, subscription_id, event_type, source_id) DO NOTHING
		RETURNING id`,
		d.TenantID, d.ID, d.SubscriptionID, d.Type, d.SourceID, d.Body, d.Status, d.Attempt, d.NextAt, d.OccurredAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoadDeliveryBySource(ctx, tx, d.TenantID, d.SubscriptionID, d.Type, d.SourceID)
	}
	if err != nil {
		return Delivery{}, mapWH(err)
	}
	d.ID = id
	return d, nil
}

// LoadDelivery reads one event by its stable id.
func LoadDelivery(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Delivery, error) {
	var d Delivery
	var last *int32
	err := tx.QueryRow(ctx, `
		SELECT id, tenant_id, subscription_id, event_type, source_id, body, status, attempt_count, next_attempt_at, occurred_at, last_status
		FROM webhook_deliveries
		WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(
		&d.ID, &d.TenantID, &d.SubscriptionID, &d.Type, &d.SourceID, &d.Body, &d.Status, &d.Attempt, &d.NextAt, &d.OccurredAt, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrStored
	}
	if err != nil {
		return Delivery{}, mapWH(err)
	}
	if last != nil {
		d.LastStatus = int(*last)
	}
	return d, nil
}

// LoadDeliveryBySource reads the unique event of one source.
func LoadDeliveryBySource(ctx context.Context, tx pgx.Tx, tenant, subscription uuid.UUID, eventType string, source uuid.UUID) (Delivery, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM webhook_deliveries
		WHERE tenant_id = $1 AND subscription_id = $2 AND event_type = $3 AND source_id = $4`,
		tenant, subscription, eventType, source).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrStored
	}
	if err != nil {
		return Delivery{}, mapWH(err)
	}
	return LoadDelivery(ctx, tx, tenant, id)
}

// claimLease is how long a worker may post before another worker may take the row.
const claimLease = 30 * time.Second

// attemptBudget ends the try before the lease, so a slow POST cannot still be
// in flight when another worker is allowed to claim the same row.
const attemptBudget = claimLease - 10*time.Second

// ClaimDelivery leases one due pending row. A fresh lease held by another
// worker, or a row that is no longer pending, returns false.
func ClaimDelivery(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, now time.Time) (Delivery, bool, error) {
	token := uuid.New()
	var d Delivery
	var last *int32
	err := tx.QueryRow(ctx, `
		UPDATE webhook_deliveries
		SET claim_token = $3, claimed_at = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		  AND status = 'pending'
		  AND next_attempt_at <= $4
		  AND (claim_token IS NULL OR claimed_at < $4::timestamptz - make_interval(secs => $5))
		RETURNING id, tenant_id, subscription_id, event_type, source_id, body, status, attempt_count, next_attempt_at, occurred_at, last_status, claim_token`,
		tenant, id, token, now, int(claimLease/time.Second)).Scan(
		&d.ID, &d.TenantID, &d.SubscriptionID, &d.Type, &d.SourceID, &d.Body, &d.Status, &d.Attempt, &d.NextAt, &d.OccurredAt, &last, &d.Claim)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, mapWH(err)
	}
	if last != nil {
		d.LastStatus = int(*last)
	}
	return d, true, nil
}

// UpdateDelivery stores the attempt result while the row is still pending.
// A delivered or dead row is left untouched, so a slower worker cannot reopen it.
// The id and body stay unchanged. A claimed attempt must present its lease.
func UpdateDelivery(ctx context.Context, tx pgx.Tx, d Delivery) error {
	var last any
	if d.LastStatus > 0 {
		last = d.LastStatus
	}
	var tag pgconn.CommandTag
	var err error
	if d.Claim != uuid.Nil {
		tag, err = tx.Exec(ctx, `
			UPDATE webhook_deliveries
			SET status = $3, attempt_count = $4, next_attempt_at = $5, last_status = $6,
			    claim_token = NULL, claimed_at = NULL, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND body = $7 AND status = 'pending' AND claim_token = $8`,
			d.TenantID, d.ID, d.Status, d.Attempt, d.NextAt, last, d.Body, d.Claim)
	} else {
		tag, err = tx.Exec(ctx, `
			UPDATE webhook_deliveries
			SET status = $3, attempt_count = $4, next_attempt_at = $5, last_status = $6, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND body = $7 AND status = 'pending' AND claim_token IS NULL`,
			d.TenantID, d.ID, d.Status, d.Attempt, d.NextAt, last, d.Body)
	}
	if err != nil {
		return mapWH(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	current, err := LoadDelivery(ctx, tx, d.TenantID, d.ID)
	if err != nil {
		return err
	}
	if current.Status == StatusDelivered || current.Status == StatusDead {
		return nil
	}
	return ErrStored
}

func mapWH(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrEvent
	}
	return errors.New("falha ao gravar o webhook")
}
