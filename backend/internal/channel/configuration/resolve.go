package configuration

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
)

// ResolveRoute reads the app–WABA–number binding as ingress.
// The tenant is the one stored on the binding. The caller does not assign it.
func (s *Server) ResolveRoute(ctx context.Context, appID, phoneNumberID string) (store.Route, error) {
	if s == nil || s.svc == nil {
		return store.Route{}, ErrUnavailable
	}
	var route store.Route
	err := db.WithIngressTx(ctx, s.svc.ingress, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		route, err = (store.Repo{}).ResolvePhone(ctx, tx, appID, phoneNumberID)
		return err
	})
	if err != nil {
		return store.Route{}, mapWriteErr(err)
	}
	if route.TenantID == uuid.Nil || route.ConnectorID == uuid.Nil || route.InboxID == uuid.Nil {
		return store.Route{}, ErrNotFound
	}
	return route, nil
}

// ResolveMaterial opens the token and the app secret for one connector.
// version is the configuration generation captured when the job was created.
// A later save or an invalid credential makes that generation stale.
// The value is not safe to return on a panel response.
func (s *Server) ResolveMaterial(ctx context.Context, tenantID, connectorID uuid.UUID, version int64) (Material, error) {
	if s == nil || s.svc == nil || tenantID == uuid.Nil || connectorID == uuid.Nil || version < 1 {
		return Material{}, ErrInvalid
	}
	var material Material
	err := s.svc.store.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		material, err = s.svc.loadMaterial(ctx, tx, tenantID, connectorID, version)
		return err
	})
	if err != nil {
		return Material{}, mapWriteErr(err)
	}
	return material, nil
}

// ResolveApp opens the App Secret bound to one trusted endpoint.
// appID is that endpoint's configured identifier. It is not taken from an
// unauthenticated payload, and the call does not read the send token or the
// connector status. A channel degraded by an invalid token still authenticates.
// The plaintext stays in process for the signature check.
func (s *Server) ResolveApp(ctx context.Context, appID string) (TrustedApp, error) {
	if s == nil || s.svc == nil {
		return TrustedApp{}, ErrUnavailable
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return TrustedApp{}, ErrInvalid
	}
	var trusted TrustedApp
	err := db.WithIngressTx(ctx, s.svc.ingress, func(ctx context.Context, tx pgx.Tx) error {
		var tenantID, appRow uuid.UUID
		var storedAppID string
		err := tx.QueryRow(ctx, `
			SELECT tenant_id, id, app_id
			FROM meta_apps
			WHERE app_id = $1
			  AND status = 'active'
		`, appID).Scan(&tenantID, &appRow, &storedAppID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if tenantID == uuid.Nil || appRow == uuid.Nil || storedAppID == "" {
			return ErrNotFound
		}
		sealed, err := (store.Repo{}).ReadAppSecret(ctx, tx, storedAppID)
		if err != nil {
			return err
		}
		secret, err := s.svc.open(tenantID, appRow, entityApp, fieldAppSecret, sealed)
		if err != nil {
			return err
		}
		trusted = TrustedApp{AppID: storedAppID, AppSecret: secret}
		return nil
	})
	if err != nil {
		return TrustedApp{}, mapWriteErr(err)
	}
	return trusted, nil
}

// ReportInvalidCredential marks the connector degraded when version is still
// the generation the worker used. A later save makes that report stale: status,
// version and alerts stay as they are. Repeating the same version does not
// open a second alert or bump the version again. The worker does not send the token.
func (s *Server) ReportInvalidCredential(ctx context.Context, tenantID, connectorID uuid.UUID, version int64) error {
	if s == nil || s.svc == nil || tenantID == uuid.Nil || connectorID == uuid.Nil || version < 1 {
		return ErrInvalid
	}
	err := s.svc.store.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, connectorType, versionText string
		err := tx.QueryRow(ctx, `
			SELECT status, connector_type, version
			FROM channel_connectors
			WHERE tenant_id = $1
			  AND tenant_id = `+tenantSetting+`
			  AND id = $2
			FOR UPDATE
		`, tenantID, connectorID).Scan(&status, &connectorType, &versionText)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if connectorType != connectorMeta || status == "disconnected" {
			return ErrNotFound
		}
		current, err := parseVersion(versionText)
		if err != nil {
			return err
		}
		idempotent := status == "degraded" && (current == version || (version < math.MaxInt64 && current == version+1))
		if current == version && status != "degraded" {
			tag, err := tx.Exec(ctx, `
				UPDATE channel_connectors
				SET status = 'degraded',
				    version = (version::bigint + 1)::text,
				    updated_at = now()
				WHERE tenant_id = $1
				  AND tenant_id = `+tenantSetting+`
				  AND id = $2
				  AND connector_type = 'meta_cloud'
				  AND status <> 'disconnected'
				  AND status <> 'degraded'
				  AND version = $3
			`, tenantID, connectorID, strconv.FormatInt(version, 10))
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrStaleVersion
			}
		} else if !idempotent {
			return ErrStaleVersion
		}
		return s.svc.openAlert(ctx, tx, tenantID, connectorID)
	})
	if err != nil {
		return mapWriteErr(err)
	}
	s.svc.note("canal degradado", connectorID, version)
	return nil
}

func (s *Service) loadMaterial(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, version int64) (Material, error) {
	var (
		status, connectorType, versionText string
		enabled                            bool
		appRow, wabaRow                    uuid.UUID
		appID, wabaID, phoneID             string
		token                              store.Secret
		appSecret                          store.Secret
	)
	err := tx.QueryRow(ctx, `
		SELECT c.status, c.enabled, c.connector_type, c.version,
		       a.id, a.app_id, w.id, w.waba_id, p.phone_number_id,
		       cred.key_version, cred.ciphertext, cred.aad,
		       sec.key_version, sec.ciphertext, sec.aad
		FROM channel_connectors AS c
		JOIN meta_phone_numbers AS p
		  ON p.tenant_id = c.tenant_id AND p.connector_id = c.id AND p.status = 'active'
		JOIN meta_wabas AS w
		  ON w.tenant_id = p.tenant_id AND w.id = p.meta_waba_id AND w.status = 'active'
		JOIN meta_apps AS a
		  ON a.tenant_id = w.tenant_id AND a.id = w.meta_app_id AND a.status = 'active'
		JOIN credential_secrets AS cred
		  ON cred.tenant_id = c.tenant_id
		 AND cred.entity = 'channel_connector'
		 AND cred.entity_id = c.id
		 AND cred.field = 'access_token'
		JOIN meta_app_secrets AS sec
		  ON sec.tenant_id = a.tenant_id AND sec.meta_app_id = a.id
		WHERE c.tenant_id = $1
		  AND c.tenant_id = `+tenantSetting+`
		  AND c.id = $2
	`, tenantID, connectorID).Scan(
		&status, &enabled, &connectorType, &versionText,
		&appRow, &appID, &wabaRow, &wabaID, &phoneID,
		&token.KeyVersion, &token.Ciphertext, &token.AAD,
		&appSecret.KeyVersion, &appSecret.Ciphertext, &appSecret.AAD,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, err
	}
	if connectorType != connectorMeta || !enabled {
		return Material{}, ErrInvalid
	}
	current, err := parseVersion(versionText)
	if err != nil {
		return Material{}, err
	}
	if current != version {
		return Material{}, ErrStaleVersion
	}
	if status != "connected" {
		return Material{}, ErrDegraded
	}
	access, err := s.open(tenantID, connectorID, entityConnector, fieldToken, token)
	if err != nil {
		return Material{}, err
	}
	secret, err := s.open(tenantID, appRow, entityApp, fieldAppSecret, appSecret)
	if err != nil {
		return Material{}, err
	}
	return Material{
		Version: current, AppID: appID, WABAID: wabaID, PhoneNumberID: phoneID,
		AccessToken: access, AppSecret: secret,
	}, nil
}

func (s *Service) openAlert(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID) error {
	var existing uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM audit_logs
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND action = $2
		  AND target_id = $3
		  AND result = 'open'
	`, tenantID, actionCredentialInvalid, connectorID).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = s.store.CreateAuditLog(ctx, db.NewAuditLog{
		Action:     actionCredentialInvalid,
		TargetType: "channel_connector",
		TargetID:   &connectorID,
		Result:     "open",
	})
	return err
}
