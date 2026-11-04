package erp

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBreakerOpensAfterThreeFailuresAndReopensOnFailedProbe(t *testing.T) {
	b := NewBreaker()
	id := uuid.New()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	boom := errors.New("boom")
	calls := 0
	fail := func() error { calls++; return boom }

	for i := 0; i < breakerFailures; i++ {
		if err := b.Do(id, now, fail); !errors.Is(err, boom) {
			t.Fatalf("falha %d: %v", i, err)
		}
	}
	if err := b.Do(id, now.Add(time.Second), fail); !errors.Is(err, ErrOpen) || calls != breakerFailures {
		t.Fatalf("circuito aberto chamou o adaptador: %v calls=%d", err, calls)
	}
	probeAt := now.Add(breakerOpenFor)
	if err := b.Do(id, probeAt, fail); !errors.Is(err, boom) {
		t.Fatalf("sondagem: %v", err)
	}
	if err := b.Do(id, probeAt.Add(time.Second), fail); !errors.Is(err, ErrOpen) || calls != breakerFailures+1 {
		t.Fatalf("sondagem que falhou deveria reabrir: %v calls=%d", err, calls)
	}
	if err := b.Do(id, probeAt.Add(breakerOpenFor), func() error { return nil }); err != nil {
		t.Fatalf("nova sondagem com sucesso: %v", err)
	}
	if err := b.Do(id, probeAt.Add(breakerOpenFor), fail); !errors.Is(err, boom) {
		t.Fatalf("circuito deveria fechar: %v", err)
	}
}

func TestBreakerIsolatesConnections(t *testing.T) {
	b := NewBreaker()
	a, c := uuid.New(), uuid.New()
	now := time.Now()
	for i := 0; i < breakerFailures; i++ {
		_ = b.Do(a, now, func() error { return errors.New("x") })
	}
	if err := b.Do(c, now, func() error { return nil }); err != nil {
		t.Fatalf("outra conexão afetada: %v", err)
	}
}
