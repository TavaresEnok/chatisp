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
	"sync"
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

func TestShutdownClosesConnectionWaitingForHeaders(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeAPI,
			Config:          config.Config{ShutdownTimeout: time.Second},
			Logger:          logging.New("error", io.Discard),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
		})
	}()
	client := &http.Client{Timeout: time.Second}
	waitStatus(t, client, "http://"+httpListener.Addr().String()+"/healthz", http.StatusOK)
	client.CloseIdleConnections()

	conn, err := net.Dial("tcp", httpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(200 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown ficou preso em conexão sem pedido")
	}
}

func TestShutdownCancelsInFlightReadyCheck(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeAPI,
			Config:          config.Config{ShutdownTimeout: time.Second},
			Logger:          logging.New("error", io.Discard),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
			ReadyChecks: []func(context.Context) error{
				func(checkCtx context.Context) error {
					select {
					case <-started:
					default:
						close(started)
					}
					<-checkCtx.Done()
					return checkCtx.Err()
				},
			},
		})
	}()
	base := "http://" + httpListener.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}
	waitStatus(t, client, base+"/healthz", http.StatusOK)
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	go func() {
		request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/readyz", nil)
		if err != nil {
			return
		}
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ready check não começou")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown não cancelou o handler em andamento")
	}
}

func TestShutdownLogsHTTPError(t *testing.T) {
	httpListener := listenLocal(t)
	metricsListener := listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	started := make(chan struct{})
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Mode:            config.ModeAPI,
			Config:          config.Config{ShutdownTimeout: 300 * time.Millisecond},
			Logger:          logging.New("info", &logs),
			HTTPListener:    httpListener,
			MetricsListener: metricsListener,
			ReadyChecks: []func(context.Context) error{
				func(context.Context) error {
					select {
					case <-started:
					default:
						close(started)
					}
					<-release
					return nil
				},
			},
		})
	}()
	base := "http://" + httpListener.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}
	waitStatus(t, client, base+"/healthz", http.StatusOK)
	go func() {
		response, err := client.Get(base + "/readyz")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ready check não começou")
	}
	cancel()
	select {
	case err := <-done:
		released = true
		close(release)
		if err == nil {
			t.Fatal("handler preso deveria falhar o shutdown")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown não retornou")
	}
	text := logs.String()
	if !strings.Contains(text, "falha ao encerrar o servidor HTTP") || !strings.Contains(text, "context deadline exceeded") {
		t.Fatalf("log sem erro de shutdown: %s", text)
	}
	if strings.Contains(text, "postgres://") || strings.Contains(text, "super-secret") {
		t.Fatalf("log expôs segredo: %s", text)
	}
}

type closeFlagConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeFlagConn) Close() error {
	c.closed.Store(true)
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

func TestCloseQuietBeforeShutdownArmLeavesSocketsOpen(t *testing.T) {
	states := newConnStates()
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	conn := &closeFlagConn{Conn: left}
	states.observe(conn, http.StateNew)
	states.beforeQuietClose = func() {
		t.Errorf("seleção quieta correu antes do shutdown")
	}
	states.closeQuiet()
	if conn.closed.Load() {
		t.Fatal("socket fechado antes do servidor marcar shutdown")
	}
}

func TestQuietSelectionDoesNotCloseSocketThatBecomesActive(t *testing.T) {
	for _, initial := range []http.ConnState{http.StateNew, http.StateIdle} {
		t.Run(initial.String(), func(t *testing.T) {
			states := newConnStates()
			states.armShutdown()
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			conn := &closeFlagConn{Conn: left}
			states.observe(conn, initial)
			states.beforeQuietClose = func() {
				states.observe(conn, http.StateActive)
			}
			states.closeQuiet()
			if conn.closed.Load() {
				t.Fatalf("seleção %s fechou o socket depois de StateActive", initial)
			}
		})
	}
}

func TestQuietSelectionClosesSocketThatStaysQuiet(t *testing.T) {
	states := newConnStates()
	states.armShutdown()
	left, right := net.Pipe()
	defer right.Close()
	conn := &closeFlagConn{Conn: left}
	states.observe(conn, http.StateNew)
	held := false
	states.beforeQuietClose = func() { held = true }
	states.closeQuiet()
	if !held || !conn.closed.Load() {
		t.Fatalf("quieto permaneceu aberto: hook=%v fechado=%v", held, conn.closed.Load())
	}
}

func TestCloseQuietSkipsActiveAndHijacked(t *testing.T) {
	states := newConnStates()
	states.armShutdown()
	activeLeft, activeRight := net.Pipe()
	defer activeLeft.Close()
	defer activeRight.Close()
	active := &closeFlagConn{Conn: activeLeft}
	states.observe(active, http.StateActive)
	hijackedLeft, hijackedRight := net.Pipe()
	defer hijackedLeft.Close()
	defer hijackedRight.Close()
	hijacked := &closeFlagConn{Conn: hijackedLeft}
	states.observe(hijacked, http.StateNew)
	states.observe(hijacked, http.StateHijacked)
	states.closeQuiet()
	if active.closed.Load() || hijacked.closed.Load() {
		t.Fatalf("handler ou hijack fechado: active=%v hijack=%v", active.closed.Load(), hijacked.closed.Load())
	}
}

func TestShutdownKeepsHandlerThatActivatedBeforeQuietClose(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	states := newConnStates()
	entered := make(chan struct{})
	releaseHandler := make(chan struct{})
	handlerReleased := false
	defer func() {
		if !handlerReleased {
			close(releaseHandler)
		}
	}()
	inCommit := make(chan struct{})
	releaseCommit := make(chan struct{})
	commitReleased := false
	defer func() {
		if !commitReleased {
			close(releaseCommit)
		}
	}()
	var once sync.Once
	states.beforeQuietClose = func() {
		once.Do(func() {
			close(inCommit)
			<-releaseCommit
		})
	}
	fresh := make(chan struct{}, 8)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/hold" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			close(entered)
			<-releaseHandler
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "kept")
		}),
		ReadHeaderTimeout: 30 * time.Second,
		ConnState: func(conn net.Conn, state http.ConnState) {
			states.observe(conn, state)
			if state == http.StateNew {
				select {
				case fresh <- struct{}{}:
				default:
				}
			}
		},
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + listener.Addr().String()
	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		response, err := client.Get(base + "/hold")
		if err != nil {
			errCh <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			errCh <- err
			return
		}
		if response.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("status %d", response.StatusCode)
			return
		}
		bodyCh <- string(body)
	}()
	select {
	case <-entered:
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("handler não começou")
	}
	for {
		select {
		case <-fresh:
		default:
			goto drained
		}
	}
drained:
	raw, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	select {
	case <-fresh:
	case <-time.After(5 * time.Second):
		t.Fatal("conexão sem header não foi aceita")
	}

	shutdownErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownErr <- shutdownHTTP(ctx, server, states)
	}()
	select {
	case <-inCommit:
	case err := <-shutdownErr:
		t.Fatalf("shutdown terminou antes da seleção quieta: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("seleção quieta não começou depois do shutdown")
	}

	handlerReleased = true
	close(releaseHandler)
	select {
	case body := <-bodyCh:
		if body != "kept" {
			t.Fatalf("corpo: %q", body)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("cliente não recebeu a resposta do handler")
	}
	commitReleased = true
	close(releaseCommit)
	select {
	case err := <-shutdownErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown não concluiu")
	}
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve não retornou")
	}
}
