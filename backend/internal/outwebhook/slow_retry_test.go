package outwebhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/platform/urlguard"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestSlowWebhookFailureRetriesUntilTerminal covers a receiver that answers
// slower than the first backoff. The River job must stay scheduled while the
// row is pending, then stop after success or the attempt budget. A later call
// does not post again.
func TestSlowWebhookFailureRetriesUntilTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant := uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant); err != nil {
		t.Fatal(err)
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("segredo-de-teste-webhook")
	var hits sync.Map
	var sigFailures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if readErr != nil {
			sigFailures.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := Verify(secret, body, r.Header.Get(HeaderSignature)); err != nil {
			sigFailures.Add(1)
		}
		n := 1
		if previous, ok := hits.Load(r.URL.Path); ok {
			n = previous.(int) + 1
		}
		hits.Store(r.URL.Path, n)
		switch r.URL.Path {
		case "/slow":
			time.Sleep(1500 * time.Millisecond)
			if n < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/timeout":
			if n == 1 {
				// Longer than the client timeout, and longer than the
				// one-second backoff of the first attempt.
				time.Sleep(8 * time.Second)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/dead":
			time.Sleep(1500 * time.Millisecond)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	poster := HTTPPoster{
		Policy: policy,
		Mode:   "test",
		Client: &http.Client{Timeout: 5 * time.Second},
	}
	var pinned atomic.Pointer[time.Time]
	deps := Deps{
		Store: store, Ring: ring, Policy: policy, Mode: "test", Poster: poster,
		Now: func() time.Time {
			if at := pinned.Load(); at != nil {
				return at.UTC()
			}
			return time.Now().UTC()
		},
	}
	insert := func(path string) (uuid.UUID, uuid.UUID) {
		t.Helper()
		sub := Subscription{
			ID: uuid.New(), TenantID: tenant, URL: server.URL + path,
			Secret: secret, Enabled: true,
			Events: map[string]bool{EventMessageCreated: true},
		}
		stored, err := SealSubscription(ring, sub)
		if err != nil {
			t.Fatal(err)
		}
		source := uuid.New()
		occurred := time.Now().UTC().Add(-time.Minute)
		if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if err := InsertSubscription(ctx, tx, stored); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at, enqueued_at)
				VALUES ($1,$2,$3,$3,$4,$4)`, tenant, EventMessageCreated, source, occurred)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return sub.ID, source
	}
	enableOnly := func(id uuid.UUID) {
		t.Helper()
		if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM webhook_subscriptions WHERE tenant_id=$1`, tenant)
			if err != nil {
				return err
			}
			defer rows.Close()
			var ids []uuid.UUID
			for rows.Next() {
				var item uuid.UUID
				if err := rows.Scan(&item); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, item)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, item := range ids {
				if err := SetEnabled(ctx, tx, tenant, item, item == id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	load := func(source uuid.UUID) (status string, attempt int, next time.Time) {
		t.Helper()
		if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT status, attempt_count, next_attempt_at
				FROM webhook_deliveries
				WHERE tenant_id=$1 AND source_id=$2`, tenant, source).Scan(&status, &attempt, &next)
		}); err != nil {
			t.Fatal(err)
		}
		return status, attempt, next
	}
	posts := func(path string) int {
		value, ok := hits.Load(path)
		if !ok {
			return 0
		}
		return value.(int)
	}
	drive := func(path string, source uuid.UUID, wantStatus string, wantPosts int) {
		t.Helper()
		pinned.Store(nil)
		sawRetry := false
		for step := 0; step < MaxAttempts+2; step++ {
			err := DeliverSource(ctx, deps, tenant, EventMessageCreated, source)
			status, attempt, nextAt := load(source)
			var retry ErrRetryAfter
			if errorsAsRetry(err, &retry) {
				if status != StatusPending {
					t.Fatalf("%s: retry com status %s", path, status)
				}
				if retry.After < time.Second {
					t.Fatalf("%s: espera %s", path, retry.After)
				}
				if !sawRetry && attempt != 1 {
					t.Fatalf("%s: primeira falha attempt=%d", path, attempt)
				}
				sawRetry = true
				due := nextAt.UTC().Add(time.Millisecond)
				pinned.Store(&due)
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if status == StatusPending {
				t.Fatalf("%s: job encerraria com pending attempt=%d next=%s posts=%d", path, attempt, nextAt.Format(time.RFC3339Nano), posts(path))
			}
			if status != wantStatus || posts(path) != wantPosts || !sawRetry {
				t.Fatalf("%s: status=%s posts=%d retry=%v want %s/%d", path, status, posts(path), sawRetry, wantStatus, wantPosts)
			}
			before := posts(path)
			if err = DeliverSource(ctx, deps, tenant, EventMessageCreated, source); err != nil {
				t.Fatalf("%s reenvio: %v", path, err)
			}
			if posts(path) != before {
				t.Fatalf("%s reenviou depois de %s: %d", path, status, posts(path))
			}
			if sigFailures.Load() != 0 {
				t.Fatalf("%s assinatura inválida", path)
			}
			return
		}
		t.Fatalf("%s não terminou", path)
	}

	slowID, slowSource := insert("/slow")
	enableOnly(slowID)
	drive("/slow", slowSource, StatusDelivered, 3)

	timeoutID, timeoutSource := insert("/timeout")
	enableOnly(timeoutID)
	drive("/timeout", timeoutSource, StatusDelivered, 2)

	deadID, deadSource := insert("/dead")
	enableOnly(deadID)
	drive("/dead", deadSource, StatusDead, MaxAttempts)
}
