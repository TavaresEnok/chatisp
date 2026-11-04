package bothook

import (
	"time"

	"github.com/google/uuid"
)

const (
	SimulationInProcess = "in-process"

	statePending     = "pending"
	stateReceived    = "received"
	stateHandedOff   = "handed_off"
	stateUncertain   = "uncertain"
	stateExpired     = "expired"
	stateUnavailable = "unavailable"
	stateSwitchedOff = "switched_off"

	receiptReceived = "received"

	actionReply       = "reply"
	actionHandoff     = "handoff"
	actionUncertain   = "uncertain"
	actionUnavailable = "unavailable"

	noticePending     = "Pendência da automação: o gancho ainda não concluiu esta conversa."
	noticeReceived    = "O gancho confirmou o recebimento. Isso não encerra o atendimento."
	noticeUncertain   = "Resultado do gancho incerto. A conversa voltou para a fila humana."
	noticeExpired     = "O prazo do gancho venceu. A conversa voltou para a fila humana."
	noticeUnavailable = "O gancho não respondeu. A conversa voltou para a fila humana."
	noticeSwitched    = "O interruptor desligou o gancho e devolveu a conversa à fila humana."
	noticeHandoff     = "O gancho devolveu a conversa à fila humana."
)

// Principal is the panel actor. The bot API uses the inbox token instead.
type Principal struct {
	TenantID, UserID, MembershipID uuid.UUID
	Role                           string
	IsSuperadmin                   bool
}

// InstallCommand creates the single hook of an inbox. The token is returned once.
type InstallCommand struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
	DeadlineSeconds int       `json:"deadline_seconds"`
}

// RotateCommand replaces the token. The previous secret stops working.
type RotateCommand struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
}

// SwitchCommand is the inbox interruptor. Turning it off invalidates bot possession.
type SwitchCommand struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Enabled         bool      `json:"enabled"`
}

// ReplyCommand is an idempotent bot reply bound to the granted epoch.
type ReplyCommand struct {
	RequestID uuid.UUID `json:"request_id"`
	Epoch     int64     `json:"controller_epoch"`
	Body      string    `json:"body"`
}

// HandoffCommand returns the conversation to the human queue.
type HandoffCommand struct {
	RequestID uuid.UUID `json:"request_id"`
	Epoch     int64     `json:"controller_epoch"`
	SectorID  uuid.UUID `json:"sector_id"`
	Reason    string    `json:"reason"`
	Note      string    `json:"note"`
}

// Installation is the hook. Token is set only on the call that created or rotated it.
type Installation struct {
	InboxID         uuid.UUID `json:"inbox_id"`
	Token           string    `json:"token,omitempty"`
	TokenPrefix     string    `json:"token_prefix"`
	TokenRevealed   bool      `json:"token_revealed"`
	DeadlineSeconds int       `json:"deadline_seconds"`
	Enabled         bool      `json:"enabled"`
	Version         int64     `json:"version"`
	Simulation      string    `json:"simulation"`
}

// SwitchResult is the interruptor after it commits.
type SwitchResult struct {
	InboxID  uuid.UUID `json:"inbox_id"`
	Enabled  bool      `json:"enabled"`
	Version  int64     `json:"version"`
	Released int       `json:"released"`
}

// Hook is the public view. It never includes the token.
type Hook struct {
	InboxID         uuid.UUID `json:"inbox_id"`
	TokenPrefix     string    `json:"token_prefix"`
	DeadlineSeconds int       `json:"deadline_seconds"`
	Enabled         bool      `json:"enabled"`
	Version         int64     `json:"version"`
	Simulation      string    `json:"simulation"`
}

// Session is one granted possession.
type Session struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"-"`
	InboxID        uuid.UUID `json:"inbox_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	GrantedEpoch   int64     `json:"granted_epoch"`
	DeadlineAt     time.Time `json:"deadline_at"`
	State          string    `json:"state"`
	Receipt        string    `json:"receipt,omitempty"`
	Notice         string    `json:"notice"`
}

// ReplyResult is the queued bot message. A repeat of the same request returns the same id.
type ReplyResult struct {
	MessageID uuid.UUID `json:"message_id"`
	Epoch     int64     `json:"controller_epoch"`
	State     string    `json:"state"`
}

// HandoffResult is the human queue after the atomic transfer.
// Controller is menu and AssigneeID is empty: that is the queue the schema allows.
type HandoffResult struct {
	HandoffID  uuid.UUID  `json:"handoff_id"`
	Epoch      int64      `json:"controller_epoch"`
	SectorID   uuid.UUID  `json:"sector_id"`
	Controller string     `json:"controller"`
	Status     string     `json:"status"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	Notice     string     `json:"notice"`
}

// Notice is one automation pending item. Uncertain stays in the list.
type Notice struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	State          string    `json:"state"`
	Notice         string    `json:"notice"`
	GrantedEpoch   int64     `json:"granted_epoch"`
}

// NoticeList is the panel payload.
type NoticeList struct {
	Notices []Notice `json:"notices"`
}
