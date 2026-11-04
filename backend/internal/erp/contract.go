package erp

import (
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Internal capability names from B.4.8. They are not supplier paths.
const (
	SearchCustomer     = "SearchCustomer"
	GetCustomerSummary = "GetCustomerSummary"
	ListOpenInvoices   = "ListOpenInvoices"
	GetInvoiceDocument = "GetInvoiceDocument"
	GetPixCode         = "GetPixCode"

	DriverFake = "fake"

	OutcomeOK          = "ok"
	OutcomeUnavailable = "unavailable"
	OutcomeHidden      = "hidden"
	OutcomeUncertain   = "uncertain"
	OutcomeNotFound    = "not_found"
	OutcomeDenied      = "denied"

	EffectRead          = "read"
	EffectUnclassified  = "unclassified"
	AvailabilityUp      = "up"
	AvailabilityDown    = "down"
	ReasonUnavailable   = "capacidade indisponível"
	ReasonHidden        = "capacidade oculta"
	ReasonNotSimulation = "adaptador não identificado como simulação"
	ReasonDown          = "erp indisponível"
	ReasonTimeout       = "tempo esgotado"
	ReasonCircuit       = "circuito aberto"
	ReasonInvoice       = "fatura não pertence ao cliente selecionado"
	ReasonUncertain     = "resultado externo incerto"
	ReasonNotFound      = "cadastro não encontrado"
)

var (
	ErrDenied            = errors.New("ação de erp não autorizada")
	ErrConflict          = errors.New("chave local já usada com outro pedido")
	ErrKeyRequired       = errors.New("chave local ausente")
	ErrReferenceRequired = errors.New("referência autorizada ausente")
	ErrPhoneNotSelector  = errors.New("telefone não escolhe o vínculo")
	ErrChoiceRequired    = errors.New("vínculo precisa ser escolhido")
	ErrNotFound          = errors.New("registro não encontrado")
	ErrInvalid           = errors.New("pedido de erp inválido")
	ErrDown              = errors.New("erp indisponível")
	ErrTimeout           = errors.New("tempo do erp esgotado")
	ErrAmbiguous         = errors.New("resultado externo incerto")
	ErrOpen              = errors.New("circuito do erp aberto")
	ErrDispatch          = errors.New("envio recusado pela janela ou pelo consentimento")
)

// DispatchError is a local send refusal. Reason is a stable policy code, never a customer payload.
type DispatchError struct {
	Reason string
}

func (e *DispatchError) Error() string {
	if e == nil || e.Reason == "" {
		return ErrDispatch.Error()
	}
	return ErrDispatch.Error() + ": " + e.Reason
}

func (e *DispatchError) Unwrap() error { return ErrDispatch }

// Principal is the attendant revalidated before a capability runs.
type Principal struct {
	TenantID   uuid.UUID
	UserID     uuid.UUID
	Membership uuid.UUID
	Role       string
	SectorIDs  []uuid.UUID
}

// Connection is the single ERP adapter of a tenant.
type Connection struct {
	TenantID     uuid.UUID
	ID           uuid.UUID
	Driver       string
	Simulated    bool
	Enabled      bool
	Availability string
	SectorID     uuid.UUID
	Capabilities map[string]bool
}

// Consult is one capability call. Reference is an opaque authorized id.
// Phone numbers are rejected. Verification is empty until D2-02 exists.
type Consult struct {
	Capability   string
	Reference    string
	CustomerID   string
	ContractID   string
	InvoiceID    string
	Verification string
	LocalKey     string
	Conversation uuid.UUID
	Now          time.Time
}

// Customer is an opaque ERP reference. It is not a commercial record.
type Customer struct {
	ExternalID  string
	ContractID  string
	DisplayName string
}

// Invoice is a transient open item. It has no monetary amount.
type Invoice struct {
	ExternalID string
	Label      string
}

// View is the typed result shown to the lateral panel.
type View struct {
	Driver      string
	Simulated   bool
	Capability  string
	Outcome     string
	Reason      string
	Effect      string
	ConsultedAt time.Time
	Customer    *Customer
	Invoices    []Invoice
	Document    []byte
	PixCode     string
	ExternalRef string
}

// Action is the durable record of one local key.
type Action struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	Conversation uuid.UUID
	Capability   string
	LocalKey     string
	Hash         [32]byte
	Effect       string
	Outcome      string
	ExternalRef  string
	View         View
	// ReasonCode is the stable token stored with the timeline event.
	ReasonCode string
	// ClientRequestID, OutboxOperation, PrincipalID, MessageID and AttemptID
	// bind a successful second copy to the outbox row that was queued.
	ClientRequestID string
	OutboxOperation string
	PrincipalID     uuid.UUID
	MessageID       uuid.UUID
	AttemptID       uuid.UUID
	// ActorID and MembershipID are the panel actor. Storage leaves them empty
	// when the call did not come from an authenticated membership.
	ActorID      uuid.UUID
	MembershipID uuid.UUID
}

// Link binds a contact to an opaque customer. Selection is explicit.
type Link struct {
	ID                 uuid.UUID
	ContactID          uuid.UUID
	ConversationID     uuid.UUID
	ExternalCustomerID string
	ExternalContractID string
	ExternalProtocol   string
	Selected           bool
}

// DefaultCapabilities are the reads the fake enables before a test opts in.
func DefaultCapabilities() []string {
	return []string{SearchCustomer, GetCustomerSummary}
}

// AllCapabilities lists the five internal names.
func AllCapabilities() []string {
	return []string{SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument, GetPixCode}
}

func knownCapability(name string) bool {
	switch name {
	case SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument, GetPixCode:
		return true
	default:
		return false
	}
}

func sensitive(name string) bool {
	return name == ListOpenInvoices || name == GetInvoiceDocument || name == GetPixCode
}

func effectOf(name string) string {
	if name == GetInvoiceDocument || name == GetPixCode {
		return EffectUnclassified
	}
	return EffectRead
}

// Authorize rechecks role and sector. Supervisors see every sector of the tenant.
func Authorize(p Principal, conn Connection) error {
	if p.TenantID == uuid.Nil || conn.TenantID == uuid.Nil || p.TenantID != conn.TenantID {
		return ErrDenied
	}
	switch p.Role {
	case "admin", "supervisor":
		return nil
	case "agent":
		if conn.SectorID == uuid.Nil {
			return ErrDenied
		}
		for _, id := range p.SectorIDs {
			if id == conn.SectorID {
				return nil
			}
		}
		return ErrDenied
	default:
		return ErrDenied
	}
}

// NormalizeReference accepts an opaque reference and rejects a phone.
func NormalizeReference(raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" || len(ref) > 128 || strings.ContainsAny(ref, " \t\r\n") {
		return "", ErrReferenceRequired
	}
	if strings.HasPrefix(ref, "+") || digitsOnly(ref) {
		return "", ErrPhoneNotSelector
	}
	return ref, nil
}

func digitsOnly(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Choose returns the link an attendant already selected.
// A phone match never selects a link by itself.
func Choose(links []Link) (Link, error) {
	var selected []Link
	for _, link := range links {
		if link.Selected {
			selected = append(selected, link)
		}
	}
	if len(selected) == 1 {
		return selected[0], nil
	}
	if len(links) == 0 {
		return Link{}, ErrNotFound
	}
	return Link{}, ErrChoiceRequired
}

// KeepLocalProtocol reports the protocol already told to the customer.
// The ERP reference is additional and does not replace it.
func KeepLocalProtocol(local, external string) (string, error) {
	local = strings.TrimSpace(local)
	external = strings.TrimSpace(external)
	if local == "" || len(local) > 64 {
		return "", ErrInvalid
	}
	if strings.ContainsAny(external, "\r\n") || len(external) > 128 {
		return "", ErrInvalid
	}
	return local, nil
}

func fingerprint(in Consult) [32]byte {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		in.Capability,
		in.Reference,
		in.CustomerID,
		in.ContractID,
		in.InvoiceID,
		in.Verification,
		in.Conversation.String(),
	}, "\x1f")))
	return sum
}

func requireKey(in Consult) error {
	if in.Capability != GetInvoiceDocument && in.Capability != GetPixCode {
		return nil
	}
	key := strings.TrimSpace(in.LocalKey)
	if key == "" || len(key) > 200 || key != in.LocalKey {
		return ErrKeyRequired
	}
	return nil
}

// AcceptReply is the local attendance reply. It does not call the ERP.
func AcceptReply(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrInvalid
	}
	return text, nil
}
