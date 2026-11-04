package erp

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// Adapter is the only component allowed to perform an ERP capability.
type Adapter interface {
	Call(ctx context.Context, in Consult) (View, error)
}

// Service applies authorization, the circuit and local-key idempotency.
// Handlers keep using AcceptReply when this service reports the ERP unavailable.
type Service struct {
	Conn    Connection
	Adapter Adapter
	Breaker *Breaker

	mu      sync.Mutex
	actions map[string]Action
}

// New validates the single fake connection and returns a service.
func New(conn Connection, adapter Adapter) (*Service, error) {
	if adapter == nil || conn.ID == uuid.Nil || conn.TenantID == uuid.Nil {
		return nil, ErrInvalid
	}
	if conn.Driver != DriverFake || !conn.Simulated {
		return nil, ErrInvalid
	}
	if conn.Availability == "" {
		conn.Availability = AvailabilityUp
	}
	if conn.Availability != AvailabilityUp && conn.Availability != AvailabilityDown {
		return nil, ErrInvalid
	}
	if conn.Capabilities == nil {
		conn.Capabilities = map[string]bool{}
	}
	return &Service{
		Conn:    conn,
		Adapter: adapter,
		Breaker: NewBreaker(),
		actions: map[string]Action{},
	}, nil
}

// Restore remembers an action loaded from storage. A matching retry does not call the adapter.
func (s *Service) Restore(action Action) {
	if s == nil || action.LocalKey == "" {
		return
	}
	s.mu.Lock()
	s.actions[action.LocalKey] = action
	s.mu.Unlock()
}

// Lateral is the panel read. An ERP outage returns a view and a nil error.
func (s *Service) Lateral(ctx context.Context, p Principal, in Consult) (View, error) {
	action, err := s.Consult(ctx, p, in)
	if err != nil {
		return View{}, err
	}
	return action.View, nil
}

// Consult executes one capability or returns the stored result of its local key.
func (s *Service) Consult(ctx context.Context, p Principal, in Consult) (Action, error) {
	if s == nil || s.Adapter == nil {
		return Action{}, ErrInvalid
	}
	if !knownCapability(in.Capability) {
		return Action{}, ErrInvalid
	}
	if err := Authorize(p, s.Conn); err != nil {
		return Action{}, err
	}
	if err := requireKey(in); err != nil {
		return Action{}, err
	}
	if in.Capability == SearchCustomer {
		ref, err := NormalizeReference(in.Reference)
		if err != nil {
			return Action{}, err
		}
		in.Reference = ref
	}
	if in.Now.IsZero() {
		return Action{}, ErrInvalid
	}
	hash := fingerprint(in)
	if in.LocalKey != "" {
		if prior, ok := s.recall(in.LocalKey); ok {
			if prior.Hash != hash {
				return Action{}, ErrConflict
			}
			if prior.Outcome == OutcomeOK || prior.Outcome == OutcomeUncertain {
				return prior, nil
			}
		}
	}

	action := Action{
		ID:           uuid.New(),
		ConnectionID: s.Conn.ID,
		Conversation: in.Conversation,
		Capability:   in.Capability,
		LocalKey:     in.LocalKey,
		Hash:         hash,
		Effect:       effectOf(in.Capability),
	}
	view := View{
		Driver:      DriverFake,
		Simulated:   true,
		Capability:  in.Capability,
		Effect:      action.Effect,
		ConsultedAt: in.Now.UTC(),
	}
	finish := func(outcome, reason string) (Action, error) {
		view.Outcome = outcome
		view.Reason = reason
		view.Customer = nil
		view.Invoices = nil
		view.Document = nil
		view.PixCode = ""
		view.ExternalRef = ""
		action.Outcome = outcome
		action.View = view
		return action, nil
	}
	if !s.Conn.Enabled || s.Conn.Availability == AvailabilityDown {
		return finish(OutcomeUnavailable, ReasonDown)
	}
	if !s.Conn.Capabilities[in.Capability] {
		return finish(OutcomeUnavailable, ReasonUnavailable)
	}
	if sensitive(in.Capability) && in.Verification == "" {
		return finish(OutcomeHidden, ReasonHidden)
	}

	var called View
	err := s.Breaker.Do(s.Conn.ID, in.Now, func() error {
		var callErr error
		called, callErr = s.Adapter.Call(ctx, in)
		return callErr
	})
	if err != nil {
		if err == ErrOpen || err == ErrDown {
			return finish(OutcomeUnavailable, ReasonDown)
		}
		if err == ErrAmbiguous {
			action.Outcome = OutcomeUncertain
			view.Outcome = OutcomeUncertain
			view.Reason = ReasonUncertain
			action.View = view
			s.remember(action)
			return action, nil
		}
		return Action{}, err
	}
	if called.Driver != DriverFake || !called.Simulated {
		view.Driver = called.Driver
		view.Simulated = called.Simulated
		return finish(OutcomeUnavailable, ReasonNotSimulation)
	}
	switch called.Outcome {
	case OutcomeOK, OutcomeNotFound, OutcomeUnavailable:
	default:
		return finish(OutcomeUnavailable, ReasonUnavailable)
	}
	called.ConsultedAt = in.Now.UTC()
	called.Capability = in.Capability
	called.Driver = DriverFake
	called.Simulated = true
	if called.Effect == "" {
		called.Effect = action.Effect
	}
	action.Outcome = called.Outcome
	action.ExternalRef = called.ExternalRef
	action.View = called
	if called.Outcome == OutcomeOK {
		s.remember(action)
	}
	return action, nil
}

func (s *Service) recall(key string) (Action, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	action, ok := s.actions[key]
	return action, ok
}

func (s *Service) remember(action Action) {
	if action.LocalKey == "" {
		return
	}
	s.mu.Lock()
	s.actions[action.LocalKey] = action
	s.mu.Unlock()
}

// ChatContinues reports whether the attendance reply can proceed.
// Authorization and invalid input still stop the ERP action itself.
func ChatContinues(err error) bool {
	return err == nil || err == ErrDown || err == ErrOpen || err == ErrAmbiguous
}
