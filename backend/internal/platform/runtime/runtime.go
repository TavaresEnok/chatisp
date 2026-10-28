// Package runtime runs the API, worker and metrics listener until shutdown.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"chatisp/internal/platform/config"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/metrics"
)

// Options selects listeners and the process mode. Nil listeners are opened from Config.
type Options struct {
	Mode            config.Mode
	Config          config.Config
	Logger          *slog.Logger
	Metrics         *metrics.Metrics
	HTTPListener    net.Listener
	MetricsListener net.Listener
}

// Run serves until ctx is cancelled or a listener fails, then shuts down gracefully.
func Run(parent context.Context, opts Options) error {
	switch opts.Mode {
	case config.ModeAPI, config.ModeWorker, config.ModeAll:
	default:
		return errors.New("modo de execução inválido")
	}
	if opts.Logger == nil {
		opts.Logger = logging.New(opts.Config.LogLevel, nil)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	meter := opts.Metrics
	if meter == nil {
		meter = metrics.New()
	}
	gate := &readyGate{}

	var httpServer *http.Server
	if opts.Mode == config.ModeAPI || opts.Mode == config.ModeAll {
		api := httpapi.New(httpapi.Dependencies{
			Ready:   gate.Check,
			Metrics: meter,
			Logger:  opts.Logger,
		})
		httpServer = newServer(api.Handler, opts.Logger)
	}
	metricsServer := newServer(meter.Handler(), opts.Logger)

	httpListener, httpOwned, err := listen(opts.HTTPListener, opts.Config.HTTPAddr, httpServer != nil)
	if err != nil {
		return fmt.Errorf("não foi possível escutar o endereço HTTP %s", opts.Config.HTTPAddr)
	}
	metricsListener, _, err := listen(opts.MetricsListener, opts.Config.MetricsAddr, true)
	if err != nil {
		closeOwned(httpListener, httpOwned)
		return fmt.Errorf("não foi possível escutar o endereço de métricas %s", opts.Config.MetricsAddr)
	}

	results := make(chan error, 2)
	started := 0
	serve := func(server *http.Server, listener net.Listener) {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			results <- nil
			return
		}
		results <- serveErr
	}

	gate.Set(true)
	if httpServer != nil {
		started++
		go serve(httpServer, httpListener)
		opts.Logger.Info("API escutando", "component", "api", "http_addr", httpListener.Addr().String())
	}
	started++
	go serve(metricsServer, metricsListener)
	opts.Logger.Info("métricas escutando", "component", "metrics", "metrics_addr", metricsListener.Addr().String())

	workerDone := make(chan struct{})
	if opts.Mode == config.ModeWorker || opts.Mode == config.ModeAll {
		go func() {
			defer close(workerDone)
			opts.Logger.Info("worker aguardando tarefas", "component", "worker")
			<-ctx.Done()
			opts.Logger.Info("worker encerrado", "component", "worker")
		}()
	} else {
		close(workerDone)
	}
	opts.Logger.Info("serviço iniciado", "component", string(opts.Mode))

	var failure error
	select {
	case <-ctx.Done():
	case serveErr := <-results:
		started--
		cancel()
		if serveErr != nil {
			opts.Logger.Error("servidor encerrou", "component", "runtime", "error", serveErr.Error())
			failure = errors.New("servidor encerrou com falha")
		}
	}

	gate.Set(false)
	opts.Logger.Info("encerramento iniciado", "component", "runtime")
	timeout := opts.Config.ShutdownTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)
	defer shutdownCancel()
	if httpServer != nil {
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			opts.Logger.Error("falha ao encerrar o servidor HTTP", "component", "api")
			if failure == nil {
				failure = errors.New("falha ao encerrar o servidor HTTP")
			}
		}
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		opts.Logger.Error("falha ao encerrar o servidor de métricas", "component", "metrics")
		if failure == nil {
			failure = errors.New("falha ao encerrar o servidor de métricas")
		}
	}
	cancel()

	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		if failure == nil {
			failure = errors.New("tempo esgotado ao encerrar o worker")
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for started > 0 {
		select {
		case serveErr := <-results:
			started--
			if serveErr != nil && failure == nil {
				opts.Logger.Error("servidor encerrou", "component", "runtime", "error", serveErr.Error())
				failure = errors.New("servidor encerrou com falha")
			}
		case <-timer.C:
			return errors.New("tempo esgotado ao encerrar o serviço")
		}
	}
	return failure
}

func newServer(handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          log.New(logWriter{logger: logger}, "", 0),
	}
}

type logWriter struct {
	logger *slog.Logger
}

func (w logWriter) Write(p []byte) (int, error) {
	message := string(p)
	if logging.Sensitive(message) {
		w.logger.Error("erro de transporte HTTP", "component", "http")
		return len(p), nil
	}
	w.logger.Error("erro de transporte HTTP", "component", "http", "error", message)
	return len(p), nil
}

func listen(existing net.Listener, addr string, needed bool) (net.Listener, bool, error) {
	if !needed {
		return nil, false, nil
	}
	if existing != nil {
		return existing, false, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, false, err
	}
	return listener, true, nil
}

func closeOwned(listener net.Listener, owned bool) {
	if owned && listener != nil {
		_ = listener.Close()
	}
}

type readyGate struct {
	mu    sync.RWMutex
	ready bool
}

func (g *readyGate) Set(ready bool) {
	g.mu.Lock()
	g.ready = ready
	g.mu.Unlock()
}

func (g *readyGate) Check(context.Context) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.ready {
		return errors.New("serviço indisponível")
	}
	return nil
}
