package publicapi

import (
	"sync"
	"time"
)

const (
	windowSeconds     = 60
	tenantWindowLimit = 600
)

type bucket struct {
	window int64
	count  int
}

// limiter counts requests in a fixed one-minute window. It is per process.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
}

func newLimiter() *limiter {
	return &limiter{buckets: map[string]bucket{}}
}

// allow reports whether the key is still inside limit. retry is the seconds
// until the current window ends when the call is refused.
func (l *limiter) allow(key string, limit int, now time.Time) (bool, int) {
	if limit < 1 {
		return false, windowSeconds
	}
	slot := now.UTC().Unix() / windowSeconds
	retry := windowSeconds - int(now.UTC().Unix()%windowSeconds)
	if retry < 1 {
		retry = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.buckets[key]
	if current.window != slot {
		current = bucket{window: slot}
	}
	if current.count >= limit {
		l.buckets[key] = current
		return false, retry
	}
	current.count++
	l.buckets[key] = current
	return true, 0
}
