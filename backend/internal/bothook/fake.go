package bothook

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Delivery is the in-process event handed to the fake bot.
// Received means the hook accepted the event. It does not mean the conversation was resolved.
type Delivery struct {
	TenantID       uuid.UUID
	InboxID        uuid.UUID
	ConversationID uuid.UUID
	Epoch          int64
	DeadlineAt     time.Time
}

// FakeResult is the deterministic outcome of the in-process bot.
// Action is reply, handoff, uncertain, unavailable or empty.
type FakeResult struct {
	Received bool
	Action   string
	Body     string
	Reason   string
	Note     string
	SectorID uuid.UUID
}

// Fake handles one delivery inside this process. Implementations must not dial the network.
type Fake interface {
	Handle(context.Context, Delivery) (FakeResult, error)
}

// QuietFake accepts the event and does not resolve the conversation.
type QuietFake struct{}

// Handle returns a receipt without a resolution.
func (QuietFake) Handle(context.Context, Delivery) (FakeResult, error) {
	return FakeResult{Received: true}, nil
}
