package jobs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"chatisp/internal/platform/metrics"
)

func TestWorkHookKeepsTheRiverErrorAndClosesLabels(t *testing.T) {
	meter := metrics.New()
	hook := &workHook{metrics: meter}
	secretKind := "https://user:pass@evil.example/11111111-1111-4111-8111-111111111111"
	secretErr := errors.New("falha com texto secreto")
	if err := hook.WorkEnd(context.Background(), &rivertype.JobRow{Kind: secretKind}, secretErr); err != secretErr {
		t.Fatal("o hook alterou o erro do worker")
	}
	snooze := river.JobSnooze(time.Second)
	if err := hook.WorkEnd(context.Background(), &rivertype.JobRow{Kind: KindOutbound}, snooze); err != snooze {
		t.Fatal("o hook alterou o snooze")
	}
	cancel := river.JobCancel(errors.New("cancelado"))
	if err := hook.WorkEnd(context.Background(), &rivertype.JobRow{Kind: KindMedia}, cancel); err != cancel {
		t.Fatal("o hook alterou o cancelamento")
	}
	if err := hook.WorkEnd(context.Background(), &rivertype.JobRow{Kind: KindIngress}, nil); err != nil {
		t.Fatal(err)
	}
	if err := hook.WorkEnd(context.Background(), &rivertype.JobRow{Kind: "chatisp.automation.tick.v1"}, context.Canceled); err != context.Canceled {
		t.Fatal("o hook alterou o cancelamento de contexto")
	}

	body := gatherText(t, meter)
	for _, hidden := range []string{"evil.example", "11111111-1111-4111-8111-111111111111", "texto secreto", "pass"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("métrica expôs %s\n%s", hidden, body)
		}
	}
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="other"`, `result="error"`)
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="outbound"`, `result="snooze"`)
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="media"`, `result="cancelled"`)
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="ingress"`, `result="success"`)
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="automation"`, `result="cancelled"`)
	if _, ok := lineValue(body, "chatisp_river_job_results_total", `worker="outbound"`, `result="error"`); ok {
		t.Fatal("snooze contou como erro")
	}
}

func TestWorkHookCountsPanicOnceAndReraises(t *testing.T) {
	meter := metrics.New()
	hook := &workHook{metrics: meter}
	const secret = "segredo-panic-7f3a"
	panicked := false
	func() {
		defer func() {
			recovered := recover()
			if recovered != secret {
				t.Fatalf("panic = %v", recovered)
			}
			panicked = true
		}()
		_ = hook.Work(context.Background(), &rivertype.JobRow{Kind: KindInbound}, func(context.Context) error {
			panic(secret)
		})
	}()
	if !panicked {
		t.Fatal("o middleware engoliu o panic")
	}
	body := gatherText(t, meter)
	if strings.Contains(body, secret) {
		t.Fatalf("métrica expôs o panic:\n%s", body)
	}
	assertSample(t, body, "chatisp_river_job_results_total", 1, `worker="inbound"`, `result="error"`)
}

func TestInsertClientDoesNotPublishWorkerResults(t *testing.T) {
	meter := metrics.New()
	client, err := New(nil, Handlers{}, WithMetrics(meter))
	if err != nil {
		t.Fatal(err)
	}
	if client.works {
		t.Fatal("cliente sem pool não executa")
	}
	body := gatherText(t, meter)
	if strings.Contains(body, "chatisp_river_job_results_total") || strings.Contains(body, "chatisp_river_jobs") {
		t.Fatalf("cliente de inserção fabricou métrica de execução:\n%s", body)
	}
}

func TestNilJobsPoolDoesNotCollect(t *testing.T) {
	meter := metrics.New()
	CollectRiverBacklog(context.Background(), nil, meter)
	body := gatherText(t, meter)
	if strings.Contains(body, "chatisp_river_") {
		t.Fatalf("pool ausente fabricou backlog:\n%s", body)
	}
}

func TestRiverBacklogReadsTheJobsPool(t *testing.T) {
	ctx := context.Background()
	reader := mustPool(t, testJobsURL, RoleJobs)
	defer reader.Close()
	assertJobsRole(t, ctx, reader)

	meter := metrics.New()
	collectRiverOnce(ctx, reader, meter)
	before := gatherText(t, meter)
	okValue, ok := lineValue(before, "chatisp_river_collect_ok")
	if !ok || okValue != 1 {
		t.Fatalf("coleta real não concluiu: %v ok=%v\n%s", okValue, ok, before)
	}
	beforeJobs, _ := lineValue(before, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)

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
	jobID, err := producer.EnqueueInbound(ctx, tx, uuid.MustParse("22222222-2222-4222-8222-222222222222"), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, `UPDATE river.river_job SET created_at = clock_timestamp() - interval '2 minutes' WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}

	secretQueue := "https://user:pass@evil.example/33333333-3333-4333-8333-333333333333?token=abc"
	if _, err := reader.Exec(ctx, `
		INSERT INTO river.river_job (args, kind, max_attempts, metadata, queue, state)
		VALUES ('{"tenant_id":"22222222-2222-4222-8222-222222222222","body":"texto secreto"}', $1, 1, '{"v":1}', $2, 'available')
	`, KindIngress, secretQueue); err != nil {
		t.Fatal(err)
	}

	collectRiverOnce(ctx, reader, meter)
	body := gatherText(t, meter)
	for _, hidden := range []string{
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
		"texto secreto",
		"evil.example",
		"token",
		secretQueue,
	} {
		if strings.Contains(body, hidden) {
			t.Fatalf("coleta expôs %s\n%s", hidden, body)
		}
	}
	jobs, ok := lineValue(body, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)
	if !ok || jobs < beforeJobs+1 {
		t.Fatalf("backlog inbound não acompanhou a tabela: antes %v agora %v", beforeJobs, jobs)
	}
	age, ok := lineValue(body, "chatisp_river_oldest_job_age_seconds", `queue="inbound"`, `state="available"`)
	if !ok || age < 60 {
		t.Fatalf("idade do job mais antigo = %v ok=%v", age, ok)
	}
	other, ok := lineValue(body, "chatisp_river_jobs", `queue="other"`, `state="available"`)
	if !ok || other < 1 {
		t.Fatalf("fila desconhecida não colapsou: %v ok=%v\n%s", other, ok, body)
	}

	kept := jobs
	collectRiverOnce(ctx, errQueryer{}, meter)
	failed := gatherText(t, meter)
	okValue, _ = lineValue(failed, "chatisp_river_collect_ok")
	errorsValue, errorsOK := lineValue(failed, "chatisp_river_collect_errors_total")
	jobs, jobsOK := lineValue(failed, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)
	if okValue != 0 || !errorsOK || errorsValue < 1 || !jobsOK || jobs != kept {
		t.Fatalf("erro de coleta zerou o backlog ok=%v erros=%v jobs=%v antes=%v", okValue, errorsValue, jobs, kept)
	}
}

func TestRiverWorkersReportDistinctResults(t *testing.T) {
	ctx := context.Background()
	meter := metrics.New()
	other := metrics.New()
	tenantID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	secretText := "corpo-secreto-do-worker"
	workerPool := mustPool(t, testJobsURL, RoleJobs)
	defer workerPool.Close()
	worker, err := New(workerPool, Handlers{
		Ingress: ingressFn(func(context.Context, uuid.UUID) error { return nil }),
		Inbound: inboundFn(func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New(secretText)
		}),
		Outbound: outboundFn(func(context.Context, uuid.UUID, uuid.UUID) error { return nil }),
		Lease: leaseFn(func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		}),
		Media: mediaFn(func(context.Context, uuid.UUID, uuid.UUID) error {
			return river.JobCancel(errors.New(secretText))
		}),
		Webhook: webhookFn(func(context.Context, uuid.UUID, string, uuid.UUID) error {
			return RetryAfterError{After: time.Hour}
		}),
		Automation: automationFn(func(context.Context) error { return context.Canceled }),
	}, fast(), WithMetrics(meter))
	if err != nil {
		t.Fatal(err)
	}

	app := mustPool(t, testURLs.App, roleApp)
	defer app.Close()
	ingress := mustPool(t, testURLs.Ingress, roleIngress)
	defer ingress.Close()
	enqueue := func(poolName string, pool interface {
		Begin(context.Context) (pgx.Tx, error)
	}, fn func(pgx.Tx) error) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("%s: %s", poolName, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("ingresso", ingress, func(tx pgx.Tx) error {
		_, err := worker.EnqueueIngress(ctx, tx, uuid.New())
		return err
	})
	enqueue("app", app, func(tx pgx.Tx) error {
		if _, err := worker.EnqueueInbound(ctx, tx, tenantID, uuid.New()); err != nil {
			return err
		}
		if _, err := worker.EnqueueOutbound(ctx, tx, tenantID, uuid.New()); err != nil {
			return err
		}
		if _, err := worker.EnqueueMedia(ctx, tx, tenantID, uuid.New()); err != nil {
			return err
		}
		meta := &river.InsertOpts{Metadata: []byte(metadataVersion)}
		if _, err := worker.inner.InsertTx(ctx, tx, WebhookArgs{
			V: SchemaVersion, TenantID: tenantID, EventType: secretText, SourceID: uuid.New(),
		}, meta); err != nil {
			return err
		}
		_, err := worker.inner.InsertTx(ctx, tx, AutomationArgs{V: SchemaVersion}, meta)
		return err
	})

	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var body string
	for {
		body = gatherText(t, meter)
		if sampleAtLeast(body, "ingress", "success", 1) &&
			sampleAtLeast(body, "inbound", "error", 1) &&
			sampleAtLeast(body, "outbound", "snooze", 1) &&
			sampleAtLeast(body, "media", "cancelled", 1) &&
			sampleAtLeast(body, "webhooks", "snooze", 1) &&
			sampleAtLeast(body, "automation", "cancelled", 1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resultados ausentes:\n%s\n%s", riverLines(body), jobDump(t, ctx, workerPool))
		}
		time.Sleep(30 * time.Millisecond)
	}
	stopWithin(t, worker)

	body = gatherText(t, meter)
	if strings.Contains(body, tenantID.String()) || strings.Contains(body, secretText) {
		t.Fatalf("execução expôs identificador ou erro:\n%s", body)
	}
	if _, ok := lineValue(body, "chatisp_river_job_results_total", `worker="outbound"`, `result="error"`); ok {
		t.Fatal("lease indisponível contou como erro")
	}
	if _, ok := lineValue(body, "chatisp_river_job_results_total", `worker="webhooks"`, `result="error"`); ok {
		t.Fatal("snooze de webhook contou como erro")
	}
	otherBody := gatherText(t, other)
	if strings.Contains(otherBody, `worker="ingress"`) {
		t.Fatal("outro registry recebeu o resultado")
	}
}

func TestRiverPanicCountsAsErrorOnceAndStaysDiscarded(t *testing.T) {
	ctx := context.Background()
	meter := metrics.New()
	other := metrics.New()
	const secret = "segredo-panic-7f3a"
	deliveryID := uuid.New()
	workerPool := mustPool(t, testJobsURL, RoleJobs)
	defer workerPool.Close()
	worker, err := New(workerPool, Handlers{
		Ingress: ingressFn(func(_ context.Context, id uuid.UUID) error {
			if id == deliveryID {
				panic(secret)
			}
			return nil
		}),
	}, fast(), WithMetrics(meter))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := lineValue(gatherText(t, meter), "chatisp_river_job_results_total", `worker="ingress"`, `result="error"`)

	ingress := mustPool(t, testURLs.Ingress, roleIngress)
	defer ingress.Close()
	tx, err := ingress.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := worker.inner.InsertTx(ctx, tx, IngressArgs{V: SchemaVersion, DeliveryID: deliveryID}, &river.InsertOpts{
		MaxAttempts: 1,
		Metadata:    []byte(metadataVersion),
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if inserted == nil || inserted.Job == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("inserção sem job")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	jobID := inserted.Job.ID
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var body string
	var value float64
	for {
		body = gatherText(t, meter)
		state := jobState(t, ctx, workerPool, jobID)
		value, _ = lineValue(body, "chatisp_river_job_results_total", `worker="ingress"`, `result="error"`)
		if state == "discarded" && value == before+1 {
			break
		}
		if state == "completed" {
			t.Fatal("panic foi concluído como sucesso")
		}
		if time.Now().After(deadline) {
			t.Fatalf("panic não contabilizado state=%s valor=%v antes=%v\n%s\n%s", state, value, before, riverLines(body), jobDump(t, ctx, workerPool))
		}
		time.Sleep(30 * time.Millisecond)
	}
	var state string
	var attempt, maxAttempts int
	if err := workerPool.QueryRow(ctx, `SELECT state::text, attempt, max_attempts FROM river.river_job WHERE id = $1`, jobID).Scan(&state, &attempt, &maxAttempts); err != nil {
		t.Fatal(err)
	}
	if state != "discarded" || attempt != 1 || maxAttempts != 1 {
		t.Fatalf("River não manteve a falha do panic: %s attempt=%d max=%d", state, attempt, maxAttempts)
	}
	if strings.Contains(body, secret) || strings.Contains(body, deliveryID.String()) {
		t.Fatalf("execução expôs panic ou identificador:\n%s", body)
	}
	stopWithin(t, worker)
	value, _ = lineValue(gatherText(t, meter), "chatisp_river_job_results_total", `worker="ingress"`, `result="error"`)
	if value != before+1 {
		t.Fatalf("shutdown alterou a contagem: %v", value)
	}
	if strings.Contains(gatherText(t, other), `worker="ingress"`) {
		t.Fatal("outro registry recebeu o panic")
	}
}

func TestRiverRollbackFailureIsDistinctAndBounded(t *testing.T) {
	const leaked = "rollback-secreto-nao-publicar"
	meter := metrics.New()
	meter.SetRiverBacklog([]metrics.RiverSample{{
		Queue: "inbound", State: "available", Jobs: 4, OldestAgeSeconds: 12,
	}})
	collectRiverOnce(context.Background(), txQueryer{tx: &scriptTx{
		rollback: func(context.Context) error { return errors.New(leaked) },
	}}, meter)
	body := gatherText(t, meter)
	if strings.Contains(body, leaked) {
		t.Fatalf("erro de rollback entrou na métrica:\n%s", body)
	}
	okValue, _ := lineValue(body, "chatisp_river_collect_ok")
	errorsValue, errorsOK := lineValue(body, "chatisp_river_collect_errors_total")
	jobs, jobsOK := lineValue(body, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)
	if okValue != 0 || !errorsOK || errorsValue != 1 || !jobsOK || jobs != 4 {
		t.Fatalf("falha de rollback publicou leitura ok=%v erros=%v jobs=%v", okValue, errorsValue, jobs)
	}

	previous := riverCollectTimeout
	riverCollectTimeout = 40 * time.Millisecond
	t.Cleanup(func() { riverCollectTimeout = previous })
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		collectRiverOnce(context.Background(), txQueryer{tx: &scriptTx{
			rollback: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			},
		}}, meter)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("rollback bloqueado não começou")
	}
	start := time.Now()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rollback bloqueado não retornou no prazo")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("rollback levou %s", elapsed)
	}
	body = gatherText(t, meter)
	okValue, _ = lineValue(body, "chatisp_river_collect_ok")
	errorsValue, _ = lineValue(body, "chatisp_river_collect_errors_total")
	jobs, _ = lineValue(body, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)
	if okValue != 0 || errorsValue != 2 || jobs != 4 {
		t.Fatalf("prazo do rollback não marcou erro ok=%v erros=%v jobs=%v", okValue, errorsValue, jobs)
	}
}

func TestRiverRollbackCancelReturnsWithoutCollectError(t *testing.T) {
	meter := metrics.New()
	meter.SetRiverBacklog([]metrics.RiverSample{{
		Queue: "inbound", State: "available", Jobs: 4, OldestAgeSeconds: 12,
	}})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		collectRiverOnce(ctx, txQueryer{tx: &scriptTx{
			rollback: func(rb context.Context) error {
				close(started)
				<-rb.Done()
				return rb.Err()
			},
		}}, meter)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("rollback não começou")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelamento não liberou o coletor")
	}
	body := gatherText(t, meter)
	okValue, _ := lineValue(body, "chatisp_river_collect_ok")
	errorsValue, errorsOK := lineValue(body, "chatisp_river_collect_errors_total")
	jobs, _ := lineValue(body, "chatisp_river_jobs", `queue="inbound"`, `state="available"`)
	if okValue != 1 || !errorsOK || errorsValue != 0 || jobs != 4 {
		t.Fatalf("cancelamento alterou a coleta ok=%v erros=%v presentes=%v jobs=%v", okValue, errorsValue, errorsOK, jobs)
	}
}

type mediaFn func(context.Context, uuid.UUID, uuid.UUID) error

func (f mediaFn) DownloadInboundMedia(ctx context.Context, tenantID, messageID uuid.UUID) error {
	return f(ctx, tenantID, messageID)
}

type webhookFn func(context.Context, uuid.UUID, string, uuid.UUID) error

func (f webhookFn) DeliverWebhook(ctx context.Context, tenantID uuid.UUID, eventType string, sourceID uuid.UUID) error {
	return f(ctx, tenantID, eventType, sourceID)
}

type automationFn func(context.Context) error

func (f automationFn) RunAutomations(ctx context.Context) error { return f(ctx) }

type txQueryer struct {
	tx pgx.Tx
}

func (q txQueryer) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return q.tx, nil
}

type scriptTx struct {
	rollback func(context.Context) error
}

func (s *scriptTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("transação aninhada fora da coleta")
}

func (s *scriptTx) Commit(context.Context) error {
	return errors.New("leitura não faz commit")
}

func (s *scriptTx) Rollback(ctx context.Context) error {
	if s.rollback == nil {
		return nil
	}
	return s.rollback(ctx)
}

func (s *scriptTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("cópia fora da coleta")
}

func (s *scriptTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (s *scriptTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (s *scriptTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("prepare fora da coleta")
}

func (s *scriptTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (s *scriptTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return emptyRows{}, nil
}

func (s *scriptTx) QueryRow(context.Context, string, ...any) pgx.Row { return emptyRow{} }

func (s *scriptTx) Conn() *pgx.Conn { return nil }

type emptyRow struct{}

func (emptyRow) Scan(...any) error { return pgx.ErrNoRows }

type emptyRows struct{}

func (emptyRows) Close() {}

func (emptyRows) Err() error { return nil }

func (emptyRows) CommandTag() pgconn.CommandTag { return pgconn.NewCommandTag("SELECT 0") }

func (emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (emptyRows) Next() bool { return false }

func (emptyRows) Scan(...any) error { return errors.New("sem linha") }

func (emptyRows) Values() ([]any, error) { return nil, errors.New("sem linha") }

func (emptyRows) RawValues() [][]byte { return nil }

func (emptyRows) Conn() *pgx.Conn { return nil }

func (emptyRows) TypeMap() *pgtype.Map { return nil }

type errQueryer struct{}

func (errQueryer) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("leitura do backlog indisponível")
}

func assertJobsRole(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) {
	t.Helper()
	var name string
	var super, bypass bool
	if err := pool.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls
		  FROM pg_roles r
		 WHERE r.rolname = current_user
	`).Scan(&name, &super, &bypass); err != nil {
		t.Fatal(err)
	}
	if name != RoleJobs || super || bypass {
		t.Fatalf("coleta não está no papel de jobs: %s super=%v bypass=%v", name, super, bypass)
	}
}

func riverLines(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "chatisp_river_job_results") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func jobDump(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT queue, state::text, count(*) FROM river.river_job GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var queue, state string
		var count int
		if err := rows.Scan(&queue, &state, &count); err != nil {
			return err.Error()
		}
		lines = append(lines, fmt.Sprintf("%s %s %d", queue, state, count))
	}
	return strings.Join(lines, "\n")
}

func sampleAtLeast(body, worker, result string, min float64) bool {
	value, ok := lineValue(body, "chatisp_river_job_results_total", `worker="`+worker+`"`, `result="`+result+`"`)
	return ok && value >= min
}

func assertSample(t *testing.T, body, name string, want float64, parts ...string) {
	t.Helper()
	got, ok := lineValue(body, name, parts...)
	if !ok || got != want {
		t.Fatalf("%s %v = %v ok=%v\n%s", name, parts, got, ok, body)
	}
}

func gatherText(t *testing.T, meter *metrics.Metrics) string {
	t.Helper()
	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func lineValue(text, name string, parts ...string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !metricLine(line, name) {
			continue
		}
		match := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}

func metricLine(line, name string) bool {
	if !strings.HasPrefix(line, name) {
		return false
	}
	if len(line) == len(name) {
		return true
	}
	switch line[len(name)] {
	case ' ', '{':
		return true
	default:
		return false
	}
}
