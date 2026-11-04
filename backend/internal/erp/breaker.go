package erp

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	breakerFailures = 3
	breakerOpenFor  = 30 * time.Second
)

type gate struct {
	failures  int
	openUntil time.Time
	probing   bool
}

// Breaker isolates one ERP connection. An open circuit does not call the adapter.
type Breaker struct {
	mu sync.Mutex
	by map[uuid.UUID]*gate
}

// NewBreaker returns a closed breaker for every connection.
func NewBreaker() *Breaker {
	return &Breaker{by: map[uuid.UUID]*gate{}}
}

func (b *Breaker) gate(id uuid.UUID) *gate {
	g := b.by[id]
	if g == nil {
		g = &gate{}
		b.by[id] = g
	}
	return g
}

// Do runs fn when the connection is closed or due for a single probe.
func (b *Breaker) Do(id uuid.UUID, now time.Time, fn func() error) error {
	if b == nil || id == uuid.Nil || fn == nil {
		return ErrOpen
	}
	b.mu.Lock()
	g := b.gate(id)
	if !g.openUntil.IsZero() && now.Before(g.openUntil) {
		b.mu.Unlock()
		return ErrOpen
	}
	if g.probing {
		b.mu.Unlock()
		return ErrOpen
	}
	if !g.openUntil.IsZero() {
		g.probing = true
		g.openUntil = time.Time{}
	}
	b.mu.Unlock()

	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()
	g = b.gate(id)
	g.probing = false
	if err != nil {
		g.failures++
		if g.failures >= breakerFailures {
			g.openUntil = now.Add(breakerOpenFor)
			g.failures = 0
		}
		return err
	}
	g.failures = 0
	g.openUntil = time.Time{}
	return nil
}
