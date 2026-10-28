package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	_ "time/tzdata" // zoneinfo embutido: o contêiner de teste pode não ter tzdata do sistema

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"chatisp/internal/platform/db/dbgen"
)

// CreateTenant inserts a provider account. It requires WithPlatform.
func (s *Store) CreateTenant(ctx context.Context, in NewTenant) (Tenant, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return Tenant{}, err
	}
	timezone, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return Tenant{}, err
	}
	if in.Status != "active" && in.Status != "suspended" {
		return Tenant{}, ErrInvalidTenantStatus
	}
	limits, err := normalizeLimits(in.Limits)
	if err != nil {
		return Tenant{}, err
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := scope.q.InsertTenant(ctx, dbgen.InsertTenantParams{
		ID:       id,
		Timezone: timezone,
		Status:   in.Status,
		Limits:   limits,
	})
	if err != nil {
		return Tenant{}, mapErr(err)
	}
	return tenantFromRow(row), nil
}

// GetTenant reads the tenant of the active WithTenant scope.
func (s *Store) GetTenant(ctx context.Context) (Tenant, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return Tenant{}, err
	}
	row, err := scope.q.GetTenant(ctx, scope.id)
	if err != nil {
		return Tenant{}, mapErr(err)
	}
	return tenantFromRow(row), nil
}

// CreateUser inserts a global user without a credential. It requires WithPlatform.
func (s *Store) CreateUser(ctx context.Context, in NewUser) (User, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return User{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	if in.Status != "invited" && in.Status != "active" && in.Status != "disabled" {
		return User{}, ErrInvalidUserStatus
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := scope.q.InsertUser(ctx, dbgen.InsertUserParams{
		ID:     id,
		Email:  email,
		Status: in.Status,
	})
	if err != nil {
		return User{}, mapErr(err)
	}
	return User{ID: row.ID, Email: row.Email, Status: row.Status, CreatedAt: asTime(row.CreatedAt)}, nil
}

// SetPasswordHash stores a credential hash. It requires WithAuth.
func (s *Store) SetPasswordHash(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	if userID == uuid.Nil || !validPasswordHash(passwordHash) {
		return ErrInvalidPasswordHash
	}
	if _, err := scope.tx.Exec(ctx, `SELECT auth_set_password_hash($1, $2)`, userID, passwordHash); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0002" {
			return ErrNotFound
		}
		return mapErr(err)
	}
	return nil
}

// PasswordHashByEmail reads one credential hash for verification. It requires WithAuth.
// A missing user and a user without a hash both return ErrNotFound.
func (s *Store) PasswordHashByEmail(ctx context.Context, email string) (string, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return "", err
	}
	var hash *string
	if err := scope.tx.QueryRow(ctx, `SELECT auth_password_hash_by_email($1)`, normalized).Scan(&hash); err != nil {
		return "", mapErr(err)
	}
	if hash == nil || *hash == "" {
		return "", ErrNotFound
	}
	return *hash, nil
}

// CreateMembership inserts a membership for the active tenant.
func (s *Store) CreateMembership(ctx context.Context, in NewMembership) (Membership, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return Membership{}, err
	}
	if in.UserID == uuid.Nil {
		return Membership{}, ErrInvalidMembershipRole
	}
	if in.Role != "admin" && in.Role != "supervisor" && in.Role != "agent" {
		return Membership{}, ErrInvalidMembershipRole
	}
	if in.Status != "invited" && in.Status != "active" && in.Status != "disabled" {
		return Membership{}, ErrInvalidMembershipStatus
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := scope.q.InsertMembership(ctx, dbgen.InsertMembershipParams{
		ID:       id,
		TenantID: scope.id,
		UserID:   in.UserID,
		Role:     in.Role,
		Status:   in.Status,
	})
	if err != nil {
		return Membership{}, mapErr(err)
	}
	return membershipFromRow(row), nil
}

// ListMemberships returns memberships of the active tenant.
func (s *Store) ListMemberships(ctx context.Context) ([]Membership, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.q.ListMembershipsByTenant(ctx, scope.id)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Membership, 0, len(rows))
	for _, row := range rows {
		out = append(out, membershipFromRow(row))
	}
	return out, nil
}

// UpdateMembershipStatus changes one membership in the active tenant.
// A hidden row updates zero records.
func (s *Store) UpdateMembershipStatus(ctx context.Context, id uuid.UUID, status string) (int64, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return 0, err
	}
	if id == uuid.Nil {
		return 0, ErrInvalidMembershipStatus
	}
	if status != "invited" && status != "active" && status != "disabled" {
		return 0, ErrInvalidMembershipStatus
	}
	n, err := scope.q.UpdateMembershipStatus(ctx, dbgen.UpdateMembershipStatusParams{
		Status:   status,
		TenantID: scope.id,
		ID:       id,
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// CreateAuditLog writes an audit row for the active tenant.
func (s *Store) CreateAuditLog(ctx context.Context, in NewAuditLog) (AuditLog, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return AuditLog{}, err
	}
	action, err := requiredText(in.Action, 200)
	if err != nil {
		return AuditLog{}, err
	}
	targetType, err := requiredText(in.TargetType, 200)
	if err != nil {
		return AuditLog{}, err
	}
	result, err := requiredText(in.Result, 200)
	if err != nil {
		return AuditLog{}, err
	}
	if in.CorrelationID != nil && len(*in.CorrelationID) > 200 {
		return AuditLog{}, ErrInvalidAudit
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := scope.q.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		ID:            id,
		TenantID:      scope.id,
		MembershipID:  in.MembershipID,
		ActorUserID:   in.ActorUserID,
		Action:        action,
		TargetType:    targetType,
		TargetID:      in.TargetID,
		Result:        result,
		CorrelationID: in.CorrelationID,
	})
	if err != nil {
		return AuditLog{}, mapErr(err)
	}
	return auditFromRow(row), nil
}

// ListAuditLogs returns audit rows of the active tenant.
func (s *Store) ListAuditLogs(ctx context.Context) ([]AuditLog, error) {
	scope, err := tenantFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.q.ListAuditLogsByTenant(ctx, scope.id)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AuditLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditFromRow(row))
	}
	return out, nil
}

// CreatePlatformAuditLog writes a platform audit row. It requires WithPlatform.
func (s *Store) CreatePlatformAuditLog(ctx context.Context, in NewPlatformAuditLog) (PlatformAuditLog, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return PlatformAuditLog{}, err
	}
	actor, err := requiredText(in.Actor, 200)
	if err != nil {
		return PlatformAuditLog{}, err
	}
	action, err := requiredText(in.Action, 200)
	if err != nil {
		return PlatformAuditLog{}, err
	}
	targetType, err := requiredText(in.TargetType, 200)
	if err != nil {
		return PlatformAuditLog{}, err
	}
	result, err := requiredText(in.Result, 200)
	if err != nil {
		return PlatformAuditLog{}, err
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := scope.q.InsertPlatformAuditLog(ctx, dbgen.InsertPlatformAuditLogParams{
		ID:            id,
		Actor:         actor,
		Action:        action,
		TargetType:    targetType,
		TargetID:      in.TargetID,
		Result:        result,
		CorrelationID: in.CorrelationID,
	})
	if err != nil {
		return PlatformAuditLog{}, mapErr(err)
	}
	return platformAuditFromRow(row), nil
}

// ListPlatformAuditLogs returns platform audit rows. It requires WithPlatform.
func (s *Store) ListPlatformAuditLogs(ctx context.Context) ([]PlatformAuditLog, error) {
	scope, err := platformFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.q.ListPlatformAuditLogs(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]PlatformAuditLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, platformAuditFromRow(row))
	}
	return out, nil
}

func normalizeTimezone(raw string) (string, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" || tz == "Local" || len(tz) > 64 || strings.ContainsAny(tz, " \t\r\n") {
		return "", ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", ErrInvalidTimezone
	}
	return tz, nil
}

func normalizeLimits(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytesTrim(raw)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > 16*1024 {
		return nil, ErrInvalidLimits
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, ErrInvalidLimits
	}
	return raw, nil
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func validPasswordHash(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsAny(value, "\r\n")
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 320 || strings.ContainsAny(email, " \t\r\n") {
		return "", ErrInvalidEmail
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func requiredText(value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return "", ErrInvalidAudit
	}
	return value, nil
}

func tenantFromRow(row dbgen.Tenant) Tenant {
	return Tenant{
		ID:        row.ID,
		Timezone:  row.Timezone,
		Status:    row.Status,
		Limits:    row.Limits,
		CreatedAt: asTime(row.CreatedAt),
		UpdatedAt: asTime(row.UpdatedAt),
	}
}

func membershipFromRow(row dbgen.Membership) Membership {
	return Membership{
		ID:        row.ID,
		TenantID:  row.TenantID,
		UserID:    row.UserID,
		Role:      row.Role,
		Status:    row.Status,
		CreatedAt: asTime(row.CreatedAt),
	}
}

func auditFromRow(row dbgen.AuditLog) AuditLog {
	return AuditLog{
		ID:            row.ID,
		TenantID:      row.TenantID,
		MembershipID:  row.MembershipID,
		ActorUserID:   row.ActorUserID,
		Action:        row.Action,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Result:        row.Result,
		CorrelationID: row.CorrelationID,
		CreatedAt:     asTime(row.CreatedAt),
	}
}

func platformAuditFromRow(row dbgen.PlatformAuditLog) PlatformAuditLog {
	return PlatformAuditLog{
		ID:            row.ID,
		Actor:         row.Actor,
		Action:        row.Action,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Result:        row.Result,
		CorrelationID: row.CorrelationID,
		CreatedAt:     asTime(row.CreatedAt),
	}
}

func asTime(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return err
	}
	if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "postgresql://") {
		return errors.New("falha de banco")
	}
	return err
}
