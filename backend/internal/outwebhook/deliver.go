package outwebhook

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"chatisp/internal/platform/urlguard"
)

const (
	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusDead      = "dead"

	// MaxAttempts is the total number of tries, including the first.
	MaxAttempts = 5
)

// Poster sends the raw body. Implementations must not follow a redirect.
type Poster interface {
	Post(ctx context.Context, rawURL string, body []byte, header http.Header) (int, error)
}

// PosterFunc adapts a function to Poster.
type PosterFunc func(ctx context.Context, rawURL string, body []byte, header http.Header) (int, error)

// Post implements Poster.
func (f PosterFunc) Post(ctx context.Context, rawURL string, body []byte, header http.Header) (int, error) {
	return f(ctx, rawURL, body, header)
}

// Headers are attached to one attempt. The body bytes stay unchanged.
func Headers(secret []byte, d Delivery) (http.Header, error) {
	sig, err := Sign(secret, d.Body)
	if err != nil {
		return nil, err
	}
	h := make(http.Header)
	h.Set(HeaderSignature, sig)
	h.Set(HeaderEventID, d.ID.String())
	h.Set(HeaderEventType, d.Type)
	h.Set(HeaderAttempt, strconv.Itoa(d.Attempt+1))
	h.Set(HeaderReplay, replayValue)
	h.Set("Content-Type", "application/json")
	return h, nil
}

// Advance applies one response. A conflict means the receiver already stored the id.
// The delivery id is never replaced.
func Advance(d Delivery, status int, callErr error, now time.Time) Delivery {
	if d.Status != StatusPending {
		return d
	}
	d.Attempt++
	if status > 0 {
		d.LastStatus = status
	}
	if callErr == nil && status >= 200 && status < 300 {
		d.Status = StatusDelivered
		return d
	}
	if callErr == nil && status == http.StatusConflict {
		d.Status = StatusDelivered
		return d
	}
	retry := callErr != nil || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	if !retry || d.Attempt >= MaxAttempts {
		d.Status = StatusDead
		return d
	}
	d.Status = StatusPending
	d.NextAt = now.Add(backoff(d.Attempt))
	return d
}

func backoff(attempt int) time.Duration {
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 4 {
		shift = 4
	}
	delay := time.Second << shift
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

// Dispatch sends one due attempt. A delivery that is not due is returned as-is.
func Dispatch(ctx context.Context, poster Poster, secret []byte, rawURL string, d Delivery, now time.Time) (Delivery, error) {
	if d.Status != StatusPending {
		return d, nil
	}
	if now.Before(d.NextAt) {
		return d, nil
	}
	if poster == nil {
		return Advance(d, 0, ErrEvent, now), nil
	}
	header, err := Headers(secret, d)
	if err != nil {
		return d, err
	}
	status, err := poster.Post(ctx, rawURL, append([]byte(nil), d.Body...), header)
	return Advance(d, status, err, now), nil
}

// GuardedPoster refuses destinations that fail the outbound policy before calling next.
type GuardedPoster struct {
	Policy *urlguard.Policy
	Scope  urlguard.Scope
	Next   Poster
}

// Post inspects the URL and only then calls Next.
func (g GuardedPoster) Post(ctx context.Context, rawURL string, body []byte, header http.Header) (int, error) {
	if g.Policy == nil || g.Next == nil {
		return 0, urlguard.ErrConfig
	}
	if _, err := g.Policy.Inspect(g.Scope, rawURL); err != nil {
		return 0, err
	}
	return g.Next.Post(ctx, rawURL, body, header)
}
