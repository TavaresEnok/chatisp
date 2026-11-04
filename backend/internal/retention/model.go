package retention

import (
	"errors"

	"github.com/google/uuid"

	"chatisp/internal/platform/db"
)

const (
	// DemandLabel é o rótulo obrigatório da exportação.
	DemandLabel = "Histórico estruturado de demandas do atendimento. Não é o histórico completo do provedor nem do ERP."
	// PurgeReason explica a recusa enquanto D2-06 segue aberta.
	PurgeReason = "expurgo automático desligado enquanto o enquadramento do provedor estiver pendente"
	// PlanNote descreve o efeito do limite sem apagar histórico.
	PlanNote = "O excesso bloqueia cadastro ou upload novo e não apaga o histórico."
)

var (
	// ErrForbidden means the caller cannot run this operation.
	ErrForbidden = errors.New("acesso negado")
	// ErrNotFound means the contact is not in this provider.
	ErrNotFound = errors.New("contato não encontrado")
	// ErrInvalid means the policy or the export format cannot be stored.
	ErrInvalid = errors.New("dados inválidos")
	// ErrPurgeDisabled means the purge job refused to delete anything.
	ErrPurgeDisabled = errors.New("expurgo automático desligado")
	// ErrUnavailable means the database cannot answer.
	ErrUnavailable = errors.New("serviço indisponível")
)

// Classes are the retention buckets. They do not share one duration.
var Classes = []string{"content", "media", "envelope", "log", "job"}

// Principal is the panel actor. Superadmin is rejected.
type Principal struct {
	TenantID     uuid.UUID
	UserID       uuid.UUID
	MembershipID uuid.UUID
	Role         string
	IsSuperadmin bool
}

// Service reads and writes retention for one provider.
type Service struct {
	DB *db.Store
}

// Policy is the provider retention profile. Nil days are still undefined.
type Policy struct {
	DisplayName    string `json:"display_name"`
	ContentDays    *int   `json:"content_days"`
	MediaDays      *int   `json:"media_days"`
	EnvelopeDays   *int   `json:"envelope_days"`
	LogDays        *int   `json:"log_days"`
	JobDays        *int   `json:"job_days"`
	AutomaticPurge bool   `json:"automatic_purge"`
}

// Demand is one attendance cycle of a contact, not an ERP case history.
type Demand struct {
	Protocol   string  `json:"protocol"`
	Cycle      int     `json:"cycle"`
	OpenedAt   string  `json:"opened_at"`
	ClosedAt   *string `json:"closed_at,omitempty"`
	Reason     string  `json:"reason"`
	Synthesis  string  `json:"synthesis"`
	Forwarding string  `json:"forwarding"`
	Author     string  `json:"author"`
}

// Export is the consumer demand file.
type Export struct {
	Label     string    `json:"label"`
	ContactID uuid.UUID `json:"contact_id"`
	Demands   []Demand  `json:"demands"`
}

// PlanUsage reports the stored caps and the current counts.
type PlanUsage struct {
	MaxAgents       *int   `json:"max_agents,omitempty"`
	Agents          int    `json:"agents"`
	MaxNumbers      *int   `json:"max_numbers,omitempty"`
	Numbers         int    `json:"numbers"`
	MaxStorageBytes *int64 `json:"max_storage_bytes,omitempty"`
	StorageBytes    int64  `json:"storage_bytes"`
	Note            string `json:"note"`
}

// PurgeResult records a refusal. Deleted is always false.
type PurgeResult struct {
	Refused bool `json:"refused"`
	Deleted bool `json:"deleted"`
	Tasks   int  `json:"tasks"`
}
