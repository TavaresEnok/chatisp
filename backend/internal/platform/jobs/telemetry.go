package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"chatisp/internal/platform/metrics"
)

const (
	riverCollectInterval = 10 * time.Second
	riverStatementLimit  = "1500"
)

// riverCollectTimeout bounds one backlog read and the ROLLBACK that follows it.
// The same deadline ends when the collector context is cancelled, so a stuck
// network command cannot hold shutdown. Tests may shorten it.
var riverCollectTimeout = 2 * time.Second

// WithMetrics counts finished work on the process registry.
// A nil meter, or a client without a worker pool, does not register series.
func WithMetrics(meter *metrics.Metrics) Option {
	return func(s *settings) { s.metrics = meter }
}

func attachWorkMetrics(cfg *river.Config, meter *metrics.Metrics, works bool) {
	if cfg == nil || meter == nil || !works {
		return
	}
	meter.EnableJobResults()
	hook := &workHook{metrics: meter}
	cfg.Hooks = append(cfg.Hooks, hook)
	// WorkEnd does not run when Work panics. The middleware sees that panic,
	// counts it once, and raises it again so River keeps its own recovery.
	cfg.Middleware = append(cfg.Middleware, hook)
}

type workHook struct {
	river.HookDefaults
	metrics *metrics.Metrics
}

// IsMiddleware marks the hook as worker middleware. HookDefaults already
// supplies the hook and plugin sentinels; embedding both defaults would make
// IsPlugin ambiguous.
func (h *workHook) IsMiddleware() bool { return true }

// Work counts a panic once and re-raises it. A normal return is counted by
// WorkEnd. Swallowing the panic would turn it into an ordinary error and
// change River's retry decision.
func (h *workHook) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		if h != nil && h.metrics != nil && job != nil {
			h.metrics.ObserveRiverResult(workerLabel(job.Kind), "error")
		}
		panic(recovered)
	}()
	return doInner(ctx)
}

// WorkEnd records the worker result and returns the same error River produced.
func (h *workHook) WorkEnd(_ context.Context, job *rivertype.JobRow, err error) error {
	if h != nil && h.metrics != nil && job != nil {
		h.metrics.ObserveRiverResult(workerLabel(job.Kind), resultLabel(err))
	}
	return err
}

func workerLabel(kind string) string {
	switch kind {
	case KindIngress:
		return "ingress"
	case KindInbound:
		return "inbound"
	case KindOutbound:
		return "outbound"
	case KindMedia:
		return "media"
	case KindWebhook:
		return "webhooks"
	case "chatisp.automation.tick.v1":
		return "automation"
	default:
		return "other"
	}
}

func resultLabel(err error) string {
	if err == nil {
		return "success"
	}
	var snooze *rivertype.JobSnoozeError
	if errors.As(err, &snooze) {
		return "snooze"
	}
	var cancel *rivertype.JobCancelError
	if errors.As(err, &cancel) {
		return "cancelled"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "error"
}

type riverQueryer interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// CollectRiverBacklog reads queue depth from the jobs pool until ctx ends.
// It does not open another database and does not insert, update or delete jobs.
// A nil pool does nothing, so an API process does not publish an empty backlog.
func CollectRiverBacklog(ctx context.Context, pool *pgxpool.Pool, meter *metrics.Metrics) {
	if pool == nil || meter == nil {
		return
	}
	meter.EnableRiverBacklog()
	collectRiverOnce(ctx, pool, meter)
	ticker := time.NewTicker(riverCollectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectRiverOnce(ctx, pool, meter)
		}
	}
}

func collectRiverOnce(ctx context.Context, queryer riverQueryer, meter *metrics.Metrics) {
	if queryer == nil || meter == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, riverCollectTimeout)
	defer cancel()
	samples, err := queryRiverBacklog(queryCtx, queryer)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		meter.SetRiverCollectError()
		return
	}
	meter.SetRiverBacklog(samples)
}

func queryRiverBacklog(ctx context.Context, queryer riverQueryer) (samples []metrics.RiverSample, err error) {
	tx, err := queryer.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() {
		rbErr := finishRiverTx(ctx, tx)
		if rbErr == nil || err != nil {
			return
		}
		// A read that did not finish the transaction is not a successful sample.
		samples = nil
		err = rbErr
	}()
	if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, riverStatementLimit); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT queue,
		       state::text,
		       count(*)::bigint,
		       COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(created_at)))::float8, 0)
		  FROM river.river_job
		 GROUP BY queue, state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sample metrics.RiverSample
		var count int64
		if err = rows.Scan(&sample.Queue, &sample.State, &count, &sample.OldestAgeSeconds); err != nil {
			return nil, err
		}
		sample.Jobs = float64(count)
		samples = append(samples, sample)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

// finishRiverTx rolls the read back under the collector deadline. pgx sends
// ROLLBACK on the network and only then returns the connection to the pool, so
// an unbounded context would stall the collector and the shutdown wait behind it.
// The context is a child of the read context: process cancellation ends it
// immediately, and it never outlives riverCollectTimeout. Any error, including
// a cancelled or failed ROLLBACK, is returned so the read is not published.
func finishRiverTx(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return nil
	}
	rbCtx, cancel := context.WithTimeout(ctx, riverCollectTimeout)
	defer cancel()
	return tx.Rollback(rbCtx)
}
