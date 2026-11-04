package metrics

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistriesStayIsolated(t *testing.T) {
	left := New()
	right := New()
	left.Observe("GET", "/healthz", 204, time.Millisecond)
	left.ObserveRiverResult("ingress", "success")

	leftBody := gatherText(t, left)
	rightBody := gatherText(t, right)
	if !strings.Contains(leftBody, `route="/healthz"`) || !strings.Contains(leftBody, `worker="ingress"`) {
		t.Fatalf("série ausente no registry de origem:\n%s", leftBody)
	}
	if strings.Contains(rightBody, `route="/healthz"`) || strings.Contains(rightBody, `worker="ingress"`) {
		t.Fatalf("registry alheio recebeu série:\n%s", rightBody)
	}
}

func TestNilMetricsAreOptional(t *testing.T) {
	var meter *Metrics
	meter.WebSocketOpened()
	meter.WebSocketClosed()
	meter.ObserveRiverResult("ingress", "success")
	meter.SetRiverBacklog(nil)
	meter.SetRiverCollectError()
	meter.RegisterPools(nil)
}

func TestPoolStatsComeFromTheOpenPool(t *testing.T) {
	pool := idlePool(t, 3)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err == nil {
		conn.Release()
		t.Fatal("pool sem servidor não deveria entregar conexão")
	}

	meter := New()
	meter.RegisterPools([]PoolBinding{
		{Role: "app", Pool: pool},
		{Role: "postgres://chatisp_app:segredo@127.0.0.1:1/chatisp", Pool: pool},
	})
	body := gatherText(t, meter)
	for _, hidden := range []string{"segredo", "127.0.0.1", "chatisp_app", "postgres://"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("métrica expôs %s\n%s", hidden, body)
		}
	}
	max, ok := lineValue(body, "chatisp_db_pool_max_connections", `role="app"`)
	if !ok || max != 3 {
		t.Fatalf("limite do pool app = %v ok=%v\n%s", max, ok, body)
	}
	acquired, ok := lineValue(body, "chatisp_db_pool_connections", `role="app"`, `state="acquired"`)
	if !ok || acquired != 0 {
		t.Fatalf("conexões adquiridas = %v ok=%v", acquired, ok)
	}
	otherMax, ok := lineValue(body, "chatisp_db_pool_max_connections", `role="other"`)
	if !ok || otherMax != 3 {
		t.Fatalf("papel desconhecido = %v ok=%v\n%s", otherMax, ok, body)
	}
}

func TestRiverCollectErrorIsNotAnEmptyBacklog(t *testing.T) {
	meter := New()
	meter.SetRiverCollectError()
	body := gatherText(t, meter)
	okValue, ok := lineValue(body, "chatisp_river_collect_ok")
	errors, errorsOK := lineValue(body, "chatisp_river_collect_errors_total")
	if !ok || okValue != 0 || !errorsOK || errors != 1 {
		t.Fatalf("sinal de falha ok=%v valor=%v erros=%v presente=%v\n%s", ok, okValue, errors, errorsOK, body)
	}
	if strings.Contains(body, "chatisp_river_jobs") || strings.Contains(body, "chatisp_river_oldest_job_age_seconds") {
		t.Fatalf("falha virou backlog zero:\n%s", body)
	}

	meter.SetRiverBacklog([]RiverSample{{
		Queue:            "https://tenant.example/11111111-1111-4111-8111-111111111111?token=abc",
		State:            "cpf",
		Jobs:             2,
		OldestAgeSeconds: 40,
	}, {
		Queue:            "ingress",
		State:            "available",
		Jobs:             1,
		OldestAgeSeconds: 15,
	}})
	body = gatherText(t, meter)
	for _, hidden := range []string{"11111111-1111-4111-8111-111111111111", "token", "tenant.example", "cpf"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("label sensível %s\n%s", hidden, body)
		}
	}
	jobs, ok := lineValue(body, "chatisp_river_jobs", `queue="other"`, `state="other"`)
	if !ok || jobs != 2 {
		t.Fatalf("fila desconhecida = %v ok=%v\n%s", jobs, ok, body)
	}
	age, ok := lineValue(body, "chatisp_river_oldest_job_age_seconds", `queue="ingress"`, `state="available"`)
	if !ok || age != 15 {
		t.Fatalf("idade = %v ok=%v", age, ok)
	}
	empty, ok := lineValue(body, "chatisp_river_jobs", `queue="webhooks"`, `state="running"`)
	if !ok || empty != 0 {
		t.Fatalf("fila vazia = %v ok=%v", empty, ok)
	}

	meter.SetRiverCollectError()
	body = gatherText(t, meter)
	okValue, _ = lineValue(body, "chatisp_river_collect_ok")
	errors, _ = lineValue(body, "chatisp_river_collect_errors_total")
	jobs, jobsOK := lineValue(body, "chatisp_river_jobs", `queue="ingress"`, `state="available"`)
	if okValue != 0 || errors != 2 || !jobsOK || jobs != 1 {
		t.Fatalf("falha seguinte alterou o snapshot ok=%v erros=%v jobs=%v presente=%v", okValue, errors, jobs, jobsOK)
	}
}

func TestJobResultLabelsAreClosed(t *testing.T) {
	meter := New()
	meter.ObserveRiverResult("ingress", "success")
	meter.ObserveRiverResult("media", "snooze")
	meter.ObserveRiverResult("cliente-5511999999999", "xz9-segredo-resultado")
	body := gatherText(t, meter)
	if strings.Contains(body, "5511999999999") || strings.Contains(body, "xz9-segredo-resultado") {
		t.Fatalf("resultado expôs detalhe:\n%s", body)
	}
	success, ok := lineValue(body, "chatisp_river_job_results_total", `worker="ingress"`, `result="success"`)
	snooze, snoozeOK := lineValue(body, "chatisp_river_job_results_total", `worker="media"`, `result="snooze"`)
	other, otherOK := lineValue(body, "chatisp_river_job_results_total", `worker="other"`, `result="other"`)
	if !ok || success != 1 || !snoozeOK || snooze != 1 || !otherOK || other != 1 {
		t.Fatalf("resultados success=%v snooze=%v other=%v", success, snooze, other)
	}
}

func idlePool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://chatisp_app@127.0.0.1:1/chatisp?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 200 * time.Millisecond
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func gatherText(t *testing.T, meter *Metrics) string {
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
