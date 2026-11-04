package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

const (
	defaultSoftStop  = 5 * time.Second
	defaultJobBudget = 30 * time.Second
	discardedKeep    = 30 * 24 * time.Hour
	errorTextLimit   = 160

	workersIngress  = 2
	workersInbound  = 4
	workersOutbound = 2
)

// Option adjusts a client. Production defaults already bound retries, job time
// and shutdown.
type Option func(*settings)

type settings struct {
	logger     *slog.Logger
	softStop   time.Duration
	jobTimeout time.Duration
	poll       time.Duration
	cooldown   time.Duration
	retry      river.ClientRetryPolicy
	testOnly   bool
	leaseWait  time.Duration
	discardFor time.Duration
}

// WithLogger sets the process logger. Argument, metadata and error values are
// omitted before they reach it.
func WithLogger(logger *slog.Logger) Option {
	return func(s *settings) { s.logger = logger }
}

// WithTestTiming shortens fetch and retry delays. It is for the package tests.
func WithTestTiming(poll time.Duration, retry river.ClientRetryPolicy, leaseWait time.Duration) Option {
	return func(s *settings) {
		s.poll = poll
		s.cooldown = poll / 2
		if s.cooldown < time.Millisecond {
			s.cooldown = time.Millisecond
		}
		if s.poll < s.cooldown {
			s.poll = s.cooldown
		}
		s.retry = retry
		s.leaseWait = leaseWait
		s.testOnly = true
		s.softStop = 200 * time.Millisecond
	}
}

// Client inserts jobs on a caller transaction and, when given a worker pool,
// works the ingress, inbound and outbound queues.
type Client struct {
	inner *river.Client[pgx.Tx]
	works bool
}

// New builds a client. A nil pool can insert with InsertTx and cannot Start.
// A non-nil pool is the worker pool and must not be the migration owner.
func New(pool *pgxpool.Pool, handlers Handlers, opts ...Option) (*Client, error) {
	cfg := settings{
		softStop:   defaultSoftStop,
		jobTimeout: defaultJobBudget,
		retry:      Backoff{},
		leaseWait:  LeaseSnooze,
		discardFor: discardedKeep,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &automationWorker{handler: handlers.Automation})
	river.AddWorker(workers, &ingressWorker{handler: handlers.Ingress})
	river.AddWorker(workers, &inboundWorker{handler: handlers.Inbound})
	river.AddWorker(workers, &outboundWorker{
		handler: handlers.Outbound,
		lease:   handlers.Lease,
		wait:    cfg.leaseWait,
	})
	riverCfg := &river.Config{
		Schema:                      SchemaName,
		Workers:                     workers,
		Logger:                      newLogger(cfg.logger),
		MaxAttempts:                 MaxAttempts,
		RetryPolicy:                 cfg.retry,
		JobTimeout:                  cfg.jobTimeout,
		SoftStopTimeout:             cfg.softStop,
		DiscardedJobRetentionPeriod: cfg.discardFor,
		ErrorHandler:                silentErrors{},
		TestOnly:                    cfg.testOnly,
	}
	works := pool != nil
	if works {
		riverCfg.Queues = map[string]river.QueueConfig{
			QueueIngress:  {MaxWorkers: workersIngress},
			QueueInbound:  {MaxWorkers: workersInbound},
			QueueOutbound: {MaxWorkers: workersOutbound},
			"scheduled":   {MaxWorkers: 1},
		}
		if handlers.Automation != nil {
			riverCfg.PeriodicJobs = []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(10*time.Second), func() (river.JobArgs, *river.InsertOpts) { return AutomationArgs{V: SchemaVersion}, nil }, &river.PeriodicJobOpts{ID: "lifecycle", RunOnStart: true})}
		}
		if cfg.poll > 0 {
			riverCfg.FetchCooldown = cfg.cooldown
			riverCfg.FetchPollInterval = cfg.poll
		}
	}
	inner, err := river.NewClient(riverpgxv5.New(pool), riverCfg)
	if err != nil {
		return nil, fmt.Errorf("falha ao montar cliente River: %w", err)
	}
	return &Client{inner: inner, works: works}, nil
}

// Start begins fetch and maintenance on the worker pool.
func (c *Client) Start(ctx context.Context) error {
	if c == nil || c.inner == nil || !c.works {
		return errWorkerPool
	}
	if err := c.inner.Start(ctx); err != nil {
		return fmt.Errorf("falha ao iniciar worker River: %w", err)
	}
	return nil
}

// Stop stops new fetches and waits until the context ends or the soft stop
// cancels running jobs. An interrupted job is put back on its queue.
func (c *Client) Stop(ctx context.Context) error {
	if c == nil || c.inner == nil || !c.works {
		return nil
	}
	if err := c.inner.Stop(ctx); err != nil {
		return fmt.Errorf("falha ao parar worker River: %w", err)
	}
	return nil
}

// StopAndCancel cancels active jobs and waits within ctx for River to stop.
func (c *Client) StopAndCancel(ctx context.Context) error { return c.inner.StopAndCancel(ctx) }

// Stopped closes only after River has stopped its workers and services.
func (c *Client) Stopped() <-chan struct{} { return c.inner.Stopped() }

// EnqueueIngress inserts an ingress job on tx and does not commit tx.
func (c *Client) EnqueueIngress(ctx context.Context, tx pgx.Tx, deliveryID uuid.UUID) (int64, error) {
	args := IngressArgs{V: SchemaVersion, DeliveryID: deliveryID}
	if err := args.validate(); err != nil {
		return 0, err
	}
	return c.insert(ctx, tx, args)
}

// EnqueueInbound inserts a tenant job on tx and does not commit tx.
func (c *Client) EnqueueInbound(ctx context.Context, tx pgx.Tx, tenantID, itemID uuid.UUID) (int64, error) {
	args := InboundArgs{V: SchemaVersion, TenantID: tenantID, ItemID: itemID}
	if err := args.validate(); err != nil {
		return 0, err
	}
	return c.insert(ctx, tx, args)
}

// EnqueueOutbound inserts a tenant dispatch job on tx and does not commit tx.
func (c *Client) EnqueueOutbound(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID) (int64, error) {
	args := OutboundArgs{V: SchemaVersion, TenantID: tenantID, AttemptID: attemptID}
	if err := args.validate(); err != nil {
		return 0, err
	}
	return c.insert(ctx, tx, args)
}

func (c *Client) insert(ctx context.Context, tx pgx.Tx, args river.JobArgs) (int64, error) {
	if c == nil || c.inner == nil {
		return 0, errors.New("cliente de jobs indisponível")
	}
	if tx == nil {
		return 0, errTxRequired
	}
	result, err := c.inner.InsertTx(ctx, tx, args, &river.InsertOpts{Metadata: []byte(metadataVersion)})
	if err != nil {
		return 0, fmt.Errorf("falha ao enfileirar job: %w", err)
	}
	if result == nil || result.Job == nil {
		return 0, errors.New("falha ao enfileirar job")
	}
	return result.Job.ID, nil
}

// ExhaustedJob is a discarded job without its arguments.
type ExhaustedJob struct {
	ID          int64
	Kind        string
	Queue       string
	Attempt     int
	MaxAttempts int
	State       string
	LastError   string
}

// Exhausted lists discarded jobs. River keeps them in river_job. This does
// not use a paid dead-letter add-on. Arguments are not returned.
func (c *Client) Exhausted(ctx context.Context, limit int) ([]ExhaustedJob, error) {
	if c == nil || c.inner == nil || !c.works {
		return nil, errWorkerPool
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	page, err := c.inner.JobList(ctx, river.NewJobListParams().States(rivertype.JobStateDiscarded).First(limit))
	if err != nil {
		return nil, fmt.Errorf("falha ao listar jobs esgotados: %w", err)
	}
	out := make([]ExhaustedJob, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		if job == nil {
			continue
		}
		item := ExhaustedJob{
			ID:          job.ID,
			Kind:        job.Kind,
			Queue:       job.Queue,
			Attempt:     job.Attempt,
			MaxAttempts: job.MaxAttempts,
			State:       string(job.State),
		}
		if n := len(job.Errors); n > 0 {
			item.LastError = clip(job.Errors[n-1].Error)
		}
		out = append(out, item)
	}
	return out, nil
}

func clip(text string) string {
	if utf8.RuneCountInString(text) <= errorTextLimit {
		return text
	}
	runes := []rune(text)
	return string(runes[:errorTextLimit])
}

type silentErrors struct{}

func (silentErrors) HandleError(context.Context, *rivertype.JobRow, error) *river.ErrorHandlerResult {
	return nil
}

func (silentErrors) HandlePanic(context.Context, *rivertype.JobRow, any, string) *river.ErrorHandlerResult {
	return nil
}
