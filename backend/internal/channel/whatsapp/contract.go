// Package whatsapp is the internal text-send contract for a future WhatsApp
// worker. It is not a Graph API client: it does not choose a version, call
// Meta or Evolution, or send media, templates or prices.
//
// Local outbox states and remote delivery evidence are different types.
// Acceptance is not delivery. A missing response is not success.
package whatsapp

import (
	"context"
	"errors"
	"strings"
)

// ConnectorType is the origin of one stable connection. Replacing a driver
// creates a new connection; identifiers are not reused across types.
type ConnectorType string

const (
	ConnectorMetaCloud    ConnectorType = "meta_cloud"
	ConnectorEvolutionWeb ConnectorType = "evolution_web"
)

// ConnectionID is the internal id of one connection.
type ConnectionID string

// RecipientID is the internal channel identity of the recipient.
// It is not a phone number and not an ERP customer id.
type RecipientID string

// AttemptID identifies one local dispatch attempt.
type AttemptID string

// LocalState is the outbox state owned by this process.
// delivered and read are not local states.
type LocalState string

const (
	StateQueued    LocalState = "queued"
	StateSending   LocalState = "sending"
	StateAccepted  LocalState = "accepted"
	StateFailed    LocalState = "failed"
	StateRetrying  LocalState = "retrying"
	StateUncertain LocalState = "uncertain"
	StateCancelled LocalState = "cancelled"
)

// Valid reports whether s is one of the local outbox states.
func (s LocalState) Valid() bool {
	switch s {
	case StateQueued, StateSending, StateAccepted, StateFailed, StateRetrying, StateUncertain, StateCancelled:
		return true
	default:
		return false
	}
}

// RemoteEvidence is what a connector actually reported after acceptance.
// The empty value means no remote evidence. It is not implied by acceptance.
type RemoteEvidence string

const (
	EvidenceNone      RemoteEvidence = ""
	EvidenceDelivered RemoteEvidence = "delivered"
	EvidenceRead      RemoteEvidence = "read"
)

// Valid reports whether e is a known remote evidence value, including none.
func (e RemoteEvidence) Valid() bool {
	switch e {
	case EvidenceNone, EvidenceDelivered, EvidenceRead:
		return true
	default:
		return false
	}
}

// Capabilities lists features this driver implements.
// A false field is an explicit gap, not a successful no-op.
type Capabilities struct {
	Text                   bool
	Media                  bool
	Templates              bool
	Interactive            bool
	DeliveryReceipts       bool
	ReadReceipts           bool
	Pricing                bool
	OfficialCustomerWindow bool
}

// TextRequest is one local intention to send plain text.
type TextRequest struct {
	AttemptID     AttemptID
	ConnectionID  ConnectionID
	ConnectorType ConnectorType
	RecipientID   RecipientID
	// CallbackData is correlation copied to biz_opaque_callback_data only by a
	// future driver, and only where that endpoint documents the field.
	// It does not make the remote send idempotent.
	CallbackData string
	Text         string
}

// TextDriver is the send surface a future worker calls.
// Media and webhooks stay outside this contract. Templates use TemplateSender.
type TextDriver interface {
	Capabilities() Capabilities
	SendText(ctx context.Context, req TextRequest) (Result, error)
}

// TemplateRequest is one local intention to send an already authorized template.
// It is not a Graph call and does not carry a rendered body.
type TemplateRequest struct {
	AttemptID     AttemptID
	ConnectionID  ConnectionID
	ConnectorType ConnectorType
	RecipientID   RecipientID
	CallbackData  string
	TemplateID    string
	Language      string
}

// TemplateSender is the identified local template contract.
// The simulation driver implements it without a network. An official driver
// must refuse before any remote call. Capabilities.Templates stays false until
// a later batch binds media and real template delivery.
type TemplateSender interface {
	SendTemplate(ctx context.Context, req TemplateRequest) (Result, error)
}

// Observation is the evidence available when a send attempt finishes.
// Zero values are not success and are not a safe retry.
type Observation struct {
	// AcceptedMessageID is the provider reference returned with documented
	// acceptance. Empty means this process has no acceptance evidence,
	// including when a response was lost after a remote accept.
	AcceptedMessageID string
	Remote            RemoteEvidence
	HTTPStatus        int
	// RejectionCode is set only for a known definitive rejection.
	RejectionCode string
	// NotDispatched is true only with proof the external call never started.
	NotDispatched bool
	// ClaimedSafeRetry is a caller assertion. It is not evidence.
	// An isolated HTTP 429 or 5xx plus this claim stays uncertain.
	ClaimedSafeRetry bool
	// CancelBeforeDispatch is a local decision. It becomes StateCancelled
	// only together with NotDispatched.
	CancelBeforeDispatch bool
	// Detail is a short pt-BR note safe to show. Message bodies are rejected.
	Detail string
}

// Result is the classified outcome of one attempt.
// Local and Remote stay independent. Simulation marks a test double.
type Result struct {
	AttemptID         AttemptID
	Local             LocalState
	Remote            RemoteEvidence
	ProviderMessageID string
	HTTPStatus        int
	RejectionCode     string
	Detail            string
	Simulation        bool
}

// HTTPStatusAllowsRetry reports whether an HTTP status by itself proves that
// repeating the send is safe. It is always false: 429 and 5xx are not enough,
// and no other status is treated as that proof.
func HTTPStatusAllowsRetry(int) bool {
	return false
}

// Classify maps evidence to a local state.
//
// Documented acceptance requires a provider message id. Delivery and read
// are copied only when the observation already carries them. A result with
// neither a definitive rejection nor proof that dispatch never started is
// uncertain, so a missing or lost response cannot become success. Retrying
// is reserved for proof that the external call never started. A claim of
// safe retry, or an HTTP status alone, does not reach that state.
func Classify(o Observation) Result {
	remote := normalizeRemote(o.Remote)
	switch {
	case conflicting(o):
		return Result{
			Local:      StateUncertain,
			HTTPStatus: o.HTTPStatus,
			Detail:     safeDetail(o.Detail, "evidências conflitantes; resultado incerto"),
		}
	case o.AcceptedMessageID != "":
		return Result{
			Local:             StateAccepted,
			Remote:            remote,
			ProviderMessageID: o.AcceptedMessageID,
			HTTPStatus:        o.HTTPStatus,
			Detail:            safeDetail(o.Detail, "envio aceito pelo conector"),
		}
	case o.CancelBeforeDispatch && o.NotDispatched:
		return Result{
			Local:  StateCancelled,
			Detail: safeDetail(o.Detail, "envio cancelado antes do despacho"),
		}
	case o.CancelBeforeDispatch:
		return Result{
			Local:      StateUncertain,
			HTTPStatus: o.HTTPStatus,
			Detail:     safeDetail(o.Detail, "cancelamento local não alcança um envio já iniciado"),
		}
	case o.RejectionCode != "":
		return Result{
			Local:         StateFailed,
			HTTPStatus:    o.HTTPStatus,
			RejectionCode: o.RejectionCode,
			Detail:        safeDetail(o.Detail, "rejeição definitiva do conector"),
		}
	case o.NotDispatched:
		return Result{
			Local:  StateRetrying,
			Detail: safeDetail(o.Detail, "despacho não iniciado; a repetição local é segura"),
		}
	default:
		// ClaimedSafeRetry is ignored. HTTPStatusAllowsRetry is never true,
		// so neither a claim nor a status code can reach StateRetrying.
		return Result{
			Local:      StateUncertain,
			HTTPStatus: o.HTTPStatus,
			Detail:     safeDetail(o.Detail, "resultado remoto desconhecido"),
		}
	}
}

func conflicting(o Observation) bool {
	if o.AcceptedMessageID != "" && (o.RejectionCode != "" || o.NotDispatched || o.CancelBeforeDispatch) {
		return true
	}
	if o.NotDispatched && o.RejectionCode != "" {
		return true
	}
	return false
}

func normalizeRemote(e RemoteEvidence) RemoteEvidence {
	if e.Valid() {
		return e
	}
	return EvidenceNone
}

func safeDetail(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
		return fallback
	}
	return value
}

// Validate checks a text request before any dispatch.
// Errors are in pt-BR and do not include the message body or callback data.
func Validate(req TextRequest) error {
	if !validToken(string(req.AttemptID), 128) {
		return errors.New("identificador da tentativa inválido")
	}
	if !validToken(string(req.ConnectionID), 128) {
		return errors.New("identificador da conexão inválido")
	}
	if !validToken(string(req.RecipientID), 128) {
		return errors.New("identificador do destinatário inválido")
	}
	switch req.ConnectorType {
	case ConnectorMetaCloud, ConnectorEvolutionWeb:
	default:
		return errors.New("conector inválido")
	}
	if strings.TrimSpace(req.Text) == "" || strings.ContainsRune(req.Text, 0) {
		return errors.New("texto inválido")
	}
	if req.CallbackData != "" && !validCallback(req.CallbackData) {
		return errors.New("correlação inválida")
	}
	return nil
}

func validToken(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func validCallback(value string) bool {
	if len(value) > 512 || strings.ContainsRune(value, 0) {
		return false
	}
	for _, r := range value {
		if r < 0x20 {
			return false
		}
	}
	return true
}
