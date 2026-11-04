// Package opsignals reads low-cardinality operational totals for the private
// metrics registry. It does not insert, update or delete, and it does not
// publish tenant, payload, phone, credential or message text.
package opsignals

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/metrics"
)

const (
	collectInterval  = 10 * time.Second
	statementLimitMS = "1500"
)

// collectTimeout bounds one read and the ROLLBACK that follows it.
// Tests may shorten it.
var collectTimeout = 2 * time.Second

type queryer interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// Collect reads database-wide totals from the platform pool until ctx ends.
// A nil pool does nothing. The API process, which has no jobs pool, does not
// call this: absence of the series is not a zero reading.
func Collect(ctx context.Context, pool *pgxpool.Pool, meter *metrics.Metrics) {
	if pool == nil || meter == nil {
		return
	}
	meter.EnableOps()
	collectOnce(ctx, pool, meter)
	ticker := time.NewTicker(collectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectOnce(ctx, pool, meter)
		}
	}
}

func collectOnce(ctx context.Context, queryer queryer, meter *metrics.Metrics) {
	if queryer == nil || meter == nil || ctx.Err() != nil {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()
	snapshot, err := query(queryCtx, queryer)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		meter.SetOpsCollectError()
		return
	}
	meter.SetOps(snapshot)
}

const scalarSQL = `
SELECT
  (SELECT count(*)::bigint FROM outbound_attempts WHERE state = 'uncertain'),
  (SELECT COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(created_at)))::float8, 0)
     FROM outbound_attempts WHERE state = 'uncertain'),
  (SELECT count(*)::bigint FROM send_reservations WHERE state = 'held'),
  (SELECT COALESCE(sum(expected_bytes), 0)::bigint FROM attachments WHERE state = 'validated'),
  (SELECT COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(created_at)))::float8, 0)
     FROM webhook_items WHERE state = 'pending')`

const jobSQL = `
SELECT queue_name, count(*)::bigint
  FROM tenant_job_reservations
 WHERE expires_at > clock_timestamp()
 GROUP BY queue_name`

const webhookSQL = `
SELECT state, count(*)::bigint
  FROM webhook_items
 GROUP BY state`

const connectorSQL = `
SELECT connector_type, status, count(*)::bigint
  FROM channel_connectors
 GROUP BY connector_type, status`

func query(ctx context.Context, queryer queryer) (snapshot metrics.OpsSnapshot, err error) {
	tx, err := queryer.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return metrics.OpsSnapshot{}, err
	}
	defer func() {
		rbErr := finishTx(ctx, tx)
		if rbErr == nil || err != nil {
			return
		}
		snapshot = metrics.OpsSnapshot{}
		err = rbErr
	}()
	if _, err = tx.Exec(ctx, `SELECT set_config('app.access_scope', 'platform', true)`); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, statementLimitMS); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	var uncertain int64
	var held int64
	var storage int64
	if err = tx.QueryRow(ctx, scalarSQL).Scan(
		&uncertain,
		&snapshot.UncertainOldestAge,
		&held,
		&storage,
		&snapshot.WebhookPendingAge,
	); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	snapshot.Uncertain = float64(uncertain)
	snapshot.SendReservationsHeld = float64(held)
	snapshot.StorageExpectedBytes = float64(storage)
	if snapshot.JobReservations, err = countByName(ctx, tx, jobSQL); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	if snapshot.WebhookItems, err = countByName(ctx, tx, webhookSQL); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	if snapshot.Connectors, err = countPairs(ctx, tx, connectorSQL); err != nil {
		return metrics.OpsSnapshot{}, err
	}
	return snapshot, nil
}

func countByName(ctx context.Context, tx pgx.Tx, sql string) ([]metrics.CountSample, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []metrics.CountSample
	for rows.Next() {
		var sample metrics.CountSample
		var count int64
		if err = rows.Scan(&sample.Name, &count); err != nil {
			return nil, err
		}
		sample.Value = float64(count)
		samples = append(samples, sample)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

func countPairs(ctx context.Context, tx pgx.Tx, sql string) ([]metrics.PairSample, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []metrics.PairSample
	for rows.Next() {
		var sample metrics.PairSample
		var count int64
		if err = rows.Scan(&sample.Left, &sample.Right, &count); err != nil {
			return nil, err
		}
		sample.Value = float64(count)
		samples = append(samples, sample)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

func finishTx(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return nil
	}
	rbCtx, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()
	return tx.Rollback(rbCtx)
}
