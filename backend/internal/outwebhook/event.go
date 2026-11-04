package outwebhook

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	EventConversationCreated  = "conversation.created"
	EventConversationAssigned = "conversation.assigned"
	EventConversationClosed   = "conversation.closed"
	EventMessageCreated       = "message.created"

	// ReplayGuidance is the instruction carried in every signed body.
	ReplayGuidance = "Aceite o event_id uma vez. Uma segunda entrega com o mesmo event_id é replay e não deve repetir o efeito."
)

// eventNamespace is the UUIDv5 namespace of outbound webhook ids.
var eventNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

var (
	ErrEvent    = errors.New("evento de webhook inválido")
	ErrDisabled = errors.New("webhook desligado")
	ErrMessage  = errors.New("mensagem local inválida")
)

// LocalMessage is the attendance record. Delivery failure must leave it intact.
type LocalMessage struct {
	ID   uuid.UUID
	Body string
}

// Envelope is the signed JSON body. Attempt count stays out of it.
type Envelope struct {
	EventID    string `json:"event_id"`
	Type       string `json:"type"`
	TenantID   string `json:"tenant_id"`
	SourceID   string `json:"source_id"`
	OccurredAt string `json:"occurred_at"`
	Replay     string `json:"replay"`
}

// Subscription is one outbound endpoint of a tenant.
type Subscription struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	URL      string
	Secret   []byte
	Events   map[string]bool
	Enabled  bool
}

// Delivery is one stable event waiting for, or finished with, the receiver.
type Delivery struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	SubscriptionID uuid.UUID
	Type           string
	SourceID       uuid.UUID
	Body           []byte
	Status         string
	Attempt        int
	NextAt         time.Time
	OccurredAt     time.Time
	LastStatus     int
}

// AcceptLocal records a message before any webhook attempt.
func AcceptLocal(body string) (LocalMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return LocalMessage{}, ErrMessage
	}
	return LocalMessage{ID: uuid.New(), Body: body}, nil
}

func knownEvent(name string) bool {
	switch name {
	case EventConversationCreated, EventConversationAssigned, EventConversationClosed, EventMessageCreated:
		return true
	default:
		return false
	}
}

// StableID is the same for every retry of one subscription, type and source.
func StableID(tenant, subscription uuid.UUID, eventType string, source uuid.UUID) (uuid.UUID, error) {
	if tenant == uuid.Nil || subscription == uuid.Nil || source == uuid.Nil || !knownEvent(eventType) {
		return uuid.Nil, ErrEvent
	}
	name := tenant.String() + "\n" + subscription.String() + "\n" + eventType + "\n" + source.String()
	return uuid.NewSHA1(eventNamespace, []byte(name)), nil
}

// Enqueue builds the immutable body for a source. The message text is not included.
func Enqueue(sub Subscription, eventType string, source uuid.UUID, occurred time.Time) (Delivery, error) {
	if !sub.Enabled {
		return Delivery{}, ErrDisabled
	}
	if !sub.Events[eventType] {
		return Delivery{}, ErrEvent
	}
	if occurred.IsZero() {
		return Delivery{}, ErrEvent
	}
	id, err := StableID(sub.TenantID, sub.ID, eventType, source)
	if err != nil {
		return Delivery{}, err
	}
	body, err := json.Marshal(Envelope{
		EventID:    id.String(),
		Type:       eventType,
		TenantID:   sub.TenantID.String(),
		SourceID:   source.String(),
		OccurredAt: occurred.UTC().Truncate(time.Second).Format(time.RFC3339),
		Replay:     ReplayGuidance,
	})
	if err != nil {
		return Delivery{}, ErrEvent
	}
	return Delivery{
		ID:             id,
		TenantID:       sub.TenantID,
		SubscriptionID: sub.ID,
		Type:           eventType,
		SourceID:       source,
		Body:           body,
		Status:         StatusPending,
		NextAt:         occurred,
		OccurredAt:     occurred.UTC().Truncate(time.Second),
	}, nil
}
