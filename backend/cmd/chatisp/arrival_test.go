package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/distribution"
	"chatisp/internal/platform/httpapi"
)

type inboundStub struct {
	calls int
	err   error
}

func (s *inboundStub) ApplyInbound(context.Context, uuid.UUID, uuid.UUID) error {
	s.calls++
	return s.err
}

func TestArrivalStopsBeforeDistributionWhenInboundFails(t *testing.T) {
	stub := &inboundStub{err: errors.New("inbound recusou")}
	var logs bytes.Buffer
	worker := arrivalInbound{next: stub, log: slog.New(slog.NewTextHandler(&logs, nil))}
	err := worker.ApplyInbound(context.Background(), uuid.New(), uuid.New())
	if err == nil || stub.calls != 1 || errors.Is(err, httpapi.ErrArrivalAssign) {
		t.Fatalf("err=%v calls=%d", err, stub.calls)
	}
	if logs.Len() != 0 {
		t.Fatalf("falha de inbound não é log de distribuição: %s", logs.String())
	}
}

func TestArrivalReportsDistributionAfterInbound(t *testing.T) {
	stub := &inboundStub{}
	var logs bytes.Buffer
	worker := arrivalInbound{
		next: stub,
		log:  slog.New(slog.NewTextHandler(&logs, nil)),
		now:  func() time.Time { return time.Now().UTC() },
	}
	err := worker.ApplyInbound(context.Background(), uuid.New(), uuid.New())
	if stub.calls != 1 || !errors.Is(err, httpapi.ErrArrivalAssign) || !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("err=%v calls=%d", err, stub.calls)
	}
	if logs.Len() == 0 || bytes.Contains(logs.Bytes(), []byte("postgres")) {
		t.Fatalf("log: %s", logs.String())
	}
}
