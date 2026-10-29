package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// LoginMembership is one tenant link returned by the restricted auth path.
type LoginMembership struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	MembershipID uuid.UUID `json:"membership_id"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	TenantStatus string    `json:"tenant_status"`
}

// LoginIdentity is the credential row used only inside WithAuth.
// PasswordHash is empty when the user has no password yet.
type LoginIdentity struct {
	UserID       uuid.UUID
	Email        string
	Status       string
	PasswordHash string
	Memberships  []LoginMembership
}

// SessionRecord is a session loaded by its secret hash. The hash itself is not returned.
type SessionRecord struct {
	Status            string
	ID                uuid.UUID
	UserID            uuid.UUID
	TenantID          uuid.UUID
	MembershipID      uuid.UUID
	Role              string
	UserStatus        string
	MembershipStatus  string
	TenantStatus      string
	Email             string
	CSRFHash          []byte
	AbsoluteExpiresAt time.Time
	IdleExpiresAt     time.Time
}

// NewSession is the hashed material of a new panel session.
type NewSession struct {
	ID                uuid.UUID
	TokenHash         []byte
	UserID            uuid.UUID
	TenantID          uuid.UUID
	MembershipID      uuid.UUID
	CSRFHash          []byte
	Now               time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

// NewAuthToken is a one-time invite or password reset. The raw token is not stored.
type NewAuthToken struct {
	ID           uuid.UUID
	TokenHash    []byte
	Purpose      string
	UserID       uuid.UUID
	TenantID     uuid.UUID
	MembershipID uuid.UUID
	ExpiresAt    time.Time
	Now          time.Time
}

// TokenResult is the outcome of consuming an invite or reset token.
type TokenResult struct {
	Status       string
	UserID       uuid.UUID
	TenantID     uuid.UUID
	MembershipID uuid.UUID
}

// Attempt is one bump of a login or reset counter.
type Attempt struct {
	Failures      int
	LockedUntil   time.Time
	AlreadyLocked bool
}

// ListedSession is safe to show to an administrator. It has no secret.
type ListedSession struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	CreatedAt         time.Time
	LastSeenAt        time.Time
	AbsoluteExpiresAt time.Time
}

// FindUserByEmail reads a global user through the platform role.
// It does not return a password hash.
func (s *Store) FindUserByEmail(ctx context.Context, email string) (User, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return User{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	var user User
	err = scope.tx.QueryRow(ctx, `
		SELECT id, email, status, created_at
		FROM users
		WHERE lower(email) = $1
	`, normalized).Scan(&user.ID, &user.Email, &user.Status, &user.CreatedAt)
	if err != nil {
		return User{}, mapAuthErr(err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

// FindUserByID reads a global user through the platform role.
func (s *Store) FindUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return User{}, err
	}
	if id == uuid.Nil {
		return User{}, ErrNotFound
	}
	var user User
	err = scope.tx.QueryRow(ctx, `
		SELECT id, email, status, created_at
		FROM users
		WHERE id = $1
	`, id).Scan(&user.ID, &user.Email, &user.Status, &user.CreatedAt)
	if err != nil {
		return User{}, mapAuthErr(err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

// FindMembershipByUser reads one membership inside the active tenant.
func (s *Store) FindMembershipByUser(ctx context.Context, userID uuid.UUID) (Membership, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return Membership{}, err
	}
	if userID == uuid.Nil {
		return Membership{}, ErrNotFound
	}
	var row Membership
	err = scope.tx.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, role, status, created_at
		FROM memberships
		WHERE tenant_id = $1 AND user_id = $2
	`, scope.id, userID).Scan(&row.ID, &row.TenantID, &row.UserID, &row.Role, &row.Status, &row.CreatedAt)
	if err != nil {
		return Membership{}, mapAuthErr(err)
	}
	row.CreatedAt = row.CreatedAt.UTC()
	return row, nil
}

// LoginIdentity loads a user, credential hash and memberships. It requires WithAuth.
func (s *Store) LoginIdentity(ctx context.Context, email string) (LoginIdentity, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return LoginIdentity{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return LoginIdentity{}, err
	}
	var identity LoginIdentity
	var raw []byte
	err = scope.tx.QueryRow(ctx, `
		SELECT user_id, email, status, password_hash, memberships
		FROM auth_login_identity($1)
	`, normalized).Scan(&identity.UserID, &identity.Email, &identity.Status, &identity.PasswordHash, &raw)
	if err != nil {
		return LoginIdentity{}, mapAuthErr(err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &identity.Memberships); err != nil {
			return LoginIdentity{}, errors.New("falha de autenticação")
		}
	}
	return identity, nil
}

// InsertSession stores a hashed session. It requires WithAuth.
func (s *Store) InsertSession(ctx context.Context, in NewSession) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `
		SELECT auth_insert_session($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, in.ID, in.TokenHash, in.UserID, in.TenantID, in.MembershipID, in.CSRFHash, in.Now, in.IdleExpiresAt, in.AbsoluteExpiresAt)
	return mapAuthErr(err)
}

// LoadSession reads a session by the hash of its cookie value. It requires WithAuth.
func (s *Store) LoadSession(ctx context.Context, tokenHash []byte, now time.Time) (SessionRecord, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return SessionRecord{}, err
	}
	var row SessionRecord
	err = scope.tx.QueryRow(ctx, `
		SELECT session_status, session_id, user_id, tenant_id, membership_id,
		       role, user_status, membership_status, tenant_status, email,
		       csrf_hash, absolute_expires_at, idle_expires_at
		FROM auth_load_session($1, $2)
	`, tokenHash, now).Scan(
		&row.Status, &row.ID, &row.UserID, &row.TenantID, &row.MembershipID,
		&row.Role, &row.UserStatus, &row.MembershipStatus, &row.TenantStatus, &row.Email,
		&row.CSRFHash, &row.AbsoluteExpiresAt, &row.IdleExpiresAt,
	)
	if err != nil {
		return SessionRecord{}, mapAuthErr(err)
	}
	row.AbsoluteExpiresAt = row.AbsoluteExpiresAt.UTC()
	row.IdleExpiresAt = row.IdleExpiresAt.UTC()
	return row, nil
}

// TouchSession extends the idle deadline. It requires WithAuth.
func (s *Store) TouchSession(ctx context.Context, id uuid.UUID, now, idleExpiresAt time.Time) (bool, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = scope.tx.QueryRow(ctx, `SELECT auth_touch_session($1, $2, $3)`, id, now, idleExpiresAt).Scan(&ok)
	if err != nil {
		return false, mapAuthErr(err)
	}
	return ok, nil
}

// RevokeSessionByHash marks the session of a cookie hash as revoked. It requires WithAuth.
func (s *Store) RevokeSessionByHash(ctx context.Context, tokenHash []byte, now time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_revoke_by_token_hash($1, $2)`, tokenHash, now)
	return mapAuthErr(err)
}

// RevokeSessionForActor revokes one session when the actor is allowed. It requires WithAuth.
// The status is ok, missing or forbidden.
func (s *Store) RevokeSessionForActor(ctx context.Context, sessionID, tenantID, actor uuid.UUID, now time.Time) (string, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return "", err
	}
	var status string
	err = scope.tx.QueryRow(ctx, `SELECT auth_revoke_session($1, $2, $3, $4)`, sessionID, tenantID, actor, now).Scan(&status)
	if err != nil {
		return "", mapAuthErr(err)
	}
	return status, nil
}

// RevokeTenantSessions revokes every live session of a provider. It requires WithAuth.
func (s *Store) RevokeTenantSessions(ctx context.Context, tenantID uuid.UUID, now time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_revoke_tenant_sessions($1, $2)`, tenantID, now)
	return mapAuthErr(err)
}

// RevokeUserTenantSessions revokes live sessions of one user in one provider. It requires WithAuth.
func (s *Store) RevokeUserTenantSessions(ctx context.Context, tenantID, userID uuid.UUID, now time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_revoke_membership_sessions($1, $2, $3)`, tenantID, userID, now)
	return mapAuthErr(err)
}

// ListTenantSessions returns live sessions of one tenant. It requires WithAuth.
func (s *Store) ListTenantSessions(ctx context.Context, tenantID uuid.UUID, now time.Time) ([]ListedSession, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.tx.Query(ctx, `
		SELECT session_id, user_id, created_at, last_seen_at, absolute_expires_at
		FROM auth_list_tenant_sessions($1, $2)
	`, tenantID, now)
	if err != nil {
		return nil, mapAuthErr(err)
	}
	defer rows.Close()
	var out []ListedSession
	for rows.Next() {
		var row ListedSession
		if err := rows.Scan(&row.ID, &row.UserID, &row.CreatedAt, &row.LastSeenAt, &row.AbsoluteExpiresAt); err != nil {
			return nil, mapAuthErr(err)
		}
		row.CreatedAt = row.CreatedAt.UTC()
		row.LastSeenAt = row.LastSeenAt.UTC()
		row.AbsoluteExpiresAt = row.AbsoluteExpiresAt.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mapAuthErr(err)
	}
	return out, nil
}

// InsertAuthToken stores a hashed one-time token and retires previous unused tokens of the same purpose.
func (s *Store) InsertAuthToken(ctx context.Context, in NewAuthToken) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	var tenantID any
	var membershipID any
	if in.TenantID != uuid.Nil {
		tenantID = in.TenantID
	}
	if in.MembershipID != uuid.Nil {
		membershipID = in.MembershipID
	}
	_, err = scope.tx.Exec(ctx, `
		SELECT auth_insert_auth_token($1, $2, $3, $4, $5, $6, $7, $8)
	`, in.ID, in.TokenHash, in.Purpose, in.UserID, tenantID, membershipID, in.ExpiresAt, in.Now)
	return mapAuthErr(err)
}

// InvalidateAuthToken marks one hashed token as used. It requires WithAuth.
func (s *Store) InvalidateAuthToken(ctx context.Context, tokenHash []byte, now time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_invalidate_auth_token($1, $2)`, tokenHash, now)
	return mapAuthErr(err)
}

// AcceptInvite consumes an invite, sets the password and activates the user. It requires WithAuth.
func (s *Store) AcceptInvite(ctx context.Context, tokenHash []byte, passwordHash string, now time.Time) (TokenResult, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return TokenResult{}, err
	}
	var result TokenResult
	err = scope.tx.QueryRow(ctx, `
		SELECT token_status, user_id, tenant_id, membership_id
		FROM auth_accept_invite($1, $2, $3)
	`, tokenHash, passwordHash, now).Scan(&result.Status, &result.UserID, &result.TenantID, &result.MembershipID)
	if err != nil {
		return TokenResult{}, mapAuthErr(err)
	}
	return result, nil
}

// ConfirmPasswordReset consumes a reset token, replaces the password and revokes sessions.
func (s *Store) ConfirmPasswordReset(ctx context.Context, tokenHash []byte, passwordHash string, now time.Time) (TokenResult, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return TokenResult{}, err
	}
	var result TokenResult
	err = scope.tx.QueryRow(ctx, `
		SELECT token_status, user_id
		FROM auth_confirm_password_reset($1, $2, $3)
	`, tokenHash, passwordHash, now).Scan(&result.Status, &result.UserID)
	if err != nil {
		return TokenResult{}, mapAuthErr(err)
	}
	return result, nil
}

// LockedUntil reports an active lock. The zero time means the subject is not locked.
func (s *Store) LockedUntil(ctx context.Context, kind string, subjectHash []byte, now time.Time) (time.Time, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return time.Time{}, err
	}
	var until *time.Time
	err = scope.tx.QueryRow(ctx, `SELECT auth_locked_until($1, $2, $3)`, kind, subjectHash, now).Scan(&until)
	if err != nil {
		return time.Time{}, mapAuthErr(err)
	}
	if until == nil {
		return time.Time{}, nil
	}
	return until.UTC(), nil
}

// BumpFailure increments a counter unless it is already locked. It requires WithAuth.
func (s *Store) BumpFailure(ctx context.Context, kind string, subjectHash []byte, now time.Time, window time.Duration) (Attempt, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return Attempt{}, err
	}
	var attempt Attempt
	var locked *time.Time
	err = scope.tx.QueryRow(ctx, `
		SELECT failures, locked_until, already_locked
		FROM auth_bump_failure($1, $2, $3, $4::interval)
	`, kind, subjectHash, now, intervalText(window)).Scan(&attempt.Failures, &locked, &attempt.AlreadyLocked)
	if err != nil {
		return Attempt{}, mapAuthErr(err)
	}
	if locked != nil {
		attempt.LockedUntil = locked.UTC()
	}
	return attempt, nil
}

// SetLock sets the moment a counter unlocks. It requires WithAuth.
func (s *Store) SetLock(ctx context.Context, kind string, subjectHash []byte, until time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_set_lock($1, $2, $3)`, kind, subjectHash, until)
	return mapAuthErr(err)
}

// ClearFailures removes one counter. It requires WithAuth.
func (s *Store) ClearFailures(ctx context.Context, kind string, subjectHash []byte) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_clear_failures($1, $2)`, kind, subjectHash)
	return mapAuthErr(err)
}

func intervalText(window time.Duration) string {
	if window <= 0 {
		window = time.Second
	}
	return fmt.Sprintf("%d milliseconds", window.Milliseconds())
}

func mapAuthErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAuthContextRequired) || errors.Is(err, ErrPlatformContextRequired) || errors.Is(err, ErrTenantContextRequired) || errors.Is(err, ErrInvalidEmail) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42501":
			return ErrAuthContextRequired
		case "P0002":
			return ErrNotFound
		default:
			return errors.New("falha de autenticação")
		}
	}
	return mapErr(err)
}
