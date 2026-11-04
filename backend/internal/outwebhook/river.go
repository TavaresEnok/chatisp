package outwebhook

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// QueueName is the shared River queue fetched by the process jobs client.
	QueueName = "webhooks"
	// JobKind is the stable River kind inserted by the attendance trigger.
	JobKind = "chatisp.webhook.deliver.v1"
)

// JobsHandler is the webhook worker registered on the shared jobs client.
// Delivery snoozes through jobs.RetryAfterError so the shared leader keeps the attempt.
type JobsHandler struct {
	Deps Deps
}

// DeliverWebhook implements jobs.WebhookHandler.
func (h JobsHandler) DeliverWebhook(ctx context.Context, tenant uuid.UUID, eventType string, source uuid.UUID) error {
	err := DeliverSource(ctx, h.Deps, tenant, eventType, source)
	var retry ErrRetryAfter
	if errors.As(err, &retry) {
		wait := retry.After
		if wait < time.Second {
			wait = time.Second
		}
		return jobs.RetryAfterError{After: wait}
	}
	return err
}

// RecoverSources enqueues captured webhook origins that never received a River
// job. The insert and the enqueued_at mark commit together. A second call does
// not add another job for an origin already marked or still queued.
func RecoverSources(ctx context.Context, store *db.Store) (int, error) {
	if store == nil {
		return 0, ErrEvent
	}
	var n int
	err := store.WithTenantTx(ctx, recoverTenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT public.webhook_recover_sources()`).Scan(&n)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// recoverTenant only satisfies the application transaction scope. Recovery
// itself reads every tenant inside the security-definer function.
var recoverTenant = uuid.MustParse("00000000-0000-4000-8000-000000000044")
