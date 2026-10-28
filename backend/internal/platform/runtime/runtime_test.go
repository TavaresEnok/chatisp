package runtime

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
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
