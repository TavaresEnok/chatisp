package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/logging"
)

func TestAPIGracefulShutdown(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode: config.ModeAPI,
			Config: config.Config{
				ShutdownTimeout: 2 * time.Second,
				DatabaseURL:     "postgres://user:super-secret-value@127.0.0.1/chatisp",
			},
			Logger:          logging.New("info", &logs),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
		})
	}()

	client := &http.Client{Timeout: time.Second}
	base := "http://" + httpListener.Addr().String()
	waitStatus(t, client, base+"/healthz", http.StatusOK)
	waitStatus(t, client, base+"/readyz", http.StatusOK)

	metricsURL := "http://" + metricsListener.Addr().String() + "/metrics"
	response, err := client.Get(metricsURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("chatisp_build_info")) {
		t.Fatalf("métricas status %d corpo %s", response.StatusCode, body)
	}

	apiMetrics, err := client.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	apiBody, _ := io.ReadAll(apiMetrics.Body)
	apiMetrics.Body.Close()
	if apiMetrics.StatusCode != http.StatusNotFound {
		t.Fatalf("métricas na API: %d", apiMetrics.StatusCode)
	}
	if !strings.Contains(apiMetrics.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("content-type %s", apiMetrics.Header.Get("Content-Type"))
	}
	if bytes.Contains(apiBody, []byte("chatisp_build_info")) {
		t.Fatal("a API expôs Prometheus")
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
	if strings.Contains(logs.String(), "super-secret-value") || strings.Contains(logs.String(), "postgres://") {
		t.Fatalf("log expôs segredo: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "encerramento iniciado") {
		t.Fatalf("log sem encerramento: %s", logs.String())
	}
}

func TestWorkerStopsOnCancel(t *testing.T) {
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeWorker,
			Config:          config.Config{ShutdownTimeout: time.Second},
			Logger:          logging.New("info", io.Discard),
			MetricsListener: metricsListener,
		})
	}()
	client := &http.Client{Timeout: time.Second}
	waitStatus(t, client, "http://"+metricsListener.Addr().String()+"/metrics", http.StatusOK)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker não encerrou")
	}
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func waitStatus(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == want {
				return
			}
			last = errStatus(response.StatusCode)
		} else {
			last = err
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("sem status %d em %s: %v", want, url, last)
}

type errStatus int

func (e errStatus) Error() string { return http.StatusText(int(e)) }

type mockWorker struct {
	started atomic.Bool
	stopped atomic.Bool
}

type failingStopWorker struct{ wait bool }

type activeWorker struct {
	stopped   chan struct{}
	cancelled chan struct{}
}

func (m *activeWorker) Start(context.Context) error    { return nil }
func (m *activeWorker) Stop(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (m *activeWorker) StopAndCancel(ctx context.Context) error {
	close(m.cancelled)
	<-ctx.Done()
	return ctx.Err()
}
func (m *activeWorker) Stopped() <-chan struct{} { return m.stopped }

func TestTimeoutKeepsPoolsUntilActiveWorkerActuallyStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &activeWorker{stopped: make(chan struct{}), cancelled: make(chan struct{})}
	cleaned := make(chan struct{})
	listener := listenLocal(t)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Mode: config.ModeWorker, Config: config.Config{ShutdownTimeout: 100 * time.Millisecond}, Logger: logging.New("info", io.Discard), MetricsListener: listener, JobsClient: worker, Cleanup: func() { close(cleaned) }})
	}()
	waitStatus(t, &http.Client{Timeout: time.Second}, "http://"+listener.Addr().String()+"/metrics", http.StatusOK)
	cancel()
	<-worker.cancelled
	time.Sleep(70 * time.Millisecond) // force timeout has expired; final confirmation window is active
	select {
	case <-cleaned:
		t.Fatal("pool fechado com trabalho ativo")
	case <-done:
		t.Fatal("Run retornou antes da confirmação")
	default:
	}
	close(worker.stopped)
	if err := <-done; err == nil {
		t.Fatal("timeout deve retornar erro")
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("pool não fechado após parada")
	}
}

func (m failingStopWorker) Start(context.Context) error { return nil }
func (m failingStopWorker) Stop(ctx context.Context) error {
	if m.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("stop failed")
}
func (m failingStopWorker) StopAndCancel(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (m failingStopWorker) Stopped() <-chan struct{}                { return make(chan struct{}) }

func TestWorkerStopFailureIsReportedWithoutClosingPools(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprint(wait), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var cleaned atomic.Bool
			done := make(chan error, 1)
			listener := listenLocal(t)
			go func() {
				done <- Run(ctx, Options{Mode: config.ModeWorker, Config: config.Config{ShutdownTimeout: 100 * time.Millisecond}, Logger: logging.New("info", io.Discard), MetricsListener: listener, JobsClient: failingStopWorker{wait: wait}, Cleanup: func() { cleaned.Store(true) }})
			}()
			waitStatus(t, &http.Client{Timeout: time.Second}, "http://"+listener.Addr().String()+"/metrics", http.StatusOK)
			cancel()
			select {
			case err := <-done:
				if err == nil || cleaned.Load() {
					t.Fatalf("err=%v cleanup=%v", err, cleaned.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("shutdown excedeu orçamento")
			}
		})
	}
}

func (m *mockWorker) Start(ctx context.Context) error {
	m.started.Store(true)
	return nil
}

func (m *mockWorker) Stop(ctx context.Context) error {
	m.stopped.Store(true)
	return nil
}
func (m *mockWorker) StopAndCancel(ctx context.Context) error { return m.Stop(ctx) }
func (m *mockWorker) Stopped() <-chan struct{} {
	ch := make(chan struct{})
	if m.stopped.Load() {
		close(ch)
	}
	return ch
}

func TestAllGracefulShutdown(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs bytes.Buffer
	var cleanedUp atomic.Bool
	worker := &mockWorker{}

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode: config.ModeAll,
			Config: config.Config{
				ShutdownTimeout: 2 * time.Second,
			},
			Logger:          logging.New("info", &logs),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
			JobsClient:      worker,
			Cleanup: func() {
				cleanedUp.Store(true)
			},
		})
	}()

	client := &http.Client{Timeout: time.Second}
	base := "http://" + httpListener.Addr().String()
	waitStatus(t, client, base+"/healthz", http.StatusOK)
	waitStatus(t, client, base+"/readyz", http.StatusOK)

	metricsURL := "http://" + metricsListener.Addr().String() + "/metrics"
	waitStatus(t, client, metricsURL, http.StatusOK)

	if !worker.started.Load() {
		t.Fatal("worker não foi iniciado")
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

	if !worker.stopped.Load() {
		t.Fatal("worker não foi encerrado")
	}
	if !cleanedUp.Load() {
		t.Fatal("cleanup não foi executado")
	}
}

func TestReadinessTracksDependencyFailure(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var healthy atomic.Bool
	healthy.Store(true)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode: config.ModeAPI,
			Config: config.Config{
				ShutdownTimeout: 2 * time.Second,
			},
			Logger:          logging.New("info", io.Discard),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
			ReadyChecks: []func(context.Context) error{
				func(ctx context.Context) error {
					if !healthy.Load() {
						return errors.New("banco inacessível")
					}
					return nil
				},
			},
		})
	}()

	client := &http.Client{Timeout: time.Second}
	base := "http://" + httpListener.Addr().String()
	waitStatus(t, client, base+"/readyz", http.StatusOK)

	// Simulate dependency failure (e.g. DB outage)
	healthy.Store(false)
	waitStatus(t, client, base+"/readyz", http.StatusServiceUnavailable)

	// Simulate dependency recovery
	healthy.Store(true)
	waitStatus(t, client, base+"/readyz", http.StatusOK)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown não concluiu")
	}
}
