package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/rivertype"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

var testAdmin string
var testURLs db.RoleURLs
var testJobsURL string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	jobsPassword, err := randomPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papel de jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := ensureJobsRole(ctx, env.AdminURL, jobsPassword); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao criar papel de jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	jobsURL, err := withUser(env.AdminURL, RoleJobs, jobsPassword)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao montar conexão de jobs: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	owner, err := openPool(ctx, urls.Owner, roleOwner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pool do dono: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	err = Migrate(ctx, owner)
	owner.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha na migração River: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	testAdmin = env.AdminURL
	testURLs = urls
	testJobsURL = jobsURL
	code := m.Run()
	env.Close()
	os.Exit(code)
}

func TestMigrateRejectsSuperuserAndRepeats(t *testing.T) {
	ctx := context.Background()
	admin, err := openPool(ctx, testAdmin, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := Migrate(ctx, admin); !errors.Is(err, errMigrateIdentity) {
		t.Fatalf("superusuário: %v", err)
	}
	owner, err := openPool(ctx, testURLs.Owner, roleOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	var version int
	var schema string
	if err := owner.QueryRow(ctx, `
		SELECT max(version), current_schema()
		FROM river.river_migration
	`).Scan(&version, &schema); err != nil {
		t.Fatal(err)
	}
	if version != MigrationVersion {
		t.Fatalf("versão River = %d", version)
	}
	var super, bypass bool
	if err := owner.QueryRow(ctx, `
		SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = $1
	`, RoleJobs).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatal("chatisp_jobs tem privilégio de superusuário ou BYPASSRLS")
	}
	var searchPath []string
	if err := owner.QueryRow(ctx, `
		SELECT proconfig
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'river' AND p.proname = 'chatisp_job_update_guard'
	`).Scan(&searchPath); err != nil {
		t.Fatal(err)
	}
	if len(searchPath) != 1 || searchPath[0] != "search_path=pg_catalog" {
		t.Fatalf("search_path da função = %v", searchPath)
	}
}

func TestQueuePrivileges(t *testing.T) {
	ctx := context.Background()
	owner, err := openPool(ctx, testURLs.Owner, roleOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	checks := []struct {
		role string
		sql  string
		want bool
	}{
		{roleApp, `SELECT has_table_privilege('chatisp_app', 'river.river_job', 'INSERT')`, true},
		{roleApp, `SELECT has_table_privilege('chatisp_app', 'river.river_job', 'DELETE')`, false},
		{roleApp, `SELECT has_table_privilege('chatisp_app', 'river.river_leader', 'SELECT')`, false},
		{roleIngress, `SELECT has_table_privilege('chatisp_ingress', 'river.river_migration', 'SELECT')`, false},
		{rolePlatform, `SELECT has_table_privilege('chatisp_platform', 'river.river_job', 'INSERT')`, false},
		{RoleJobs, `SELECT has_table_privilege('chatisp_jobs', 'river.river_job', 'UPDATE')`, true},
		{RoleJobs, `SELECT has_table_privilege('chatisp_jobs', 'river.river_migration', 'SELECT')`, false},
		{RoleJobs, `SELECT has_function_privilege('chatisp_jobs', 'river.chatisp_job_update_guard()', 'EXECUTE')`, true},
		{roleApp, `SELECT has_function_privilege('chatisp_app', 'river.chatisp_job_update_guard()', 'EXECUTE')`, false},
	}
	for _, check := range checks {
		var got bool
		if err := owner.QueryRow(ctx, check.sql).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s: %s = %v", check.role, check.sql, got)
		}
	}
	app, err := openPool(ctx, testURLs.App, roleApp)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	producer, err := New(nil, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := producer.EnqueueInbound(ctx, tx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = app.Exec(ctx, `UPDATE river.river_job SET priority = priority WHERE id = $1`, jobID)
	if !isPrivilege(err) {
		t.Fatalf("app atualizou job: %v", err)
	}
	_, err = app.Exec(ctx, `DELETE FROM river.river_job`)
	if !isPrivilege(err) {
		t.Fatalf("app apagou job: %v", err)
	}
}

func TestRollbackDoesNotPublishAndCommitDoes(t *testing.T) {
	ctx := context.Background()
	app := mustPool(t, testURLs.App, roleApp)
	defer app.Close()
	ingress := mustPool(t, testURLs.Ingress, roleIngress)
	defer ingress.Close()
	platform := mustPool(t, testURLs.Platform, rolePlatform)
	defer platform.Close()
	reader := mustPool(t, testJobsURL, RoleJobs)
	defer reader.Close()
	producer, err := New(nil, Handlers{})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := producer.EnqueueInbound(ctx, tx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if n := countJob(t, ctx, reader, hidden); n != 0 {
		t.Fatalf("job visível antes do commit: %d", n)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countJob(t, ctx, reader, hidden); n != 0 {
		t.Fatalf("rollback publicou job: %d", n)
	}

	tx, err = ingress.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	delivery := uuid.New()
	visible, err := producer.EnqueueIngress(ctx, tx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var args, queue string
	var attempts int
	if err := reader.QueryRow(ctx, `
		SELECT args::text, queue, max_attempts
		FROM river.river_job WHERE id = $1
	`, visible).Scan(&args, &queue, &attempts); err != nil {
		t.Fatal(err)
	}
	if queue != QueueIngress || attempts != MaxAttempts || containsTenant(args) {
		t.Fatalf("job de ingresso = fila %s tentativas %d args %s", queue, attempts, args)
	}

	tx, err = platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.EnqueueInbound(ctx, tx, uuid.New(), uuid.New()); err == nil {
		t.Fatal("plataforma enfileirou job")
	}
	_ = tx.Rollback(ctx)

	if _, err := producer.EnqueueInbound(ctx, nil, uuid.New(), uuid.New()); !errors.Is(err, errInvalidArgs) && !errors.Is(err, errTxRequired) {
		// nil tx is rejected before a database call
		if !errors.Is(err, errTxRequired) {
			t.Fatal(err)
		}
	}
}

func TestPendingJobSurvivesRestartAndShutdown(t *testing.T) {
	ctx := context.Background()
	app := mustPool(t, testURLs.App, roleApp)
	producer, err := New(nil, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.New()
	itemID := uuid.New()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := producer.EnqueueInbound(ctx, tx, tenantID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	app.Close()

	var gotItem uuid.UUID
	var mu sync.Mutex
	workerPool := mustPool(t, testJobsURL, RoleJobs)
	worker, err := New(workerPool, Handlers{
		Inbound: inboundFn(func(_ context.Context, gotTenant, got uuid.UUID) error {
			if gotTenant != tenantID {
				return errors.New("tenant divergente")
			}
			mu.Lock()
			gotItem = got
			mu.Unlock()
			return nil
		}),
	}, fast())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gotItem == itemID
	})
	stopWithin(t, worker)
	if state := jobState(t, ctx, workerPool, jobID); state != "completed" {
		t.Fatalf("estado após restart = %s", state)
	}
	workerPool.Close()

	started := make(chan struct{}, 1)
	blockPool := mustPool(t, testJobsURL, RoleJobs)
	blocker, err := New(blockPool, Handlers{
		Ingress: ingressFn(func(ctx context.Context, _ uuid.UUID) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}),
	}, fast())
	if err != nil {
		t.Fatal(err)
	}
	if err := blocker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	insertPool := mustPool(t, testURLs.Ingress, roleIngress)
	inserter, err := New(nil, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err = insertPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stoppedID, err := inserter.EnqueueIngress(ctx, tx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	insertPool.Close()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("job não chegou a executar antes do shutdown")
	}
	stopWithin(t, blocker)
	var state string
	waitUntil(t, 10*time.Second, func() bool {
		state = jobState(t, ctx, blockPool, stoppedID)
		return state != "running"
	})
	if state == "completed" || state == "discarded" || state == "cancelled" {
		t.Fatalf("shutdown encerrou o job em %s", state)
	}
	blockPool.Close()

	done := make(chan struct{}, 1)
	againPool := mustPool(t, testJobsURL, RoleJobs)
	defer againPool.Close()
	again, err := New(againPool, Handlers{
		Ingress: ingressFn(func(context.Context, uuid.UUID) error {
			done <- struct{}{}
			return nil
		}),
	}, fast())
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("job interrompido não foi reprocessado")
	}
	stopWithin(t, again)
	if state := jobState(t, ctx, againPool, stoppedID); state != "completed" {
		t.Fatalf("estado após novo worker = %s", state)
	}
}

func TestOutboundLeaseAndDiscardedInspection(t *testing.T) {
	ctx := context.Background()
	var leaseCalls int
	var mu sync.Mutex
	ran := make(chan struct{}, 1)
	pool := mustPool(t, testJobsURL, RoleJobs)
	defer pool.Close()
	worker, err := New(pool, Handlers{
		Lease: leaseFn(func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			mu.Lock()
			leaseCalls++
			allow := leaseCalls > 1
			mu.Unlock()
			return allow, nil
		}),
		Outbound: outboundFn(func(context.Context, uuid.UUID, uuid.UUID) error {
			ran <- struct{}{}
			return nil
		}),
		Inbound: inboundFn(func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New("falha controlada")
		}),
	}, fast())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	app := mustPool(t, testURLs.App, roleApp)
	defer app.Close()
	producer, err := New(nil, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outboundID, err := producer.EnqueueOutbound(ctx, tx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	failedID, err := producer.EnqueueInbound(ctx, tx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(20 * time.Second):
		t.Fatal("despacho não esperou a reserva")
	}
	mu.Lock()
	calls := leaseCalls
	mu.Unlock()
	if calls < 2 {
		t.Fatalf("reserva consultada %d vezes", calls)
	}
	waitUntil(t, 30*time.Second, func() bool {
		return jobState(t, ctx, pool, failedID) == "discarded"
	})
	stopWithin(t, worker)
	found, err := worker.Exhausted(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, job := range found {
		if job.ID != failedID {
			continue
		}
		seen = true
		if job.State != string(rivertype.JobStateDiscarded) || job.Queue != QueueInbound || job.LastError != "falha controlada" {
			t.Fatalf("job esgotado = %+v", job)
		}
	}
	if !seen {
		t.Fatal("job esgotado não apareceu na inspeção")
	}
	if jobState(t, ctx, pool, outboundID) != "completed" {
		t.Fatalf("envio não concluiu: %s", jobState(t, ctx, pool, outboundID))
	}
}

type ingressFn func(context.Context, uuid.UUID) error

func (f ingressFn) ExpandIngress(ctx context.Context, deliveryID uuid.UUID) error {
	return f(ctx, deliveryID)
}

type inboundFn func(context.Context, uuid.UUID, uuid.UUID) error

func (f inboundFn) ApplyInbound(ctx context.Context, tenantID, itemID uuid.UUID) error {
	return f(ctx, tenantID, itemID)
}

type outboundFn func(context.Context, uuid.UUID, uuid.UUID) error

func (f outboundFn) DispatchOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) error {
	return f(ctx, tenantID, attemptID)
}

type leaseFn func(context.Context, uuid.UUID, uuid.UUID) (bool, error)

func (f leaseFn) AllowOutbound(ctx context.Context, tenantID, attemptID uuid.UUID) (bool, error) {
	return f(ctx, tenantID, attemptID)
}

type nowRetry struct{}

func (nowRetry) NextRetry(*rivertype.JobRow) time.Time {
	return time.Now().Add(time.Millisecond)
}

func fast() Option {
	return WithTestTiming(20*time.Millisecond, nowRetry{}, 30*time.Millisecond)
}

func mustID() uuid.UUID {
	return uuid.MustParse("11111111-1111-4111-8111-111111111111")
}

func mustPool(t *testing.T, raw, name string) *pgxpool.Pool {
	t.Helper()
	pool, err := openPool(context.Background(), raw, name)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func openPool(ctx context.Context, raw, name string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	return pgxpool.NewWithConfig(ctx, cfg)
}

func countJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func jobState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var state string
	err := pool.QueryRow(ctx, `SELECT state::text FROM river.river_job WHERE id = $1`, id).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func waitUntil(t *testing.T, limit time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("tempo esgotado")
}

func stopWithin(t *testing.T, client *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func containsTenant(raw string) bool {
	return len(raw) >= 9 && (stringContains(raw, "tenant_id") || stringContains(raw, "texto"))
}

func stringContains(raw, part string) bool {
	return len(part) > 0 && len(raw) >= len(part) && (func() bool {
		for i := 0; i+len(part) <= len(raw); i++ {
			if raw[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})()
}

func isPrivilege(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func ensureJobsRole(ctx context.Context, adminURL, password string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'chatisp_jobs')`).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := conn.QueryRow(ctx, `
		SELECT format('%s ROLE chatisp_jobs LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L', $1::text, $2::text)
	`, verb, password).Scan(&stmt); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return err
	}
	for _, query := range []string{
		`SELECT format('GRANT CONNECT, CREATE ON DATABASE %I TO chatisp_owner', current_database())`,
		`SELECT format('GRANT CONNECT ON DATABASE %I TO chatisp_jobs', current_database())`,
	} {
		var grant string
		if err := conn.QueryRow(ctx, query).Scan(&grant); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, grant); err != nil {
			return err
		}
	}
	return nil
}

func withUser(raw, user, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String(), nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
