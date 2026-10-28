package db

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Tenant is one provider account.
type Tenant struct {
	ID        uuid.UUID
	Timezone  string
	Status    string
	Limits    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewTenant is the platform input for a provider account.
// The caller supplies the id; it is not taken from request JSON by domain code.
type NewTenant struct {
	ID       uuid.UUID
	Timezone string
	Status   string
	Limits   json.RawMessage
}

// User is a global identity. Password hashes are not returned.
type User struct {
	ID        uuid.UUID
	Email     string
	Status    string
	CreatedAt time.Time
}

// NewUser is the platform input for a global user.
// Password hashes are not accepted here. WithAuth writes them.
type NewUser struct {
	ID     uuid.UUID
	Email  string
	Status string
}

// Membership binds a global user to one tenant.
type Membership struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    uuid.UUID
	Role      string
	Status    string
	CreatedAt time.Time
}

// NewMembership is created inside WithTenant. TenantID comes from that context.
type NewMembership struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Role   string
	Status string
}

// AuditLog is a tenant-scoped audit record.
type AuditLog struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	MembershipID  *uuid.UUID
	ActorUserID   *uuid.UUID
	Action        string
	TargetType    string
	TargetID      *uuid.UUID
	Result        string
	CorrelationID *string
	CreatedAt     time.Time
}

// NewAuditLog is written inside WithTenant. TenantID comes from that context.
type NewAuditLog struct {
	ID            uuid.UUID
	MembershipID  *uuid.UUID
	ActorUserID   *uuid.UUID
	Action        string
	TargetType    string
	TargetID      *uuid.UUID
	Result        string
	CorrelationID *string
}

// PlatformAuditLog is platform audit, separate from tenant audit.
type PlatformAuditLog struct {
	ID            uuid.UUID
	Actor         string
	Action        string
	TargetType    string
	TargetID      *string
	Result        string
	CorrelationID *string
	CreatedAt     time.Time
}

// NewPlatformAuditLog is written inside WithPlatform.
type NewPlatformAuditLog struct {
	ID            uuid.UUID
	Actor         string
	Action        string
	TargetType    string
	TargetID      *string
	Result        string
	CorrelationID *string
}
