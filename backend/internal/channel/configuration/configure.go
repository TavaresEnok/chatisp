package configuration

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
)

const tenantSetting = `NULLIF(current_setting('app.tenant_id', true), '')::uuid`

var (
	remoteID    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	displayE164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

// Configure stores one manual Meta inbox for the administrator's tenant.
// A second call with the same phone rotates that inbox. A new phone on the
// same app creates another inbox. The response never contains the secrets.
func (s *Service) Configure(ctx context.Context, sessionHash []byte, in Manual) (Box, error) {
	actor, err := s.authorize(ctx, sessionHash)
	if err != nil {
		return Box{}, err
	}
	in, err = normalize(in)
	if err != nil {
		return Box{}, err
	}
	var box Box
	err = s.store.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireAdmin(ctx, actor); err != nil {
			return err
		}
		var err error
		box, err = s.configure(ctx, tx, actor, in)
		return err
	})
	if err != nil {
		return Box{}, mapWriteErr(err)
	}
	s.note("canal configurado", box.ConnectorID, box.Version)
	return box, nil
}

func (s *Service) requireAdmin(ctx context.Context, actor actor) error {
	member, err := s.store.FindMembership(ctx, actor.MembershipID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrForbidden
		}
		return err
	}
	if member.UserID != actor.UserID || member.Role != "admin" || member.Status != "active" || member.TenantID != actor.TenantID {
		return ErrForbidden
	}
	return nil
}

func normalize(in Manual) (Manual, error) {
	in.InboxName = strings.TrimSpace(in.InboxName)
	in.DisplayNumber = strings.TrimSpace(in.DisplayNumber)
	in.AppID = strings.TrimSpace(in.AppID)
	in.WABAID = strings.TrimSpace(in.WABAID)
	in.PhoneNumberID = strings.TrimSpace(in.PhoneNumberID)
	in.AppSecret = strings.TrimSpace(in.AppSecret)
	in.AccessToken = strings.TrimSpace(in.AccessToken)
	if in.InboxName == "" || len([]rune(in.InboxName)) > 120 {
		return Manual{}, ErrInvalid
	}
	if in.DisplayNumber != "" && !displayE164.MatchString(in.DisplayNumber) {
		return Manual{}, ErrInvalid
	}
	if !remoteID.MatchString(in.AppID) || !remoteID.MatchString(in.WABAID) || !remoteID.MatchString(in.PhoneNumberID) {
		return Manual{}, ErrInvalid
	}
	if in.AppID == in.WABAID || in.AppID == in.PhoneNumberID || in.WABAID == in.PhoneNumberID {
		return Manual{}, ErrInvalid
	}
	if !secretText(in.AppSecret) || !secretText(in.AccessToken) {
		return Manual{}, ErrInvalid
	}
	return in, nil
}

func secretText(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

type phoneRow struct {
	ID          uuid.UUID
	WabaRowID   uuid.UUID
	ConnectorID uuid.UUID
	InboxID     uuid.UUID
	Version     int64
	Type        string
}

type appRow struct {
	ID uuid.UUID
}

type wabaRow struct {
	ID    uuid.UUID
	AppID uuid.UUID
}

func (s *Service) configure(ctx context.Context, tx pgx.Tx, actor actor, in Manual) (Box, error) {
	phone, err := lockPhone(ctx, tx, actor.TenantID, in.PhoneNumberID)
	if err != nil {
		return Box{}, err
	}
	app, err := findApp(ctx, tx, actor.TenantID, in.AppID)
	if err != nil {
		return Box{}, err
	}
	waba, err := findWaba(ctx, tx, actor.TenantID, in.WABAID)
	if err != nil {
		return Box{}, err
	}
	if phone.ID != uuid.Nil {
		return s.rotate(ctx, tx, actor, in, phone, app, waba)
	}
	return s.create(ctx, tx, actor, in, app, waba)
}

func (s *Service) rotate(ctx context.Context, tx pgx.Tx, actor actor, in Manual, phone phoneRow, app appRow, waba wabaRow) (Box, error) {
	if phone.Type != connectorMeta || app.ID == uuid.Nil || waba.ID == uuid.Nil || waba.ID != phone.WabaRowID || waba.AppID != app.ID {
		return Box{}, ErrInvalid
	}
	changed, appSecretVersion, err := s.writeAppSecret(ctx, tx, actor.TenantID, app.ID, in.AppSecret)
	if err != nil {
		return Box{}, err
	}
	if err := s.writeToken(ctx, tx, actor.TenantID, phone.ConnectorID, in.AccessToken); err != nil {
		return Box{}, err
	}
	if err := updateInbox(ctx, tx, actor.TenantID, phone.InboxID, in.InboxName, in.DisplayNumber); err != nil {
		return Box{}, err
	}
	if err := updatePhoneDisplay(ctx, tx, actor.TenantID, phone.ID, in.DisplayNumber); err != nil {
		return Box{}, err
	}
	version, err := bumpConnector(ctx, tx, actor.TenantID, phone.ConnectorID, true)
	if err != nil {
		return Box{}, err
	}
	if changed {
		if err := bumpAppConnectors(ctx, tx, actor.TenantID, app.ID, phone.ConnectorID); err != nil {
			return Box{}, err
		}
	}
	if err := s.audit(ctx, actor, actionConfigured, phone.ConnectorID, "success"); err != nil {
		return Box{}, err
	}
	return Box{
		ConnectorID: phone.ConnectorID, InboxID: phone.InboxID, Name: in.InboxName,
		AppID: in.AppID, WABAID: in.WABAID, PhoneNumberID: in.PhoneNumberID,
		DisplayNumber: in.DisplayNumber, Status: "connected", Enabled: true, Version: version,
		TokenKeyVersion: s.ring.Current(), AppSecretKeyVersion: appSecretVersion,
	}, nil
}

func (s *Service) create(ctx context.Context, tx pgx.Tx, actor actor, in Manual, app appRow, waba wabaRow) (Box, error) {
	if waba.ID != uuid.Nil && (app.ID == uuid.Nil || waba.AppID != app.ID) {
		return Box{}, ErrInvalid
	}
	connectorID, inboxID := uuid.New(), uuid.New()
	appSecretVersion := ""
	if err := insertConnector(ctx, tx, actor.TenantID, connectorID, in.PhoneNumberID); err != nil {
		return Box{}, err
	}
	if err := insertInbox(ctx, tx, actor.TenantID, inboxID, connectorID, in.InboxName, in.DisplayNumber); err != nil {
		return Box{}, err
	}
	if app.ID == uuid.Nil {
		app.ID = uuid.New()
		waba.ID = uuid.New()
		phoneID := uuid.New()
		secret, err := s.seal(actor.TenantID, app.ID, entityApp, fieldAppSecret, in.AppSecret)
		if err != nil {
			return Box{}, err
		}
		appSecretVersion = secret.KeyVersion
		var display string
		if in.DisplayNumber != "" {
			display = in.DisplayNumber
		}
		if err := (store.Repo{}).RegisterMeta(ctx, tx, store.MetaBinding{
			TenantID: actor.TenantID, AppRowID: app.ID, AppID: in.AppID, Secret: secret,
			WabaRowID: waba.ID, WabaID: in.WABAID, PhoneRowID: phoneID, PhoneNumberID: in.PhoneNumberID,
			DisplayNumber: display, ConnectorID: connectorID, InboxID: inboxID,
		}); err != nil {
			return Box{}, err
		}
	} else {
		changed, version, err := s.writeAppSecret(ctx, tx, actor.TenantID, app.ID, in.AppSecret)
		if err != nil {
			return Box{}, err
		}
		appSecretVersion = version
		if waba.ID == uuid.Nil {
			waba.ID = uuid.New()
			if err := insertWaba(ctx, tx, actor.TenantID, waba.ID, app.ID, in.WABAID); err != nil {
				return Box{}, err
			}
		}
		if err := insertPhone(ctx, tx, actor.TenantID, uuid.New(), waba.ID, connectorID, inboxID, in); err != nil {
			return Box{}, err
		}
		if changed {
			if err := bumpAppConnectors(ctx, tx, actor.TenantID, app.ID, connectorID); err != nil {
				return Box{}, err
			}
		}
	}
	if err := s.writeToken(ctx, tx, actor.TenantID, connectorID, in.AccessToken); err != nil {
		return Box{}, err
	}
	if err := s.audit(ctx, actor, actionConfigured, connectorID, "success"); err != nil {
		return Box{}, err
	}
	version, err := readVersion(ctx, tx, actor.TenantID, connectorID)
	if err != nil {
		return Box{}, err
	}
	return Box{
		ConnectorID: connectorID, InboxID: inboxID, Name: in.InboxName,
		AppID: in.AppID, WABAID: in.WABAID, PhoneNumberID: in.PhoneNumberID,
		DisplayNumber: in.DisplayNumber, Status: "connected", Enabled: true, Version: version,
		TokenKeyVersion: s.ring.Current(), AppSecretKeyVersion: appSecretVersion,
	}, nil
}

// writeAppSecret stores a new App Secret under the current ring key.
// The same plaintext keeps the existing ciphertext. The returned version is
// the one left in meta_app_secrets, which can be older than ring.Current.
func (s *Service) writeAppSecret(ctx context.Context, tx pgx.Tx, tenantID, appID uuid.UUID, plaintext string) (bool, string, error) {
	var current store.Secret
	err := tx.QueryRow(ctx, `
		SELECT key_version, ciphertext, aad
		FROM meta_app_secrets
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND meta_app_id = $2
	`, tenantID, appID).Scan(&current.KeyVersion, &current.Ciphertext, &current.AAD)
	if err == nil {
		opened, openErr := s.open(tenantID, appID, entityApp, fieldAppSecret, current)
		if openErr == nil && sameSecret(opened, plaintext) {
			return false, current.KeyVersion, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	sealed, err := s.seal(tenantID, appID, entityApp, fieldAppSecret, plaintext)
	if err != nil {
		return false, "", err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE meta_app_secrets
		SET key_version = $3, ciphertext = $4, aad = $5
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND meta_app_id = $2
	`, tenantID, appID, sealed.KeyVersion, sealed.Ciphertext, sealed.AAD)
	if err != nil {
		return false, "", err
	}
	if tag.RowsAffected() != 1 {
		return false, "", ErrNotFound
	}
	return true, sealed.KeyVersion, nil
}

func (s *Service) writeToken(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, plaintext string) error {
	sealed, err := s.seal(tenantID, connectorID, entityConnector, fieldToken, plaintext)
	if err != nil {
		return err
	}
	_, err = (store.Repo{}).GetCredential(ctx, tx, tenantID, connectorID, fieldToken)
	if errors.Is(err, store.ErrNotFound) {
		_, err = (store.Repo{}).PutCredential(ctx, tx, tenantID, connectorID, fieldToken, sealed)
		return err
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE credential_secrets
		SET key_version = $4, ciphertext = $5, aad = $6
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND entity = 'channel_connector'
		  AND entity_id = $2
		  AND field = $3
	`, tenantID, connectorID, fieldToken, sealed.KeyVersion, sealed.Ciphertext, sealed.AAD)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) audit(ctx context.Context, actor actor, action string, connectorID uuid.UUID, result string) error {
	_, err := s.store.CreateAuditLog(ctx, db.NewAuditLog{
		MembershipID: &actor.MembershipID,
		ActorUserID:  &actor.UserID,
		Action:       action,
		TargetType:   "channel_connector",
		TargetID:     &connectorID,
		Result:       result,
	})
	return err
}

func lockPhone(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, phoneNumberID string) (phoneRow, error) {
	var row phoneRow
	var version string
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.meta_waba_id, p.connector_id, p.inbox_id, c.version, c.connector_type
		FROM meta_phone_numbers AS p
		JOIN channel_connectors AS c
		  ON c.tenant_id = p.tenant_id AND c.id = p.connector_id
		WHERE p.tenant_id = $1
		  AND p.tenant_id = `+tenantSetting+`
		  AND p.phone_number_id = $2
		FOR UPDATE OF p, c
	`, tenantID, phoneNumberID).Scan(&row.ID, &row.WabaRowID, &row.ConnectorID, &row.InboxID, &version, &row.Type)
	if errors.Is(err, pgx.ErrNoRows) {
		return phoneRow{}, nil
	}
	if err != nil {
		return phoneRow{}, err
	}
	parsed, err := strconv.ParseInt(version, 10, 64)
	if err != nil || parsed < 1 {
		return phoneRow{}, ErrInvalid
	}
	row.Version = parsed
	return row, nil
}

func findApp(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, appID string) (appRow, error) {
	var row appRow
	err := tx.QueryRow(ctx, `
		SELECT id FROM meta_apps
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND app_id = $2
	`, tenantID, appID).Scan(&row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return appRow{}, nil
	}
	return row, err
}

func findWaba(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, wabaID string) (wabaRow, error) {
	var row wabaRow
	err := tx.QueryRow(ctx, `
		SELECT id, meta_app_id FROM meta_wabas
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND waba_id = $2
	`, tenantID, wabaID).Scan(&row.ID, &row.AppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return wabaRow{}, nil
	}
	return row, err
}

func insertConnector(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, phoneNumberID string) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO channel_connectors (
			id, tenant_id, connector_type, remote_origin, version, status, enabled, credential_ref, channel
		)
		SELECT $2, t.id, 'meta_cloud', $3, '1', 'connected', true, $4, 'whatsapp'
		FROM tenants AS t
		WHERE t.id = $1
		  AND t.id = `+tenantSetting+`
		RETURNING id
	`, tenantID, connectorID, "meta:"+phoneNumberID, "cred:"+connectorID.String()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func insertInbox(ctx context.Context, tx pgx.Tx, tenantID, inboxID, connectorID uuid.UUID, name, display string) error {
	var presented *string
	if display != "" {
		presented = &display
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO inboxes (id, tenant_id, connector_id, name, presented_number)
		SELECT $2, $1, $3, $4, $5
		WHERE $1 = `+tenantSetting+`
		RETURNING id
	`, tenantID, inboxID, connectorID, name, presented).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func insertWaba(ctx context.Context, tx pgx.Tx, tenantID, wabaID, appID uuid.UUID, remote string) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO meta_wabas (tenant_id, id, meta_app_id, waba_id, status)
		SELECT $1, $2, $3, $4, 'active'
		WHERE $1 = `+tenantSetting+`
		RETURNING id
	`, tenantID, wabaID, appID, remote).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func insertPhone(ctx context.Context, tx pgx.Tx, tenantID, phoneID, wabaID, connectorID, inboxID uuid.UUID, in Manual) error {
	var display *string
	if in.DisplayNumber != "" {
		display = &in.DisplayNumber
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO meta_phone_numbers (
			tenant_id, id, meta_waba_id, phone_number_id, display_number, status, connector_id, inbox_id
		)
		SELECT $1, $2, $3, $4, $5, 'active', $6, $7
		WHERE $1 = `+tenantSetting+`
		RETURNING id
	`, tenantID, phoneID, wabaID, in.PhoneNumberID, display, connectorID, inboxID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func updateInbox(ctx context.Context, tx pgx.Tx, tenantID, inboxID uuid.UUID, name, display string) error {
	var presented *string
	if display != "" {
		presented = &display
	}
	tag, err := tx.Exec(ctx, `
		UPDATE inboxes
		SET name = $3, presented_number = $4, updated_at = now()
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND id = $2
	`, tenantID, inboxID, name, presented)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func updatePhoneDisplay(ctx context.Context, tx pgx.Tx, tenantID, phoneID uuid.UUID, display string) error {
	var presented *string
	if display != "" {
		presented = &display
	}
	tag, err := tx.Exec(ctx, `
		UPDATE meta_phone_numbers
		SET display_number = $3
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND id = $2
	`, tenantID, phoneID, presented)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func bumpConnector(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, reconnect bool) (int64, error) {
	status := ""
	if reconnect {
		status = "connected"
	}
	var version string
	err := tx.QueryRow(ctx, `
		UPDATE channel_connectors
		SET version = (version::bigint + 1)::text,
		    updated_at = now(),
		    status = CASE WHEN $3 = '' THEN status ELSE $3 END,
		    enabled = CASE WHEN $3 = '' THEN enabled ELSE true END
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND id = $2
		  AND connector_type = 'meta_cloud'
		RETURNING version
	`, tenantID, connectorID, status).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(version, 10, 64)
	if err != nil || parsed < 1 {
		return 0, ErrInvalid
	}
	return parsed, nil
}

func bumpAppConnectors(ctx context.Context, tx pgx.Tx, tenantID, appID, except uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE channel_connectors AS c
		SET version = (c.version::bigint + 1)::text,
		    updated_at = now()
		WHERE c.tenant_id = $1
		  AND c.tenant_id = `+tenantSetting+`
		  AND c.connector_type = 'meta_cloud'
		  AND c.id <> $3
		  AND c.id IN (
			SELECT p.connector_id
			FROM meta_phone_numbers AS p
			JOIN meta_wabas AS w
			  ON w.tenant_id = p.tenant_id AND w.id = p.meta_waba_id
			WHERE p.tenant_id = $1
			  AND w.meta_app_id = $2
		  )
	`, tenantID, appID, except)
	return err
}

func readVersion(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID) (int64, error) {
	var version string
	err := tx.QueryRow(ctx, `
		SELECT version
		FROM channel_connectors
		WHERE tenant_id = $1
		  AND tenant_id = `+tenantSetting+`
		  AND id = $2
		  AND connector_type = 'meta_cloud'
	`, tenantID, connectorID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(version, 10, 64)
	if err != nil || parsed < 1 {
		return 0, ErrInvalid
	}
	return parsed, nil
}
