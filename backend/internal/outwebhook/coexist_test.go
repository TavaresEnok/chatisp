package outwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
)

func TestWebhookKindMatchesSharedClient(t *testing.T) {
	if JobKind != jobs.KindWebhook || QueueName != jobs.QueueWebhooks {
		t.Fatalf("fila ou tipo divergente: %s/%s e %s/%s", QueueName, JobKind, jobs.QueueWebhooks, jobs.KindWebhook)
	}
}

type tickAuto struct{ n atomic.Int64 }

func (a *tickAuto) RunAutomations(context.Context) error {
	a.n.Add(1)
	return nil
}

type okIngress struct{}

func (okIngress) ExpandIngress(context.Context, uuid.UUID) error { return nil }

type okInbound struct{}

func (okInbound) ApplyInbound(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type okOutbound struct{}

func (okOutbound) DispatchOutbound(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type okLease struct{}

func (okLease) AllowOutbound(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

type okHook struct{}

func (okHook) DeliverWebhook(context.Context, uuid.UUID, string, uuid.UUID) error { return nil }

// TestCoexistingClientsRescueBothKinds keeps two worker processes on one River
// schema. Each registers ingress, inbound, outbound, webhooks and the lifecycle
// schedule. The first leader must rescue a running job of each kind. After it
// stops, the other leader must rescue a new set and keep enqueueing lifecycle.
func TestCoexistingClientsRescueBothKinds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	poolA, poolB, query := openJobPeers(t, ctx)

	autoA, autoB := &tickAuto{}, &tickAuto{}
	clientA := startPeer(t, ctx, poolA, "jobs-a", autoA)
	clientB := startPeer(t, ctx, poolB, "jobs-b", autoB)
	stoppedA := false
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if !stoppedA {
			if err := clientA.Stop(stopCtx); err != nil {
				t.Error(err)
			}
		}
		if err := clientB.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()

	first := []int64{
		insertRunning(t, ctx, query, jobs.KindIngress, jobs.QueueIngress, jobs.IngressArgs{V: jobs.SchemaVersion, DeliveryID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindInbound, jobs.QueueInbound, jobs.InboundArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), ItemID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindOutbound, jobs.QueueOutbound, jobs.OutboundArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), AttemptID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindWebhook, jobs.QueueWebhooks, jobs.WebhookArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), EventType: EventMessageCreated, SourceID: uuid.New()}),
	}
	if err := clientA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitLeader(t, ctx, query, "jobs-a")
	waitCompleted(t, ctx, query, first)
	waitCount(t, &autoA.n, 1)

	if err := clientB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	second := []int64{
		insertRunning(t, ctx, query, jobs.KindIngress, jobs.QueueIngress, jobs.IngressArgs{V: jobs.SchemaVersion, DeliveryID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindInbound, jobs.QueueInbound, jobs.InboundArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), ItemID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindOutbound, jobs.QueueOutbound, jobs.OutboundArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), AttemptID: uuid.New()}),
		insertRunning(t, ctx, query, jobs.KindWebhook, jobs.QueueWebhooks, jobs.WebhookArgs{V: jobs.SchemaVersion, TenantID: uuid.New(), EventType: EventMessageCreated, SourceID: uuid.New()}),
	}
	for _, id := range second {
		if state := riverState(t, ctx, query, id); state != "running" {
			t.Fatalf("job %d já saiu de running antes da interrupção: %s", id, state)
		}
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := clientA.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()
	stoppedA = true

	waitLeader(t, ctx, query, "jobs-b")
	seen := autoB.n.Load()
	waitCompleted(t, ctx, query, second)
	waitCount(t, &autoB.n, seen+1)
}

func startPeer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string, auto jobs.AutomationHandler) *jobs.Client {
	t.Helper()
	client, err := jobs.New(pool, jobs.Handlers{
		Ingress:    okIngress{},
		Inbound:    okInbound{},
		Outbound:   okOutbound{},
		Lease:      okLease{},
		Automation: auto,
		Webhook:    okHook{},
	}, jobs.WithClientID(id), jobs.WithLogger(slog.New(slog.DiscardHandler)), jobs.WithTestTiming(15*time.Millisecond, jobs.Backoff{}, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func openJobPeers(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
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
	password, err := randomHex(24)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureJobsRole(ctx, env.AdminURL, password); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pools.Close)
	if err = jobs.Migrate(ctx, pools.Owner); err != nil {
		t.Fatal(err)
	}
	jobsURL, err := withUser(env.AdminURL, "chatisp_jobs", password)
	if err != nil {
		t.Fatal(err)
	}
	poolA, err := pgxpool.New(ctx, jobsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolA.Close)
	poolB, err := pgxpool.New(ctx, jobsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolB.Close)
	return poolA, poolB, poolA
}

func insertRunning(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind, queue string, args any) int64 {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = pool.QueryRow(ctx, `
		INSERT INTO river.river_job (args, kind, max_attempts, metadata, queue, state, attempted_at, attempted_by)
		VALUES ($1::jsonb, $2, 8, '{"v":1}'::jsonb, $3, 'running', now() - interval '3 hours', $4)
		RETURNING id`, raw, kind, queue, []string{"interrompido"}).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func riverState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(ctx, `SELECT state::text FROM river.river_job WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func waitCompleted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids []int64) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	states := make([]string, len(ids))
	for time.Now().Before(deadline) {
		done := true
		for i, id := range ids {
			states[i] = riverState(t, ctx, pool, id)
			switch states[i] {
			case "discarded", "cancelled":
				t.Fatalf("job %d ficou %s", id, states[i])
			case "completed":
			default:
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("jobs não concluíram: %v", states)
}

func waitLeader(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		err := pool.QueryRow(ctx, `SELECT leader_id FROM river.river_leader`).Scan(&got)
		if err == nil && got == id {
			return
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("líder = %s, esperado %s", got, id)
}

func waitCount(t *testing.T, n *atomic.Int64, min int64) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= min {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("lifecycle executou %d, esperado ao menos %d", n.Load(), min)
}
