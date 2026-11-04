package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/outwebhook"
	"chatisp/internal/platform/crypto"
)

// PublishWebhookSource materializes outbound deliveries for one captured source.
// The attendance transaction already recorded the source. A nil transaction or
// key ring is refused and does not write a delivery.
func PublishWebhookSource(ctx context.Context, tx pgx.Tx, ring *crypto.Ring, tenant uuid.UUID, eventType string, source uuid.UUID) ([]outwebhook.Delivery, error) {
	if tx == nil || ring == nil {
		return nil, outwebhook.ErrEvent
	}
	return outwebhook.Materialize(ctx, tx, ring, tenant, eventType, source)
}
