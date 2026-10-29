package jobs

import (
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"
)

func TestBackoffJitterAndCap(t *testing.T) {
	origin := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	first := Backoff{
		now:  func() time.Time { return origin },
		rand: func(n int64) int64 { return 0 },
	}
	early := first.NextRetry(&rivertype.JobRow{})
	span := RetryBase / 5
	if early.Before(origin.Add(RetryBase-span)) || early.After(origin.Add(RetryBase-span+time.Millisecond)) {
		t.Fatalf("primeira retentativa = %s", early.Sub(origin))
	}

	last := Backoff{
		now:  func() time.Time { return origin },
		rand: func(n int64) int64 { return n - 1 },
	}
	capped := last.NextRetry(&rivertype.JobRow{Errors: make([]rivertype.AttemptError, 30)})
	capSpan := RetryCap / 5
	if capped.Before(origin.Add(RetryCap+capSpan-time.Millisecond)) || capped.After(origin.Add(RetryCap+capSpan+time.Millisecond)) {
		t.Fatalf("teto da retentativa = %s", capped.Sub(origin))
	}
}

func TestArgsStayMinimal(t *testing.T) {
	if err := (IngressArgs{V: SchemaVersion}).validate(); err == nil {
		t.Fatal("envelope sem id foi aceito")
	}
	if err := (InboundArgs{V: SchemaVersion, ItemID: mustID()}).validate(); err == nil {
		t.Fatal("job de domínio sem tenant foi aceito")
	}
	if err := (OutboundArgs{V: SchemaVersion, TenantID: mustID()}).validate(); err == nil {
		t.Fatal("envio sem tentativa foi aceito")
	}
	if err := (IngressArgs{V: SchemaVersion, DeliveryID: mustID()}).validate(); err != nil {
		t.Fatal(err)
	}
}
