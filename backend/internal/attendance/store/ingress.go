package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// RegisterMeta writes the trusted app, WABA and phone binding for one tenant.
// The secret is ciphertext only.
func (Repo) RegisterMeta(ctx context.Context, tx pgx.Tx, in MetaBinding) error {
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return err
	}
	if in.AppRowID == uuid.Nil || in.WabaRowID == uuid.Nil || in.PhoneRowID == uuid.Nil || in.ConnectorID == uuid.Nil || in.InboxID == uuid.Nil {
		return ErrInvalid
	}
	appID, ok := text(in.AppID, 64)
	if !ok {
		return ErrInvalid
	}
	wabaID, ok := text(in.WabaID, 64)
	if !ok {
		return ErrInvalid
	}
	phoneID, ok := text(in.PhoneNumberID, 64)
	if !ok || in.Secret.KeyVersion == "" || len(in.Secret.Ciphertext) == 0 || in.Secret.AAD == "" {
		return ErrInvalid
	}
	registryID := in.RegistryID
	create := registryID == uuid.Nil
	if create {
		registryID = in.AppRowID
	} else if in.Capability == uuid.Nil {
		return ErrInvalid
	}
	var bindingID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT bind_meta_app($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		appID, in.AppRowID, registryID, in.Secret.KeyVersion, in.Secret.Ciphertext,
		in.AADScope, in.AADTenantID, in.AADEntity, in.AADEntityID, in.AADField,
		in.Secret.AAD, create, in.Capability).Scan(&bindingID)
	if err != nil {
		return err
	}
	if bindingID != in.AppRowID {
		return ErrInvalid
	}
	q := queries(tx)
	if err := q.InsertMetaWaba(ctx, dbgen.InsertMetaWabaParams{
		TenantID:  in.TenantID,
		ID:        in.WabaRowID,
		MetaAppID: in.AppRowID,
		WabaID:    wabaID,
	}); err != nil {
		return err
	}
	var display *string
	if in.DisplayNumber != "" {
		display = &in.DisplayNumber
	}
	return q.InsertMetaPhone(ctx, dbgen.InsertMetaPhoneParams{
		TenantID:      in.TenantID,
		ID:            in.PhoneRowID,
		MetaWabaID:    in.WabaRowID,
		PhoneNumberID: phoneID,
		DisplayNumber: display,
		ConnectorID:   in.ConnectorID,
		InboxID:       in.InboxID,
	})
}

// ResolvePhone reads the registry as ingress. The tenant comes from the binding,
// not from the caller.
func (Repo) ResolvePhone(ctx context.Context, tx pgx.Tx, appID, phoneNumberID string) (Route, error) {
	if err := requireIngress(ctx, tx); err != nil {
		return Route{}, err
	}
	appID, ok := text(appID, 64)
	if !ok {
		return Route{}, ErrInvalid
	}
	phoneNumberID, ok = text(phoneNumberID, 64)
	if !ok {
		return Route{}, ErrInvalid
	}
	row, err := queries(tx).ResolveMetaPhone(ctx, dbgen.ResolveMetaPhoneParams{AppID: appID, PhoneNumberID: phoneNumberID})
	if isNoRow(err) {
		return Route{}, ErrNotFound
	}
	if err != nil {
		return Route{}, err
	}
	return Route{TenantID: row.TenantID, InboxID: row.InboxID, ConnectorID: row.ConnectorID, PhoneNumberID: row.PhoneNumberID}, nil
}

// ReadAppSecret returns version, ciphertext and AAD for webhook verification.
func (Repo) ReadAppSecret(ctx context.Context, tx pgx.Tx, appID string) (Secret, error) {
	if err := requireIngress(ctx, tx); err != nil {
		return Secret{}, err
	}
	appID, ok := text(appID, 64)
	if !ok {
		return Secret{}, ErrInvalid
	}
	row, err := queries(tx).ReadMetaAppSecret(ctx, appID)
	if isNoRow(err) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, err
	}
	return Secret{KeyVersion: row.KeyVersion, Ciphertext: row.Ciphertext, AAD: row.Aad}, nil
}

// StoreDelivery persists the raw envelope before any tenant item is created.
// The same body hash replays the original id. reason quarantines an unknown phone.
func (Repo) StoreDelivery(ctx context.Context, tx pgx.Tx, appID string, body []byte, reason, remoteRef string) (Delivery, error) {
	if err := requireIngress(ctx, tx); err != nil {
		return Delivery{}, err
	}
	appID, ok := text(appID, 64)
	if !ok || len(body) == 0 || len(body) > 1048576 {
		return Delivery{}, ErrInvalid
	}
	state := "stored"
	if reason != "" {
		if reason != "unknown_phone" && reason != "incompatible_binding" {
			return Delivery{}, ErrInvalid
		}
		state = "quarantined"
	}
	sum := sha256.Sum256(body)
	q := queries(tx)
	id, err := q.InsertIngressDelivery(ctx, dbgen.InsertIngressDeliveryParams{
		ID:         uuid.New(),
		AppID:      appID,
		BodySha256: sum[:],
		Body:       body,
		State:      state,
	})
	if isNoRow(err) {
		existing, err := q.GetIngressDeliveryByHash(ctx, dbgen.GetIngressDeliveryByHashParams{AppID: appID, BodySha256: sum[:]})
		if err != nil {
			return Delivery{}, err
		}
		return Delivery{ID: existing.ID, State: existing.State, Replayed: true}, nil
	}
	if err != nil {
		return Delivery{}, err
	}
	if reason != "" {
		if err := q.InsertQuarantine(ctx, dbgen.InsertQuarantineParams{
			ID:         uuid.New(),
			DeliveryID: id,
			Reason:     reason,
			RemoteRef:  remoteRef,
		}); err != nil {
			return Delivery{}, err
		}
	}
	return Delivery{ID: id, State: state}, nil
}

// WebhookInput is one segregated item. TenantID must be the resolved route.
type WebhookInput struct {
	TenantID      uuid.UUID
	DeliveryID    uuid.UUID
	InboxID       uuid.UUID
	ConnectorID   uuid.UUID
	PhoneNumberID string
	ExternalKey   string
	Kind          string
	Payload       json.RawMessage
}

// WebhookResult is the item id. Replayed means the delivery key already existed.
type WebhookResult struct {
	ID       uuid.UUID
	Replayed bool
}

// PutWebhookItem inserts one tenant item. The ingress policy checks the phone binding.
// Payload is the segregated item, not the original multi-tenant envelope.
func (Repo) PutWebhookItem(ctx context.Context, tx pgx.Tx, in WebhookInput) (WebhookResult, error) {
	if err := requireIngress(ctx, tx); err != nil {
		return WebhookResult{}, err
	}
	key, ok := text(in.ExternalKey, 256)
	if !ok || in.TenantID == uuid.Nil || in.DeliveryID == uuid.Nil || in.InboxID == uuid.Nil || in.ConnectorID == uuid.Nil {
		return WebhookResult{}, ErrInvalid
	}
	if in.Kind != "message" && in.Kind != "status" && in.Kind != "other" {
		return WebhookResult{}, ErrInvalid
	}
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	q := queries(tx)
	id, err := q.InsertWebhookItem(ctx, dbgen.InsertWebhookItemParams{
		TenantID:      in.TenantID,
		ID:            uuid.New(),
		DeliveryID:    in.DeliveryID,
		InboxID:       in.InboxID,
		ConnectorID:   in.ConnectorID,
		PhoneNumberID: in.PhoneNumberID,
		ExternalKey:   key,
		Kind:          in.Kind,
		Payload:       payload,
	})
	if isNoRow(err) {
		existing, err := q.GetWebhookItem(ctx, dbgen.GetWebhookItemParams{DeliveryID: in.DeliveryID, ExternalKey: key})
		if err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{ID: existing, Replayed: true}, nil
	}
	if err != nil {
		return WebhookResult{}, err
	}
	return WebhookResult{ID: id}, nil
}

// ListWebhookItems reads items of the active tenant. It does not read the raw envelope.
func (Repo) ListWebhookItems(ctx context.Context, tx pgx.Tx, tenantID, inboxID uuid.UUID) ([]uuid.UUID, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	rows, err := queries(tx).ListWebhookItems(ctx, dbgen.ListWebhookItemsParams{TenantID: tenantID, InboxID: inboxID})
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// MarkDelivery moves a stored envelope to routed after items were written.
func (Repo) MarkDelivery(ctx context.Context, tx pgx.Tx, id uuid.UUID, state string) error {
	if err := requireIngress(ctx, tx); err != nil {
		return err
	}
	if id == uuid.Nil || (state != "stored" && state != "routed" && state != "quarantined") {
		return ErrInvalid
	}
	return queries(tx).MarkIngressDelivery(ctx, dbgen.MarkIngressDeliveryParams{State: state, ID: id})
}
