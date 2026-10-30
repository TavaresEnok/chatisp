package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/db/dbgen"
)

func (s Service) authorize(ctx context.Context, tx pgx.Tx, p Principal, inboxID uuid.UUID, sending bool) error {
	if err := s.authorizePrincipal(ctx, tx, p); err != nil {
		return err
	}
	if inboxID == uuid.Nil {
		return ErrInboxForbidden
	}
	var status, kind string
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT c.status, c.enabled, c.connector_type
		FROM inboxes AS i
		JOIN channel_connectors AS c
		  ON c.tenant_id = i.tenant_id AND c.id = i.connector_id
		WHERE i.tenant_id = $1 AND i.id = $2
	`, p.TenantID, inboxID).Scan(&status, &enabled, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInboxForbidden
	}
	if err != nil {
		return err
	}
	if sending && (!enabled || status != "connected" || kind != "meta_cloud") {
		return ErrChannelUnavailable
	}
	return nil
}

func (s Service) authorizePrincipal(ctx context.Context, tx pgx.Tx, p Principal) error {
	if s.DB == nil || tx == nil || p.IsSuperadmin || p.TenantID == uuid.Nil || p.UserID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	membership, err := s.DB.FindMembership(ctx, p.MembershipID)
	if errors.Is(err, db.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if membership.UserID != p.UserID || membership.Status != "active" {
		return ErrForbidden
	}
	switch membership.Role {
	case "admin", "supervisor", "agent":
	default:
		return ErrForbidden
	}
	tenant, err := s.DB.GetTenant(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return ErrTenantInactive
	}
	if err != nil {
		return err
	}
	if tenant.Status != "active" {
		return ErrTenantInactive
	}
	return nil
}

// authorAllowed re-reads the persisted human author under the conversation lock.
// Bot and menu attempts have no operational membership; epoch fencing covers them.
// A human attempt is dispatchable only while that membership is active and its
// role is still admin, supervisor or agent. The membership row is locked so a
// revocation cannot commit between this check and the sending transition.
func (s Service) authorAllowed(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID) (bool, error) {
	if _, err := queries(tx).LockMessage(ctx, dbgen.LockMessageParams{TenantID: tenantID, ID: messageID}); errors.Is(err, pgx.ErrNoRows) {
		return false, store.ErrNotFound
	} else if err != nil {
		return false, err
	}
	var author *uuid.UUID
	var origin string
	err := tx.QueryRow(ctx, `
		SELECT author_membership_id, origin
		FROM messages
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
	`, tenantID, messageID).Scan(&author, &origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, store.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if origin != store.ControllerHuman {
		return true, nil
	}
	if author == nil || *author == uuid.Nil {
		return false, nil
	}
	var role, status string
	err = tx.QueryRow(ctx, `
		SELECT role, status
		FROM memberships
		WHERE tenant_id = $1
		  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		  AND id = $2
		FOR UPDATE
	`, tenantID, *author).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != "active" {
		return false, nil
	}
	switch role {
	case "admin", "supervisor", "agent":
		return true, nil
	default:
		return false, nil
	}
}

func (s Service) tenantActive(ctx context.Context) error {
	tenant, err := s.DB.GetTenant(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return ErrTenantInactive
	}
	if err != nil {
		return err
	}
	if tenant.Status != "active" {
		return ErrTenantInactive
	}
	return nil
}
