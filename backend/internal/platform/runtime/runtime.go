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

	"chatisp/internal/attachments"
	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/bothook"
	"chatisp/internal/businesshours"
	"chatisp/internal/cannedreplies"
	channelConfig "chatisp/internal/channel/configuration"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/collision"
	"chatisp/internal/contactdirectory"
	"chatisp/internal/contactdocs"
	"chatisp/internal/embed"
	"chatisp/internal/lifecycle"
	"chatisp/internal/notifications"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/presence"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
)

// Worker represents the lifecycle for background job execution.
type Worker interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	StopAndCancel(ctx context.Context) error
	Stopped() <-chan struct{}
}

// Options selects listeners and the process mode. Nil listeners are opened from Config.
type Options struct {
	Mode            config.Mode
	Config          config.Config
	Logger          *slog.Logger
	Metrics         *metrics.Metrics
	HTTPListener    net.Listener
	MetricsListener net.Listener
	Auth            *auth.Service
	Attendance      attendanceService.Service
	Collision       *collision.Service
	Channels        *channelConfig.Service
	Sectors         sectors.Service
	Contacts        contactdirectory.Service
	Notifications   *notifications.Service
	Realtime        http.Handler
	Webhook         http.Handler
	CannedReplies   cannedreplies.Service
	ChannelPolicy   *channelpolicy.Service
	Attachments     *attachments.Service
	BusinessHours   businesshours.Service
	Documents       contactdocs.Service
	Presence        presence.Service
	Lifecycle       lifecycle.Service
	Triage          triage.Service
	Embed           embed.Service
	Store           *db.Store
	JobsClient      Worker
	Cleanup         func()
	TrustedOrigins  []string
	ReadyChecks     []func(context.Context) error
}

// Run serves until ctx is cancelled or a listener fails, then shuts down gracefully.
func Run(parent context.Context, opts Options) error {
	switch opts.Mode {
	case config.ModeAPI, config.ModeWorker, config.ModeAll:
	default:
		return errors.New("modo de execução inválido")
	}
	cleanupSafe := true
	defer func() {
		if cleanupSafe && opts.Cleanup != nil {
			opts.Cleanup()
		}
	}()
	if opts.Logger == nil {
		opts.Logger = logging.New(opts.Config.LogLevel, nil)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	meter := opts.Metrics
	if meter == nil {
		meter = metrics.New()
	}
	gate := &readyGate{
		checks: append([]func(context.Context) error(nil), opts.ReadyChecks...),
	}

	var httpServer *http.Server
	if opts.Mode == config.ModeAPI || opts.Mode == config.ModeAll {
		api := httpapi.New(httpapi.Dependencies{
			Ready:          gate.Check,
			Metrics:        meter,
			Logger:         opts.Logger,
			Auth:           opts.Auth,
			Attendance:     opts.Attendance,
			Collision:      opts.Collision,
			Channels:       opts.Channels,
			Sectors:        opts.Sectors,
			Contacts:       opts.Contacts,
			Notifications:  opts.Notifications,
			Realtime:       opts.Realtime,
			Webhook:        opts.Webhook,
			CannedReplies:  opts.CannedReplies,
			ChannelPolicy:  opts.ChannelPolicy,
			Attachments:    opts.Attachments,
			BusinessHours:  opts.BusinessHours,
			Documents:      opts.Documents,
			Presence:       opts.Presence,
			Lifecycle:      opts.Lifecycle,
			Triage:         opts.Triage,
			Embed:          opts.Embed,
			BotHook:        bothook.New(opts.Store),
			Store:          opts.Store,
			KeyRing:        opts.Config.KeyRing,
			Env:            opts.Config.Env,
			TrustedOrigins: opts.TrustedOrigins,
		})
		httpServer = newServer(api.Handler, opts.Logger)
	}
	metricsServer := newServer(meter.Handler(), opts.Logger)

	httpListener, httpOwned, err := listen(opts.HTTPListener, opts.Config.HTTPAddr, httpServer != nil)
	if err != nil {
		return fmt.Errorf("não foi possível escutar o endereço HTTP %s", opts.Config.HTTPAddr)
	}
	metricsListener, metricsOwned, err := listen(opts.MetricsListener, opts.Config.MetricsAddr, true)
	if err != nil {
		closeOwned(httpListener, httpOwned)
		return fmt.Errorf("não foi possível escutar o endereço de métricas %s", opts.Config.MetricsAddr)
	}

	workerDone := make(chan struct{})
	var workerStopErr error
	var workerStopped bool
	if opts.Mode == config.ModeWorker || opts.Mode == config.ModeAll {
		// Recover unmarked sources before this client fetches. The same leader
		// owns ingress, inbound, outbound, webhooks and the lifecycle schedule.
		if hookErr := startOutboundWebhooks(ctx, opts); hookErr != nil {
			opts.Logger.Error("falha ao recuperar webhooks de saída", "component", "outwebhook", "error", hookErr.Error())
			closeOwned(httpListener, httpOwned)
			closeOwned(metricsListener, metricsOwned)
			return fmt.Errorf("falha ao recuperar webhooks de saída: %w", hookErr)
		}
		if opts.JobsClient != nil {
			gate.AddCheck(func(context.Context) error {
				select {
				case <-workerDone:
					return errors.New("trabalhador River não está em execução")
				default:
					return nil
				}
			})
			if err := opts.JobsClient.Start(ctx); err != nil {
				opts.Logger.Error("falha ao iniciar worker River", "component", "worker", "error", err.Error())
				closeOwned(httpListener, httpOwned)
				closeOwned(metricsListener, metricsOwned)
				return fmt.Errorf("falha ao iniciar worker River: %w", err)
			}
			opts.Logger.Info("worker River iniciado", "component", "worker")
		}
		go func() {
			defer close(workerDone)
			opts.Logger.Info("worker aguardando tarefas", "component", "worker")
			<-ctx.Done()
			if opts.JobsClient != nil {
				opts.Logger.Info("worker River encerrando", "component", "worker")
				stopTimeout := opts.Config.ShutdownTimeout
				if stopTimeout <= 0 {
					stopTimeout = 15 * time.Second
				}
				stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout/2)
				workerStopErr = opts.JobsClient.Stop(stopCtx)
				stopCancel()
				select {
				case <-opts.JobsClient.Stopped():
					workerStopped = true
				default:
					forceCtx, forceCancel := context.WithTimeout(context.Background(), stopTimeout/2)
					forceErr := opts.JobsClient.StopAndCancel(forceCtx)
					select {
					case <-opts.JobsClient.Stopped():
						workerStopped = true
					case <-forceCtx.Done():
					}
					forceCancel()
					workerStopErr = errors.Join(workerStopErr, forceErr)
					if !workerStopped {
						// Allow cancelled jobs a bounded final interval to persist their
						// state before the process returns and closes their pools.
						timer := time.NewTimer(stopTimeout)
						select {
						case <-opts.JobsClient.Stopped():
							workerStopped = true
						case <-timer.C:
							workerStopErr = errors.Join(workerStopErr, errors.New("worker não confirmou parada; pools permanecem abertos"))
						}
						timer.Stop()
					}
				}
			} else {
				workerStopped = true
			}
			if workerStopped {
				opts.Logger.Info("worker encerrado", "component", "worker")
			}
		}()
	} else {
		workerStopped = true
		close(workerDone)
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
		if workerStopErr != nil {
			failure = errors.Join(failure, fmt.Errorf("falha ao encerrar worker: %w", workerStopErr))
		}
		cleanupSafe = workerStopped
	case <-time.After(2*timeout + time.Second):
		cleanupSafe = false
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
	mu     sync.RWMutex
	ready  bool
	checks []func(context.Context) error
}

func (g *readyGate) Set(ready bool) {
	g.mu.Lock()
	g.ready = ready
	g.mu.Unlock()
}

func (g *readyGate) AddCheck(check func(context.Context) error) {
	if check == nil {
		return
	}
	g.mu.Lock()
	g.checks = append(g.checks, check)
	g.mu.Unlock()
}

func (g *readyGate) Check(ctx context.Context) error {
	g.mu.RLock()
	ready := g.ready
	checks := append([]func(context.Context) error(nil), g.checks...)
	g.mu.RUnlock()

	if !ready {
		return errors.New("serviço indisponível")
	}

	for _, check := range checks {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := check(checkCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("dependência indisponível: %w", err)
		}
	}
	return nil
}
