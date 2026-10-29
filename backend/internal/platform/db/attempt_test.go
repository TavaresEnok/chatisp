package db_test

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"chatisp/internal/platform/db"
)

func TestConcurrentFirstFailuresAreCounted(t *testing.T) {
	const (
		n      = 4
		rounds = 8
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	window := 15 * time.Minute

	for round := range rounds {
		subject := randomSubject(t)
		failures := make([]int, n)
		errs := make([]error, n)
		var start sync.WaitGroup
		start.Add(1)
		var ready sync.WaitGroup
		ready.Add(n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func() {
				defer wg.Done()
				ready.Done()
				start.Wait()
				errs[i] = testStore.WithAuth(ctx, func(ctx context.Context) error {
					attempt, err := testStore.BumpFailure(ctx, "login_account", subject, now, window)
					if err != nil {
						return err
					}
					if attempt.AlreadyLocked {
						return errors.New("contador nasceu bloqueado")
					}
					failures[i] = attempt.Failures
					return nil
				})
			}()
		}
		ready.Wait()
		start.Done()
		wg.Wait()
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("rodada %d tentativa %d: %v", round, i, errs[i])
			}
		}
		sort.Ints(failures)
		for i := range n {
			if failures[i] != i+1 {
				t.Fatalf("rodada %d contagens: %v", round, failures)
			}
		}
	}
}

func TestBumpFailureWindowAndLock(t *testing.T) {
	subject := randomSubject(t)
	now := time.Date(2026, 4, 2, 15, 0, 0, 0, time.UTC)
	window := 15 * time.Minute
	ctx := context.Background()

	first := bumpFailure(t, ctx, subject, now, window)
	if first.Failures != 1 || first.AlreadyLocked || !first.LockedUntil.IsZero() {
		t.Fatalf("primeira: %+v", first)
	}
	second := bumpFailure(t, ctx, subject, now.Add(time.Minute), window)
	if second.Failures != 2 || second.AlreadyLocked {
		t.Fatalf("segunda: %+v", second)
	}
	resetAt := now.Add(window + time.Second)
	reset := bumpFailure(t, ctx, subject, resetAt, window)
	if reset.Failures != 1 || reset.AlreadyLocked || !reset.LockedUntil.IsZero() {
		t.Fatalf("janela: %+v", reset)
	}

	until := resetAt.Add(time.Hour)
	err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		return testStore.SetLock(ctx, "login_account", subject, until)
	})
	if err != nil {
		t.Fatal(err)
	}
	locked := bumpFailure(t, ctx, subject, resetAt.Add(time.Second), window)
	if !locked.AlreadyLocked || locked.Failures != 1 || !locked.LockedUntil.Equal(until) {
		t.Fatalf("bloqueio: %+v", locked)
	}
}

func bumpFailure(t *testing.T, ctx context.Context, subject []byte, now time.Time, window time.Duration) db.Attempt {
	t.Helper()
	var attempt db.Attempt
	err := testStore.WithAuth(ctx, func(ctx context.Context) error {
		got, err := testStore.BumpFailure(ctx, "login_account", subject, now, window)
		if err != nil {
			return err
		}
		attempt = got
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func randomSubject(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}
