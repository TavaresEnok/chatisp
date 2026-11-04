package pipeline

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/service"
	"chatisp/internal/outwebhook"
	"chatisp/internal/platform/crypto"
)

// PublishWebhookSource forwards one captured source to the webhook outbox.
func PublishWebhookSource(ctx context.Context, tx pgx.Tx, ring *crypto.Ring, tenant uuid.UUID, eventType string, source uuid.UUID) ([]outwebhook.Delivery, error) {
	return service.PublishWebhookSource(ctx, tx, ring, tenant, eventType, source)
}
