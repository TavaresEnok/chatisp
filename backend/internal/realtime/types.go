package realtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/metrics"
)

// Principal represents the authenticated panel actor.
type Principal struct {
	SessionID    uuid.UUID `json:"session_id"`
	UserID       uuid.UUID `json:"user_id"`
	MembershipID uuid.UUID `json:"membership_id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	Role         string    `json:"role"`
	IsSuperadmin bool      `json:"is_superadmin"`
}

// AttendancePrincipal adapts Principal to attendanceService.Principal.
func (p Principal) AttendancePrincipal() attendanceService.Principal {
	return attendanceService.Principal{
		UserID:       p.UserID,
		MembershipID: p.MembershipID,
		TenantID:     p.TenantID,
		Role:         p.Role,
		IsSuperadmin: p.IsSuperadmin,
	}
}

// Authenticator verifies the session cookie and revalidates operational access.
type Authenticator interface {
	AuthenticateSession(ctx context.Context, sessionToken string) (Principal, error)
	RevalidatePrincipal(ctx context.Context, p Principal, sessionToken string) error
}

// AttendanceService provides access to queue snapshots and realtime event replay.
type AttendanceService interface {
	Snapshot(ctx context.Context, p attendanceService.Principal, in store.SnapshotInput) (store.Snapshot, error)
	Events(ctx context.Context, p attendanceService.Principal, inboxID uuid.UUID, after int64) (store.EventPage, error)
}

// InboxResolver finds the inbox of a conversation for event filtering.
type InboxResolver interface {
	ResolveConversationInbox(ctx context.Context, tenantID, conversationID uuid.UUID) (uuid.UUID, error)
}

// Notifier provides lightweight event notification signaling per tenant.
type Notifier interface {
	Subscribe(tenantID uuid.UUID) (<-chan struct{}, func())
	Notify(ctx context.Context, tenantID uuid.UUID) error
}

// Options configures the Realtime websocket handler.
type Options struct {
	TrustedOrigins     []string
	Authenticator      Authenticator
	Attendance         AttendanceService
	InboxResolver      InboxResolver
	Notifier           Notifier
	PollInterval       time.Duration
	RevalidateInterval time.Duration
	BufferSize         int
	WriteTimeout       time.Duration
	Logger             *slog.Logger
	DB                 *db.Store
	// Metrics counts sockets on the process registry after a successful upgrade.
	// Nil keeps the handler usable in tests that do not observe the gauge.
	Metrics *metrics.Metrics
}

// ClientMessage is sent by the client over websocket.
type ClientMessage struct {
	Action         string    `json:"action,omitempty"`
	Type           string    `json:"type,omitempty"`
	InboxID        uuid.UUID `json:"inbox_id,omitempty"`
	ConversationID uuid.UUID `json:"conversation_id,omitempty"`
	SinceSeq       int64     `json:"since_seq,omitempty"`
	After          int64     `json:"after,omitempty"`
	WithSnapshot   bool      `json:"with_snapshot,omitempty"`
}

// Operation returns the effective operation name from Action or Type.
func (m ClientMessage) Operation() string {
	if m.Action != "" {
		return m.Action
	}
	return m.Type
}

// ServerMessage is sent by the server over websocket.
type ServerMessage struct {
	Type          string          `json:"type"`
	InboxID       uuid.UUID       `json:"inbox_id,omitempty"`
	NextCursor    int64           `json:"next_cursor,omitempty"`
	Conversations []store.Head    `json:"conversations,omitempty"`
	Messages      []store.Message `json:"messages,omitempty"`
	Events        []store.Event   `json:"events,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	Code          string          `json:"code,omitempty"`
	Message       string          `json:"message,omitempty"`
}
