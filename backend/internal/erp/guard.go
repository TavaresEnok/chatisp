package erp

import (
	"context"
	"errors"
	"time"
)

const (
	// DefaultTimeout bounds one adapter call so a slow ERP cannot hold the panel request open.
	DefaultTimeout = 2 * time.Second
	// DefaultInFlight is the number of adapter calls in flight for this process.
	DefaultInFlight = 8
)

// Gate limits how many adapter calls run at once.
type Gate struct {
	sem chan struct{}
}

// NewGate returns a concurrency gate. A non-positive limit uses DefaultInFlight.
func NewGate(limit int) *Gate {
	if limit < 1 {
		limit = DefaultInFlight
	}
	return &Gate{sem: make(chan struct{}, limit)}
}

type guarded struct {
	inner   Adapter
	gate    *Gate
	timeout time.Duration
}

func (g guarded) Call(ctx context.Context, in Consult) (View, error) {
	if g.inner == nil {
		return View{}, ErrDown
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}
	if g.gate != nil {
		select {
		case g.gate.sem <- struct{}{}:
			defer func() { <-g.gate.sem }()
		case <-ctx.Done():
			return View{}, timeoutOrDown(ctx.Err())
		}
	}
	view, err := g.inner.Call(ctx, in)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return View{}, timeoutOrDown(err)
	}
	return view, err
}

func timeoutOrDown(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	return ErrDown
}
