package jobs

import (
	"crypto/rand"
	"encoding/binary"
	"time"

	"github.com/riverqueue/river/rivertype"
)

// Backoff is the client retry schedule. The delay doubles from RetryBase and
// stops growing at RetryCap. Jitter is ±20%. The attempt budget is MaxAttempts.
type Backoff struct {
	now  func() time.Time
	rand func(n int64) int64
}

// NextRetry implements river.ClientRetryPolicy.
func (b Backoff) NextRetry(job *rivertype.JobRow) time.Time {
	now := time.Now
	if b.now != nil {
		now = b.now
	}
	roll := cryptoInt
	if b.rand != nil {
		roll = b.rand
	}
	failures := 1
	if job != nil {
		failures = len(job.Errors) + 1
	}
	if failures < 1 {
		failures = 1
	}
	shift := failures - 1
	if shift > 8 {
		shift = 8
	}
	delay := RetryBase << shift
	if delay <= 0 || delay > RetryCap {
		delay = RetryCap
	}
	span := delay / 5
	if span < time.Millisecond {
		span = time.Millisecond
	}
	// roll returns [0, 2*span]. The center of that range is span.
	width := int64(span)*2 + 1
	jitter := time.Duration(roll(width)) - span
	wait := delay + jitter
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return now().Add(wait)
}

func cryptoInt(n int64) int64 {
	if n <= 1 {
		return 0
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0
	}
	v := int64(binary.BigEndian.Uint64(buf[:]) & 0x7fffffffffffffff)
	return v % n
}
