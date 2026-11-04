package runtime

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/metrics"
)

func TestListenerServesTheProcessRegistry(t *testing.T) {
	meter := metrics.New()
	app := idlePool(t, "app", 3)
	defer app.Close()
	meter.RegisterPools([]metrics.PoolBinding{{Role: "app", Pool: app}})
	meter.WebSocketOpened()

	listener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var collectorStopped atomic.Bool
	var closedEarly atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeAPI,
			Config:          config.Config{ShutdownTimeout: time.Second},
			Logger:          logging.New("error", io.Discard),
			Metrics:         meter,
			HTTPListener:    listenLocal(t),
			MetricsListener: listener,
			Cleanup: func() {
				if !collectorStopped.Load() {
					closedEarly.Store(true)
				}
			},
			Collectors: []func(context.Context){
				func(collectCtx context.Context) {
					defer collectorStopped.Store(true)
					<-collectCtx.Done()
				},
			},
		})
	}()

	body := waitMetric(t, "http://"+listener.Addr().String()+"/metrics", func(text string) bool {
		max, ok := lineValue(text, "chatisp_db_pool_max_connections", `role="app"`)
		open, openOK := lineValue(text, "chatisp_websocket_connections")
		return ok && max == 3 && openOK && open == 1 && !strings.Contains(text, "chatisp_river_")
	})
	if strings.Contains(body, "127.0.0.1") {
		t.Fatalf("listener expôs endereço do pool:\n%s", body)
	}
	meter.WebSocketClosed()
	waitMetric(t, "http://"+listener.Addr().String()+"/metrics", func(text string) bool {
		open, ok := lineValue(text, "chatisp_websocket_connections")
		return ok && open == 0
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown não concluiu")
	}
	if !collectorStopped.Load() || closedEarly.Load() {
		t.Fatal("coletor não parou antes do cleanup")
	}
}

func TestWorkerListenerShowsRiverCollectFailure(t *testing.T) {
	meter := metrics.New()
	jobsPool := idlePool(t, "jobs", 2)
	defer jobsPool.Close()
	listener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var collectorStopped atomic.Bool
	var closedEarly atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeWorker,
			Config:          config.Config{ShutdownTimeout: time.Second},
			Logger:          logging.New("error", io.Discard),
			Metrics:         meter,
			MetricsListener: listener,
			Cleanup: func() {
				if !collectorStopped.Load() {
					closedEarly.Store(true)
				}
			},
			Collectors: []func(context.Context){
				func(collectCtx context.Context) {
					defer collectorStopped.Store(true)
					jobs.CollectRiverBacklog(collectCtx, jobsPool, meter)
				},
			},
		})
	}()

	body := waitMetric(t, "http://"+listener.Addr().String()+"/metrics", func(text string) bool {
		okValue, ok := lineValue(text, "chatisp_river_collect_ok")
		errors, errorsOK := lineValue(text, "chatisp_river_collect_errors_total")
		return ok && okValue == 0 && errorsOK && errors >= 1 && !strings.Contains(text, "chatisp_river_jobs")
	})
	if strings.Contains(body, "127.0.0.1") || strings.Contains(body, "postgres://") {
		t.Fatalf("falha de coleta expôs destino:\n%s", body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown não concluiu")
	}
	if !collectorStopped.Load() || closedEarly.Load() {
		t.Fatal("coleta de backlog ainda usava o pool no cleanup")
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

func waitMetric(t *testing.T, url string, ready func(string) bool) string {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		last = string(body)
		if response.StatusCode == http.StatusOK && ready(last) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("métrica não apareceu no listener:\n%s", last)
	return last
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
