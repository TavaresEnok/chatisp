package configuration

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Status lists the tenant's Meta inboxes and open credential alerts.
func (s *Service) Status(ctx context.Context, sessionHash []byte) (Status, error) {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return Status{}, err
	}
	var out Status
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		var err error
		out.Boxes, err = listBoxes(ctx, tx, actor.TenantID)
		if err != nil {
			return err
		}
		out.Alerts, err = listAlerts(ctx, tx, actor.TenantID)
		return err
	})
	if err != nil {
		return Status{}, mapWriteErr(err)
	}
	return out, nil
}

// AcknowledgeAlert closes one open credential alert of this tenant.
func (s *Service) AcknowledgeAlert(ctx context.Context, sessionHash []byte, alertID uuid.UUID) error {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return err
	}
	if alertID == uuid.Nil {
		return ErrInvalid
	}
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE audit_logs
			SET result = 'acknowledged'
			WHERE tenant_id = $1
			  AND tenant_id = `+tenantSetting+`
			  AND id = $2
			  AND action = $3
			  AND result = 'open'
		`, actor.TenantID, alertID, actionCredentialInvalid)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
	return mapWriteErr(err)
}

// Inspect returns quarantine identifiers, quarantined item keys and discarded
// jobs of this tenant. Global quarantines lack reliable tenant attribution and stay server-only.
func (s *Service) Inspect(ctx context.Context, sessionHash []byte) (Inspection, error) {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return Inspection{}, err
	}
	var out Inspection
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		var err error
		out.Items, err = listQuarantineItems(ctx, tx, actor.TenantID)
		return err
	})
	if err != nil {
		return Inspection{}, mapWriteErr(err)
	}
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out.Discarded, err = listDiscarded(ctx, tx, actor.TenantID)
		return err
	})
	if err != nil {
		return Inspection{}, mapWriteErr(err)
	}
	return out, nil
}

func listBoxes(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]Box, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.id, i.id, i.name, a.app_id, w.waba_id, p.phone_number_id,
		       COALESCE(p.display_number, ''), c.status, c.enabled, c.version,
		       COALESCE(cred.key_version, ''), meta_app_secret_version(a.id)
		FROM channel_connectors AS c
		JOIN inboxes AS i
		  ON i.tenant_id = c.tenant_id AND i.connector_id = c.id
		JOIN meta_phone_numbers AS p
		  ON p.tenant_id = c.tenant_id AND p.connector_id = c.id
		JOIN meta_wabas AS w
		  ON w.tenant_id = p.tenant_id AND w.id = p.meta_waba_id
		JOIN meta_apps AS a
		  ON a.tenant_id = w.tenant_id AND a.id = w.meta_app_id
		LEFT JOIN credential_secrets AS cred
		  ON cred.tenant_id = c.tenant_id
		 AND cred.entity = 'channel_connector'
		 AND cred.entity_id = c.id
		 AND cred.field = 'access_token'
		WHERE c.tenant_id = $1
		  AND c.tenant_id = `+tenantSetting+`
		  AND c.connector_type = 'meta_cloud'
		ORDER BY p.phone_number_id
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Box{}
	for rows.Next() {
		var box Box
		var version string
		if err := rows.Scan(
			&box.ConnectorID, &box.InboxID, &box.Name, &box.AppID, &box.WABAID, &box.PhoneNumberID,
			&box.DisplayNumber, &box.Status, &box.Enabled, &version,
			&box.TokenKeyVersion, &box.AppSecretKeyVersion,
		); err != nil {
			return nil, err
		}
		parsed, err := parseVersion(version)
		if err != nil {
			return nil, err
		}
		box.Version = parsed
		out = append(out, box)
	}
	return out, rows.Err()
}

func listAlerts(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]Alert, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, target_id, action, result, created_at::text
		FROM audit_logs
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND action = $2
		  AND result = 'open'
		  AND target_id IS NOT NULL
		ORDER BY created_at, id
	`, tenantID, actionCredentialInvalid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var alert Alert
		if err := rows.Scan(&alert.ID, &alert.ConnectorID, &alert.Action, &alert.Result, &alert.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, alert)
	}
	return out, rows.Err()
}

func listQuarantineItems(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]ItemRef, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, inbox_id, external_key, kind, state
		FROM webhook_items
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND state = 'quarantined'
		ORDER BY created_at, id
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemRef{}
	for rows.Next() {
		var item ItemRef
		if err := rows.Scan(&item.ID, &item.InboxID, &item.ExternalKey, &item.Kind, &item.State); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func listDiscarded(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]DiscardedJob, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, kind, queue, state::text, attempt
		FROM river.river_job
		WHERE state = 'discarded'
		  AND args->>'tenant_id' = $1
		ORDER BY id
		LIMIT 50
	`, tenantID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DiscardedJob{}
	for rows.Next() {
		var job DiscardedJob
		if err := rows.Scan(&job.ID, &job.Kind, &job.Queue, &job.State, &job.Attempt); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func parseVersion(version string) (int64, error) {
	parsed, err := strconv.ParseInt(version, 10, 64)
	if err != nil || parsed < 1 {
		return 0, ErrInvalid
	}
	return parsed, nil
}
