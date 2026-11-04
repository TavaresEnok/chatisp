package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/metrics"
)

func TestAPIWiringDoesNotInventRiverBacklog(t *testing.T) {
	meter := metrics.New()
	app := idlePool(t, "app", 4)
	defer app.Close()
	platform := idlePool(t, "platform", 2)
	defer platform.Close()
	ingress := idlePool(t, "ingress", 2)
	defer ingress.Close()
	collectors := bindProcessTelemetry(meter, &db.Pools{App: app, Platform: platform, Ingress: ingress}, nil)
	if collectors != nil {
		t.Fatalf("modo sem pool de jobs criou %d coletores", len(collectors))
	}
	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "chatisp_river_") {
		t.Fatalf("api publicou backlog global:\n%s", text)
	}
	for _, role := range []string{"app", "platform", "ingress"} {
		if _, ok := lineValue(text, "chatisp_db_pool_connections", `role="`+role+`"`); !ok {
			t.Fatalf("pool %s ausente\n%s", role, text)
		}
	}
	if strings.Contains(text, "127.0.0.1") || strings.Contains(text, "postgres://") {
		t.Fatalf("papel expôs destino:\n%s", text)
	}
}

func TestJobsPoolCollectorFailsClosedOnTheSameRegistry(t *testing.T) {
	meter := metrics.New()
	jobsPool := idlePool(t, "jobs", 4)
	defer jobsPool.Close()
	collectors := bindProcessTelemetry(meter, nil, jobsPool)
	if len(collectors) != 1 {
		t.Fatalf("coletores = %d", len(collectors))
	}
	max, ok := lineValue(gather(t, meter), "chatisp_db_pool_max_connections", `role="jobs"`)
	if !ok || max != 4 {
		t.Fatalf("pool de jobs = %v ok=%v", max, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	collectors[0](ctx)
	text := gather(t, meter)
	okValue, ok := lineValue(text, "chatisp_river_collect_ok")
	errors, errorsOK := lineValue(text, "chatisp_river_collect_errors_total")
	if !ok || okValue != 0 || !errorsOK || errors < 1 || strings.Contains(text, "chatisp_river_jobs") {
		t.Fatalf("falha de coleta indistinta de zero: ok=%v valor=%v erros=%v\n%s", ok, okValue, errors, text)
	}
}

func idlePool(t *testing.T, role string, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://" + role + "@127.0.0.1:1/chatisp?sslmode=disable")
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

func gather(t *testing.T, meter *metrics.Metrics) string {
	t.Helper()
	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func lineValue(text, name string, parts ...string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		if len(line) > len(name) && line[len(name)] != ' ' && line[len(name)] != '{' {
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
