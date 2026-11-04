package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	// RiverModule and RiverVersion are the open-source module pinned in go.mod.
	RiverModule  = "github.com/riverqueue/river"
	RiverVersion = "v0.50.0"

	// PgxModule and PgxVersion are the driver River v0.50.0 requires.
	PgxModule  = "github.com/jackc/pgx/v5"
	PgxVersion = "v5.11.0"

	// WebsocketModule is the direct dependency reserved for the panel socket.
	WebsocketModule  = "github.com/coder/websocket"
	WebsocketVersion = "v1.8.15"

	// SchemaName is the Postgres schema that holds River tables.
	SchemaName = "river"

	// MigrationVersion is the main line shipped with River v0.50.0.
	MigrationVersion = 8

	// SchemaVersion is the args and metadata version written by this package.
	SchemaVersion = 1

	// MaxAttempts bounds ordinary retries. Snoozes do not consume an attempt.
	MaxAttempts = 8

	RetryBase = time.Second
	RetryCap  = 2 * time.Minute

	// LeaseSnooze is how long an outbound job waits when the domain lease
	// refuses it. River's process-wide MaxWorkers is not that limit.
	LeaseSnooze = 5 * time.Second

	QueueIngress  = "ingress"
	QueueInbound  = "inbound"
	QueueOutbound = "outbound"
	// QueueWebhooks is the outbound webhook queue. It shares this client so the
	// leader that rescues stuck jobs knows the kind.
	QueueWebhooks = "webhooks"

	KindIngress  = "chatisp.ingress.expand.v1"
	KindInbound  = "chatisp.inbound.apply.v1"
	KindOutbound = "chatisp.outbound.dispatch.v1"
	KindWebhook  = "chatisp.webhook.deliver.v1"

	priorityInbound  = 1
	priorityIngress  = 2
	priorityOutbound = 3

	roleOwner    = "chatisp_owner"
	roleApp      = "chatisp_app"
	rolePlatform = "chatisp_platform"
	roleIngress  = "chatisp_ingress"
	// RoleJobs is the worker role. Cluster creation is outside this package
	// because chatisp_owner cannot create roles. Migrate only grants to it.
	RoleJobs = "chatisp_jobs"
)

var (
	errInvalidArgs     = errors.New("argumentos de job inválidos")
	errHandlerMissing  = errors.New("handler de job não registrado")
	errLeaseMissing    = errors.New("reserva de envio não integrada")
	errTxRequired      = errors.New("transação obrigatória para enfileirar")
	errWorkerPool      = errors.New("pool de jobs indisponível")
	errMigrateIdentity = errors.New("migração River exige chatisp_owner sem superuser e sem BYPASSRLS")
	errJobsRole        = errors.New("papel chatisp_jobs ausente ou privilegiado demais")
	errEnqueueRole     = errors.New("papel de enfileiramento ausente")
)

// IngressHandler expands one stored envelope. The job carries no tenant.
type IngressHandler interface {
	ExpandIngress(ctx context.Context, deliveryID uuid.UUID) error
}

// InboundHandler applies one tenant-scoped ingress item.
type InboundHandler interface {
	ApplyInbound(ctx context.Context, tenantID, itemID uuid.UUID) error
}

// OutboundHandler dispatches one outbound attempt after the domain lease allows it.
type OutboundHandler interface {
	DispatchOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) error
}

// OutboundLease is the domain reservation for a tenant and number.
// allowed false means the dispatcher must wait. A River queue size does not
// replace this decision.
type OutboundLease interface {
	AllowOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) (allowed bool, err error)
}

// WebhookHandler delivers one captured outbound webhook source.
type WebhookHandler interface {
	DeliverWebhook(ctx context.Context, tenantID uuid.UUID, eventType string, sourceID uuid.UUID) error
}

// RetryAfterError tells the webhook worker to snooze. The attempt is kept.
type RetryAfterError struct {
	After time.Duration
}

func (e RetryAfterError) Error() string { return "entrega adiada" }

// Handlers are supplied by ingress and attendance. Nil means that kind is not
// wired yet. Work then fails closed instead of succeeding as a no-op.
// Webhook is still registered on every working client so a leader does not
// discard that kind. The webhooks queue is fetched only when Webhook is set.
type Handlers struct {
	Ingress    IngressHandler
	Inbound    InboundHandler
	Outbound   OutboundHandler
	Lease      OutboundLease
	Automation AutomationHandler
	Webhook    WebhookHandler
}

// IngressArgs identifies one authenticated envelope. It has no tenant.
type IngressArgs struct {
	V          int       `json:"v"`
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (IngressArgs) Kind() string { return KindIngress }

// InboundArgs identifies one tenant item.
type InboundArgs struct {
	V        int       `json:"v"`
	TenantID uuid.UUID `json:"tenant_id"`
	ItemID   uuid.UUID `json:"item_id"`
}

func (InboundArgs) Kind() string { return KindInbound }

// OutboundArgs identifies one outbound attempt. The message body stays in the
// domain row, not in the job.
type OutboundArgs struct {
	V         int       `json:"v"`
	TenantID  uuid.UUID `json:"tenant_id"`
	AttemptID uuid.UUID `json:"attempt_id"`
}

func (OutboundArgs) Kind() string { return KindOutbound }

// WebhookArgs is one captured source. The JSON shape matches the trigger in
// migration 00044. The signed body is loaded later from the domain row.
type WebhookArgs struct {
	V         int       `json:"v"`
	TenantID  uuid.UUID `json:"tenant_id"`
	EventType string    `json:"event_type"`
	SourceID  uuid.UUID `json:"source_id"`
}

func (WebhookArgs) Kind() string { return KindWebhook }

func (a WebhookArgs) validate() error {
	if a.V != SchemaVersion || a.TenantID == uuid.Nil || a.SourceID == uuid.Nil || a.EventType == "" {
		return errInvalidArgs
	}
	return nil
}

func (a IngressArgs) validate() error {
	if a.V != SchemaVersion || a.DeliveryID == uuid.Nil {
		return errInvalidArgs
	}
	return nil
}

func (a InboundArgs) validate() error {
	if a.V != SchemaVersion || a.TenantID == uuid.Nil || a.ItemID == uuid.Nil {
		return errInvalidArgs
	}
	return nil
}

func (a OutboundArgs) validate() error {
	if a.V != SchemaVersion || a.TenantID == uuid.Nil || a.AttemptID == uuid.Nil {
		return errInvalidArgs
	}
	return nil
}

// AutomationHandler runs durable lifecycle jobs with a worker clock.
type AutomationHandler interface{ RunAutomations(context.Context) error }
