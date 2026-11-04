package retention

import (
	"context"
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
// Objects, when set, deletes the private file of each removed attachment
// in the same transaction attempt. A failure rolls the database change back.
type Service struct {
	DB      *db.Store
	Objects ObjectDeleter
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

const (
	// PrivacyExportLabel states the file is the attendance dossier of one person.
	PrivacyExportLabel = "Exportação dos dados do titular neste atendimento. Não é o histórico completo do provedor nem do ERP. Cópias de backup imutável não entram neste arquivo."
	// PrivacyDeletionNote states what the subject erasure keeps.
	PrivacyDeletionNote = "O expurgo automático continua desligado. Ciclos, protocolos e motivos permanecem. Backup imutável não é apagado por esta operação."
	// TombstoneName replaces the contact name after a subject erasure.
	TombstoneName = "titular excluído"
	// NoteTombstone replaces a conversation note that cannot be empty.
	NoteTombstone = "conteúdo removido a pedido do titular"
)

// ObjectDeleter removes one private object version after the row is ready
// to leave the database. A nil deleter still removes the database reference.
type ObjectDeleter interface {
	Delete(ctx context.Context, key, version string) error
}

// PrivacyProfile is the contact card. The document ciphertext is never copied.
type PrivacyProfile struct {
	Name           string  `json:"name"`
	Phone          *string `json:"phone"`
	Email          *string `json:"email"`
	Notes          string  `json:"notes"`
	Blocked        bool    `json:"blocked"`
	ExternalRef    *string `json:"external_ref,omitempty"`
	DocumentStored bool    `json:"document_stored"`
	DocumentKind   string  `json:"document_kind,omitempty"`
	ErasedAt       *string `json:"erased_at,omitempty"`
}

// PrivacyIdentity is the channel identifier still stored for the contact.
type PrivacyIdentity struct {
	ExternalID string `json:"external_id"`
}

// PrivacyMessage is one stored body of this contact's attendance.
type PrivacyMessage struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Protocol       string    `json:"protocol"`
	Direction      string    `json:"direction"`
	Origin         string    `json:"origin"`
	Visibility     string    `json:"visibility"`
	Body           string    `json:"body"`
	IngestedAt     string    `json:"ingested_at"`
}

// PrivacyNote is an attendant note on a conversation of this contact.
type PrivacyNote struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Body           string    `json:"body"`
}

// PrivacyMedia describes an attachment without its storage key.
type PrivacyMedia struct {
	AttachmentID uuid.UUID `json:"attachment_id"`
	Name         string    `json:"name"`
	MediaType    string    `json:"media_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
}

// PrivacyExport is the admin file for one subject request.
type PrivacyExport struct {
	Label      string            `json:"label"`
	ContactID  uuid.UUID         `json:"contact_id"`
	Profile    PrivacyProfile    `json:"profile"`
	Identities []PrivacyIdentity `json:"identities"`
	Messages   []PrivacyMessage  `json:"messages"`
	Notes      []PrivacyNote     `json:"notes"`
	Media      []PrivacyMedia    `json:"media"`
	Demands    []Demand          `json:"demands"`
}

// DeletionResult is the subject erasure. AutomaticPurge stays false.
type DeletionResult struct {
	ContactID          uuid.UUID `json:"contact_id"`
	Erased             bool      `json:"erased"`
	AlreadyErased      bool      `json:"already_erased"`
	Protocols          []string  `json:"protocols"`
	MessagesRedacted   int       `json:"messages_redacted"`
	NotesRedacted      int       `json:"notes_redacted"`
	AttachmentsRemoved int       `json:"attachments_removed"`
	ObjectsRemoved     int       `json:"objects_removed"`
	AutomaticPurge     bool      `json:"automatic_purge"`
	Note               string    `json:"note"`
}
