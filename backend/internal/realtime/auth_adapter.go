package realtime

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

// AuthAdapter bridges platform auth.Service and db.Store to realtime interfaces.
type AuthAdapter struct {
	auth *auth.Service
	db   *db.Store
}

// NewAuthAdapter creates a new Authenticator and InboxResolver adapter.
func NewAuthAdapter(authSvc *auth.Service, database *db.Store) *AuthAdapter {
	return &AuthAdapter{
		auth: authSvc,
		db:   database,
	}
}

// DB returns the underlying platform database store.
func (a *AuthAdapter) DB() *db.Store {
	if a == nil {
		return nil
	}
	return a.db
}

// AuthenticateSession validates the session cookie token against database state.
func (a *AuthAdapter) AuthenticateSession(ctx context.Context, sessionToken string) (Principal, error) {
	if a == nil || a.auth == nil {
		return Principal{}, ErrUnauthorized
	}
	p, err := a.auth.Authenticate(ctx, sessionToken, "", false)
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			return Principal{}, ErrForbidden
		}
		return Principal{}, ErrUnauthorized
	}
	if p.EnrollmentOnly {
		return Principal{}, ErrForbidden
	}
	switch p.Role {
	case "admin", "supervisor", "agent":
	default:
		return Principal{}, ErrForbidden
	}
	return Principal{
		SessionID:    p.SessionID,
		UserID:       p.UserID,
		MembershipID: p.MembershipID,
		TenantID:     p.TenantID,
		Role:         p.Role,
		IsSuperadmin: p.IsSuperadmin,
	}, nil
}

// RevalidatePrincipal re-checks the session, membership and tenant status.
func (a *AuthAdapter) RevalidatePrincipal(ctx context.Context, p Principal, sessionToken string) error {
	cur, err := a.AuthenticateSession(ctx, sessionToken)
	if err != nil {
		return err
	}
	if cur.TenantID != p.TenantID || cur.MembershipID != p.MembershipID || cur.UserID != p.UserID {
		return ErrForbidden
	}
	return nil
}

// ResolveConversationInbox looks up the inbox_id for a given conversation.
func (a *AuthAdapter) ResolveConversationInbox(ctx context.Context, tenantID, conversationID uuid.UUID) (uuid.UUID, error) {
	if a == nil || a.db == nil {
		return uuid.Nil, ErrInboxForbidden
	}
	var inboxID *uuid.UUID
	err := a.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT inbox_id
			FROM conversations
			WHERE tenant_id = $1 AND id = $2
		`, tenantID, conversationID).Scan(&inboxID)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrInboxForbidden
		}
		return uuid.Nil, err
	}
	if inboxID == nil || *inboxID == uuid.Nil {
		return uuid.Nil, ErrInboxForbidden
	}
	return *inboxID, nil
}
