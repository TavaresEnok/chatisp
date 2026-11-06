package opsalert

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	jobScanSQL = `
SELECT id, kind, queue, state::text, created_at
FROM river.river_job
WHERE state IN ('available', 'running', 'retryable', 'scheduled', 'pending')
  AND created_at < $2
  AND args->>'tenant_id' = $1
ORDER BY created_at
LIMIT $3`

	connectorScanSQL = `
SELECT id, connector_type, status
FROM channel_connectors
WHERE tenant_id = $1
  AND status = 'disconnected'
ORDER BY id
LIMIT $2`

	webhookScanSQL = `
SELECT id, kind, state, created_at
FROM webhook_items
WHERE tenant_id = $1
  AND state = 'pending'
  AND created_at < $2
ORDER BY created_at
LIMIT $3`
)

var (
	// ErrInvalid é um limiar fora da faixa gravável.
	ErrInvalid = errors.New("limite inválido")
	// ErrConflict é uma versão de configuração desatualizada.
	ErrConflict = errors.New("versão conflitante")
)

// View é a leitura do admin: limites gravados e alertas abertos. Sem linha de
// configuração, os segundos ficam nulos e job/webhook não alertam.
type View struct {
	JobAgeSeconds     *int    `json:"job_age_seconds"`
	WebhookAgeSeconds *int    `json:"webhook_age_seconds"`
	SettingsVersion   int64   `json:"settings_version"`
	Configured        bool    `json:"configured"`
	Alerts            []Alert `json:"alerts"`
}

// Refresh lê o estado persistido, grava os alertas locais e devolve os abertos.
func Refresh(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, now time.Time) (View, error) {
	if tenantID == uuid.Nil {
		return View{}, ErrInvalid
	}
	now = now.UTC()
	limits, view, err := loadLimits(ctx, tx, tenantID)
	if err != nil {
		return View{}, err
	}
	facts, err := loadFacts(ctx, tx, tenantID, limits, now)
	if err != nil {
		return View{}, err
	}
	alerts := Derive(limits, facts)
	if err := sync(ctx, tx, tenantID, now, alerts); err != nil {
		return View{}, err
	}
	view.Alerts = alerts
	return view, nil
}

// SaveLimits grava os dois limiares e atualiza os alertas na mesma transação.
// expectedVersion 0 insere a primeira linha. Outro valor exige a versão atual.
func SaveLimits(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, jobSeconds, webhookSeconds int, expectedVersion int64, now time.Time) (View, error) {
	if tenantID == uuid.Nil || !ValidAge(jobSeconds) || !ValidAge(webhookSeconds) || expectedVersion < 0 {
		return View{}, ErrInvalid
	}
	if expectedVersion == 0 {
		_, err := tx.Exec(ctx, `
INSERT INTO ops_alert_settings (tenant_id, job_age_seconds, webhook_age_seconds, version)
VALUES ($1, $2, $3, 1)`, tenantID, jobSeconds, webhookSeconds)
		if isUnique(err) {
			return View{}, ErrConflict
		}
		if err != nil {
			return View{}, err
		}
	} else {
		tag, err := tx.Exec(ctx, `
UPDATE ops_alert_settings
SET job_age_seconds = $3, webhook_age_seconds = $4, version = version + 1
WHERE tenant_id = $1 AND version = $2`, tenantID, expectedVersion, jobSeconds, webhookSeconds)
		if err != nil {
			return View{}, err
		}
		if tag.RowsAffected() == 0 {
			return View{}, ErrConflict
		}
	}
	return Refresh(ctx, tx, tenantID, now)
}

func loadLimits(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (Limits, View, error) {
	view := View{Alerts: []Alert{}}
	var jobSeconds, webhookSeconds int
	var version int64
	err := tx.QueryRow(ctx, `
SELECT job_age_seconds, webhook_age_seconds, version
FROM ops_alert_settings
WHERE tenant_id = $1`, tenantID).Scan(&jobSeconds, &webhookSeconds, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Limits{}, view, nil
	}
	if err != nil {
		return Limits{}, View{}, err
	}
	view.Configured = true
	view.JobAgeSeconds = &jobSeconds
	view.WebhookAgeSeconds = &webhookSeconds
	view.SettingsVersion = version
	return Limits{
		JobAge:     time.Duration(jobSeconds) * time.Second,
		WebhookAge: time.Duration(webhookSeconds) * time.Second,
	}, view, nil
}

func loadFacts(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, limits Limits, now time.Time) (Facts, error) {
	var facts Facts
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suspended_ingress_alerts WHERE tenant_id=$1),
        coalesce((SELECT admission_blocked FROM suspended_ingress_alerts WHERE tenant_id=$1),false)`, tenantID).Scan(&facts.SuspendedIngress, &facts.SuspendedAdmissionBlocked); err != nil {
		return Facts{}, err
	}
	if limits.JobAge > 0 {
		ok, err := relationExists(ctx, tx, "river.river_job")
		if err != nil {
			return Facts{}, err
		}
		if ok {
			jobs, err := loadJobs(ctx, tx, tenantID, now.Add(-limits.JobAge), now)
			if err != nil {
				return Facts{}, err
			}
			facts.Jobs = jobs
		}
	}
	connectors, err := loadConnectors(ctx, tx, tenantID)
	if err != nil {
		return Facts{}, err
	}
	facts.Connectors = connectors
	ok, err := relationExists(ctx, tx, "public.webhook_items")
	if err != nil {
		return Facts{}, err
	}
	facts.WebhookQueue = ok
	if ok && limits.WebhookAge > 0 {
		items, err := loadWebhooks(ctx, tx, tenantID, now.Add(-limits.WebhookAge), now)
		if err != nil {
			return Facts{}, err
		}
		facts.Webhooks = items
	}
	return facts, nil
}

func loadJobs(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, olderThan, now time.Time) ([]JobFact, error) {
	rows, err := tx.Query(ctx, jobScanSQL, tenantID.String(), olderThan, ListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []JobFact
	for rows.Next() {
		var fact JobFact
		var created time.Time
		if err := rows.Scan(&fact.ID, &fact.Kind, &fact.Queue, &fact.State, &created); err != nil {
			return nil, err
		}
		fact.Age = now.Sub(created)
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func loadConnectors(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]ConnectorFact, error) {
	rows, err := tx.Query(ctx, connectorScanSQL, tenantID, ListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []ConnectorFact
	for rows.Next() {
		var fact ConnectorFact
		if err := rows.Scan(&fact.ID, &fact.Type, &fact.Status); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func loadWebhooks(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, olderThan, now time.Time) ([]WebhookFact, error) {
	rows, err := tx.Query(ctx, webhookScanSQL, tenantID, olderThan, ListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []WebhookFact
	for rows.Next() {
		var fact WebhookFact
		var created time.Time
		if err := rows.Scan(&fact.ID, &fact.Kind, &fact.State, &created); err != nil {
			return nil, err
		}
		fact.Age = now.Sub(created)
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func relationExists(ctx context.Context, tx pgx.Tx, name string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok)
	return ok, err
}

func sync(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, now time.Time, alerts []Alert) error {
	open := make(map[string]struct{}, len(alerts))
	for _, alert := range alerts {
		open[alert.Signal+"\x00"+alert.SubjectKey] = struct{}{}
		_, err := tx.Exec(ctx, `
INSERT INTO ops_alerts (tenant_id, id, signal, subject_key, label, opened_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT ON CONSTRAINT ops_alerts_subject_key DO UPDATE
SET label = EXCLUDED.label,
    opened_at = CASE WHEN ops_alerts.resolved_at IS NULL THEN ops_alerts.opened_at ELSE EXCLUDED.opened_at END,
    resolved_at = NULL`, tenantID, uuid.New(), alert.Signal, alert.SubjectKey, alert.Label, now)
		if err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `
SELECT signal, subject_key
FROM ops_alerts
WHERE tenant_id = $1 AND resolved_at IS NULL`, tenantID)
	if err != nil {
		return err
	}
	type key struct{ signal, subject string }
	var stale []key
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.signal, &item.subject); err != nil {
			rows.Close()
			return err
		}
		if _, keep := open[item.signal+"\x00"+item.subject]; !keep {
			stale = append(stale, item)
		}
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return scanErr
	}
	for _, item := range stale {
		if _, err := tx.Exec(ctx, `
UPDATE ops_alerts
SET resolved_at = $4
WHERE tenant_id = $1 AND signal = $2 AND subject_key = $3 AND resolved_at IS NULL`, tenantID, item.signal, item.subject, now); err != nil {
			return err
		}
	}
	return nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
