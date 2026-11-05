// Command chatisp runs the API, worker, migrations or OpenAPI export.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/pipeline"
	attendanceService "chatisp/internal/attendance/service"
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
	"chatisp/internal/outwebhook"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/platform/migrate"
	"chatisp/internal/platform/opsignals"
	"chatisp/internal/platform/runtime"
	"chatisp/internal/platform/tenantcache"
	"chatisp/internal/presence"
	"chatisp/internal/realtime"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
)

const usageText = `uso: chatisp <modo>

modos:
  api       servidor HTTP e métricas
  worker    worker e métricas
  migrate   aplica migrações SQL com goose
  all       API, worker e métricas
  openapi   grava o OpenAPI em JSON
  bootstrap cria o primeiro provedor e o primeiro administrador

bootstrap (instalação vazia; senha pela entrada padrão, primeira linha):
  chatisp bootstrap -email admin@exemplo.com [-superadmin] [-timezone America/Sao_Paulo]
  Exige CHATISP_DATABASE_URL (dono), CHATISP_APP_DATABASE_URL e CHATISP_PLATFORM_DATABASE_URL.

openapi:
  chatisp openapi [-output caminho]

Execute a partir de backend/ para o caminho padrão api/openapi.json.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runWithInput(ctx, os.Args[1:], os.Environ(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "chatisp: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, environ []string, stdout, stderr io.Writer) error {
	return runWithInput(ctx, args, environ, nil, stdout, stderr)
}

func runWithInput(ctx context.Context, args []string, environ []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return errors.New("informe o modo: api, worker, migrate, all ou openapi")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "openapi":
		return exportOpenAPI(args[1:], stdout, stderr)
	case "dev-prepare":
		return devPrepare(ctx, args[1:], stdout, stderr)
	case "bootstrap":
		return bootstrap(ctx, args[1:], environ, stdin, stdout, stderr)
	case "api", "worker", "migrate", "all":
		load := config.LoadRuntime
		if args[0] == "migrate" {
			load = config.Load
		}
		cfg, err := load(environ)
		if err != nil {
			return err
		}
		log := logging.New(cfg.LogLevel, stdout)
		if args[0] == "migrate" {
			return applyMigrations(ctx, cfg, log)
		}
		mode, err := config.ParseMode(args[0])
		if err != nil {
			return err
		}
		svcs, err := setupRuntimeServices(ctx, cfg, mode, log, environ)
		if err != nil {
			return err
		}
		return runtime.Run(ctx, runtime.Options{
			Mode:           mode,
			Config:         cfg,
			Logger:         log,
			Auth:           svcs.Auth,
			Attendance:     svcs.Attendance,
			Collision:      svcs.Collision,
			Channels:       svcs.Channels,
			Sectors:        svcs.Sectors,
			Contacts:       svcs.Contacts,
			Notifications:  svcs.Notifications,
			Realtime:       svcs.Realtime,
			Webhook:        svcs.Webhook,
			CannedReplies:  svcs.CannedReplies,
			ChannelPolicy:  svcs.ChannelPolicy,
			Attachments:    svcs.Attachments,
			BusinessHours:  svcs.BusinessHours,
			Documents:      svcs.Documents,
			Presence:       svcs.Presence,
			Lifecycle:      svcs.Lifecycle,
			Triage:         svcs.Triage,
			Embed:          svcs.Embed,
			Store:          svcs.Store,
			Catalog:        svcs.Catalog,
			JobsClient:     svcs.JobsClient,
			ReadyChecks:    svcs.ReadyChecks,
			Cleanup:        svcs.Cleanup,
			TrustedOrigins: cfg.PanelOrigins,
			Metrics:        svcs.Metrics,
			Collectors:     svcs.Collectors,
		})
	default:
		return errors.New("modo desconhecido: use api, worker, migrate, all ou openapi")
	}
}

func applyMigrations(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if err := migrate.Apply(ctx, cfg, log); err != nil {
		return err
	}
	ownerCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("falha ao aplicar migrações River")
	}
	ownerCfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, ownerCfg)
	if err != nil {
		return errors.New("falha ao aplicar migrações River")
	}
	defer pool.Close()
	if err := jobs.Migrate(ctx, pool); err != nil {
		return errors.New("falha ao aplicar migrações River")
	}
	return nil
}

type runtimeServices struct {
	Auth          *auth.Service
	Attendance    attendanceService.Service
	Collision     *collision.Service
	Channels      *channelConfig.Service
	Sectors       sectors.Service
	Contacts      contactdirectory.Service
	Notifications *notifications.Service
	Realtime      http.Handler
	Webhook       http.Handler
	CannedReplies cannedreplies.Service
	ChannelPolicy *channelpolicy.Service
	Attachments   *attachments.Service
	BusinessHours businesshours.Service
	Documents     contactdocs.Service
	Presence      presence.Service
	Lifecycle     lifecycle.Service
	Triage        triage.Service
	Embed         embed.Service
	Store         *db.Store
	Catalog       *tenantcache.Cache
	JobsClient    runtime.Worker
	ReadyChecks   []func(context.Context) error
	Cleanup       func()
	Metrics       *metrics.Metrics
	Collectors    []func(context.Context)
}

func setupRuntimeServices(ctx context.Context, cfg config.Config, mode config.Mode, log *slog.Logger, environ []string) (runtimeServices, error) {
	switch mode {
	case config.ModeAPI:
		if cfg.AppDatabaseURL == "" || cfg.PlatformDatabaseURL == "" || cfg.IngressDatabaseURL == "" {
			return runtimeServices{}, errors.New("urls de banco de dados (app, platform, ingress) obrigatórias para o modo api")
		}
		if cfg.KeyRing == nil {
			return runtimeServices{}, errors.New("chaves de dados (CHATISP_DATA_KEYS) obrigatórias para o modo api")
		}
		if strings.TrimSpace(cfg.MetaVerifyToken) == "" {
			return runtimeServices{}, errors.New("CHATISP_META_VERIFY_TOKEN é obrigatório para recepção de webhooks no modo api")
		}
	case config.ModeWorker:
		if !cfg.MetaSimulation {
			return runtimeServices{}, errors.New("despacho oficial Meta desabilitado no marco M2: CHATISP_META_SIMULATION=true é obrigatório para execução de workers de envio")
		}
		if cfg.JobsDatabaseURL == "" {
			return runtimeServices{}, errors.New("url de banco de jobs (CHATISP_JOBS_DATABASE_URL) obrigatória para o modo worker")
		}
		if cfg.AppDatabaseURL == "" || cfg.PlatformDatabaseURL == "" || cfg.IngressDatabaseURL == "" {
			return runtimeServices{}, errors.New("urls de banco de dados (app, platform, ingress) obrigatórias para o modo worker")
		}
		if cfg.KeyRing == nil {
			return runtimeServices{}, errors.New("chaves de dados (CHATISP_DATA_KEYS) obrigatórias para o modo worker")
		}
	case config.ModeAll:
		if !cfg.MetaSimulation {
			return runtimeServices{}, errors.New("despacho oficial Meta desabilitado no marco M2: CHATISP_META_SIMULATION=true é obrigatório para execução de workers de envio")
		}
		if cfg.JobsDatabaseURL == "" {
			return runtimeServices{}, errors.New("url de banco de jobs (CHATISP_JOBS_DATABASE_URL) obrigatória para o modo all")
		}
		if cfg.AppDatabaseURL == "" || cfg.PlatformDatabaseURL == "" || cfg.IngressDatabaseURL == "" {
			return runtimeServices{}, errors.New("urls de banco de dados (app, platform, ingress) obrigatórias para o modo all")
		}
		if cfg.KeyRing == nil {
			return runtimeServices{}, errors.New("chaves de dados (CHATISP_DATA_KEYS) obrigatórias para o modo all")
		}
		if strings.TrimSpace(cfg.MetaVerifyToken) == "" {
			return runtimeServices{}, errors.New("CHATISP_META_VERIFY_TOKEN é obrigatório para recepção de webhooks no modo all")
		}
	}

	meter := metrics.New()

	var (
		pools      *db.Pools
		workerPool *pgxpool.Pool
		cleanups   []func()
	)
	cleanupAll := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	hasDB := cfg.AppDatabaseURL != "" || cfg.PlatformDatabaseURL != "" || cfg.IngressDatabaseURL != ""
	if hasDB {
		var err error
		pools, err = db.OpenPools(ctx, db.RoleURLs{
			App:      cfg.AppDatabaseURL,
			Platform: cfg.PlatformDatabaseURL,
			Ingress:  cfg.IngressDatabaseURL,
		})
		if err != nil {
			return runtimeServices{}, fmt.Errorf("falha ao preparar banco de dados: %w", err)
		}
		cleanups = append(cleanups, pools.Close)
	}

	if (mode == config.ModeWorker || mode == config.ModeAll) && cfg.JobsDatabaseURL != "" {
		wp, err := openWorkerPool(ctx, cfg.JobsDatabaseURL)
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao conectar ao banco de jobs: %w", err)
		}
		workerPool = wp
		cleanups = append(cleanups, workerPool.Close)
	}

	var readyChecks []func(context.Context) error
	if pools != nil {
		if pools.App != nil {
			readyChecks = append(readyChecks, pools.App.Ping)
		}
		if pools.Platform != nil {
			readyChecks = append(readyChecks, pools.Platform.Ping)
		}
		if pools.Ingress != nil {
			readyChecks = append(readyChecks, pools.Ingress.Ping)
		}
	}
	if workerPool != nil {
		readyChecks = append(readyChecks, workerPool.Ping)
	}
	collectors := bindProcessTelemetry(meter, pools, workerPool)

	insertJobsClient, err := jobs.New(nil, jobs.Handlers{})
	if err != nil {
		cleanupAll()
		return runtimeServices{}, fmt.Errorf("falha ao criar cliente de jobs: %w", err)
	}

	var store *db.Store
	if pools != nil && pools.App != nil && pools.Platform != nil {
		store = db.NewStore(pools.App, pools.Platform)
	}

	var identity *auth.Service
	if store != nil {
		identity, err = auth.New(auth.Options{
			Store:  store,
			Mail:   mail.NewSender(cfg.SMTPAddr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom),
			Logger: log,
			Keys:   cfg.KeyRing,
		})
		if err != nil {
			cleanupAll()
			return runtimeServices{}, errors.New("falha ao preparar autenticação")
		}
	}

	var privateMedia attachments.Storage
	if store != nil {
		privateMedia, err = attachmentStorage(environ)
		if err != nil {
			cleanupAll()
			return runtimeServices{}, err
		}
	}

	var attendanceSvc attendanceService.Service
	if store != nil {
		attendanceSvc = attendanceService.New(store, insertJobsClient)
		// The API return value and the worker copy must share this store.
		// A later assignment does not reach handlers already constructed.
		attendanceSvc.Files = privateMedia
		attendanceSvc.AttachmentLimits = tenantAttachmentLimits
	}

	var channelSvc *channelConfig.Service
	if store != nil && pools != nil && pools.Ingress != nil && cfg.KeyRing != nil {
		channelSvc, err = channelConfig.New(store, pools.Ingress, cfg.KeyRing)
		if err != nil {
			cleanupAll()
			return runtimeServices{}, errors.New("falha ao preparar configuração de canais")
		}
		channelSvc.SetLogger(log)
		// The quality_rating read exists only where Graph may be read. Official
		// dispatch is still refused at startup (D2-03), so only the local
		// stand-in gets a reader; elsewhere the admin read answers 503.
		if cfg.MetaSimulation && cfg.MetaStandInURL != "" {
			reader, err := pipeline.NewQualityReader(pipeline.DriverFactoryConfig{Simulation: true, StandInURL: cfg.MetaStandInURL})
			if err != nil {
				cleanupAll()
				return runtimeServices{}, errors.New("falha ao preparar leitura de qualidade")
			}
			channelSvc.SetQualityReader(reader)
		}
	}

	var realtimeHandler http.Handler
	if identity != nil && store != nil {
		var notifier realtime.Notifier
		if pools != nil && pools.App != nil {
			pgNotifier := realtime.NewPGNotifier(ctx, pools.App, log)
			cleanups = append(cleanups, pgNotifier.Close)
			notifier = pgNotifier
		} else {
			notifier = realtime.NewLocalNotifier()
		}
		authAdapter := realtime.NewAuthAdapter(identity, store)
		var authenticator realtime.Authenticator = authAdapter
		if store != nil {
			authenticator = runtime.SessionPresence{Next: authAdapter, Presence: presence.New(store), Log: log}
		}
		realtimeHandler = realtime.NewHandler(realtime.Options{
			TrustedOrigins: cfg.PanelOrigins,
			Authenticator:  authenticator,
			Attendance:     attendanceSvc,
			InboxResolver:  authAdapter,
			Notifier:       notifier,
			Logger:         log,
			DB:             store,
			Metrics:        meter,
		})
		if store != nil {
			realtimeHandler = runtime.SessionPresenceHandler{Next: realtimeHandler, Presence: presence.New(store), Log: log}
		}
	}

	var webhookHandler http.Handler
	if channelSvc != nil && pools != nil && pools.Ingress != nil {
		configServer := channelSvc.Server()
		verifyTokens := pipeline.VerifyTokenFunc(func(_ context.Context, _ string) (string, error) {
			if cfg.MetaVerifyToken != "" {
				return cfg.MetaVerifyToken, nil
			}
			return "", pipeline.ErrNotFound
		})
		wh, err := pipeline.NewWebhookHandler(pipeline.WebhookConfig{
			AppID:        cfg.MetaAppID,
			AppResolver:  configServer,
			VerifyTokens: verifyTokens,
			IngressPool:  pools.Ingress,
			JobsClient:   insertJobsClient,
			Logger:       log,
		})
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao configurar webhook: %w", err)
		}
		webhookHandler = wh
	}

	var workerClient *jobs.Client
	if (mode == config.ModeWorker || mode == config.ModeAll) && workerPool != nil && store != nil && pools != nil && pools.Ingress != nil && channelSvc != nil {
		if !cfg.MetaSimulation {
			cleanupAll()
			return runtimeServices{}, errors.New("despacho oficial Meta desabilitado no marco M2: CHATISP_META_SIMULATION=true é obrigatório para execução de workers de envio")
		}
		configServer := channelSvc.Server()
		driverFactory, err := pipeline.NewDriverFactory(pipeline.DriverFactoryConfig{
			Simulation: cfg.MetaSimulation,
			StandInURL: cfg.MetaStandInURL,
		})
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao preparar driver factory: %w", err)
		}
		triageInboxes := make(map[uuid.UUID]bool)
		for _, id := range cfg.TriageInboxes {
			triageInboxes[uuid.MustParse(id)] = true
		}
		mediaFetcher, err := runtime.FetcherForProcess(cfg.MetaSimulation, "v23.0")
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao selecionar download de mídia: %w", err)
		}
		handlers, err := pipeline.NewWorkers(pipeline.WorkersConfig{
			DB:                 store,
			TriageInboxes:      triageInboxes,
			IngressPool:        pools.Ingress,
			JobsClient:         insertJobsClient,
			AttendanceService:  attendanceSvc,
			RouteResolver:      configServer,
			MaterialResolver:   configServer,
			CredentialReporter: configServer,
			DriverFactory:      driverFactory,
			Logger:             log,
			MediaStorage:       privateMedia,
			MediaFetcher:       mediaFetcher,
			Simulation:         cfg.MetaSimulation,
		})
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao registrar handlers de workers: %w", err)
		}
		handlers.Inbound = arrivalInbound{next: handlers.Inbound, store: store, log: log}
		handlers.Webhook = outwebhook.JobsHandler{Deps: outwebhook.Deps{Store: store, Ring: cfg.KeyRing, Mode: cfg.Env}}
		workerClient, err = jobs.New(workerPool, handlers, jobs.WithLogger(log), jobs.WithMetrics(meter))
		if err != nil {
			cleanupAll()
			return runtimeServices{}, fmt.Errorf("falha ao montar cliente de jobs: %w", err)
		}
	}

	var workerWorker runtime.Worker
	if workerClient != nil {
		workerWorker = workerClient
	}

	switch mode {
	case config.ModeAPI:
		if store == nil || identity == nil || attendanceSvc.DB == nil || channelSvc == nil || realtimeHandler == nil || webhookHandler == nil {
			cleanupAll()
			return runtimeServices{}, errors.New("serviços essenciais incompletos para o modo api")
		}
	case config.ModeWorker:
		if workerClient == nil {
			cleanupAll()
			return runtimeServices{}, errors.New("trabalhador River não pôde ser inicializado para o modo worker")
		}
	case config.ModeAll:
		if store == nil || identity == nil || attendanceSvc.DB == nil || channelSvc == nil || realtimeHandler == nil || webhookHandler == nil || workerClient == nil {
			cleanupAll()
			return runtimeServices{}, errors.New("serviços essenciais incompletos para o modo all")
		}
	}

	var canned cannedreplies.Service
	var hours businesshours.Service
	var policySvc *channelpolicy.Service
	var attachmentSvc *attachments.Service
	var documents contactdocs.Service
	var operatorPresence presence.Service
	var panels embed.Service
	var catalog *tenantcache.Cache
	if store != nil {
		opened, openErr := tenantcache.New()
		if openErr != nil {
			cleanupAll()
			return runtimeServices{}, openErr
		}
		catalog = opened
		cleanups = append(cleanups, catalog.Close)
		canned = cannedreplies.New(store)
		hours = businesshours.New(store)
		hours.Cache = catalog
		// No implicit catalog driver. SyncTemplates refuses before any write
		// until a real driver is injected. An empty fake is not a provider snapshot.
		policySvc = &channelpolicy.Service{DB: store, Cache: catalog}
		attachmentSvc = &attachments.Service{DB: store, Storage: privateMedia, Limits: tenantAttachmentLimits}
		documents = contactdocs.New(store, cfg.KeyRing)
		operatorPresence = presence.New(store)
		panels = embed.New(store)
	}

	return runtimeServices{
		Auth:          identity,
		Attendance:    attendanceSvc,
		Collision:     &collision.Service{DB: store},
		Channels:      channelSvc,
		Sectors:       sectors.Service{DB: store},
		Contacts:      contactdirectory.Service{DB: store, Visibility: httpapi.ContactVisibility{}},
		Notifications: notifications.New(store),
		Realtime:      realtimeHandler,
		Webhook:       webhookHandler,
		CannedReplies: canned,
		ChannelPolicy: policySvc,
		Attachments:   attachmentSvc,
		BusinessHours: hours,
		Documents:     documents,
		Presence:      operatorPresence,
		Lifecycle:     lifecycle.New(store),
		Triage:        triage.New(store),
		Embed:         panels,
		Store:         store,
		Catalog:       catalog,
		JobsClient:    workerWorker,
		ReadyChecks:   readyChecks,
		Cleanup:       cleanupAll,
		Metrics:       meter,
		Collectors:    collectors,
	}, nil
}

// bindProcessTelemetry attaches pools that are already open and, only when the
// jobs pool exists, the River backlog reader. The API process has no jobs pool
// and therefore does not publish a process-local empty backlog as if it were
// the queue.
func bindProcessTelemetry(meter *metrics.Metrics, pools *db.Pools, jobsPool *pgxpool.Pool) []func(context.Context) {
	if meter == nil {
		return nil
	}
	var bindings []metrics.PoolBinding
	if pools != nil {
		if pools.App != nil {
			bindings = append(bindings, metrics.PoolBinding{Role: "app", Pool: pools.App})
		}
		if pools.Platform != nil {
			bindings = append(bindings, metrics.PoolBinding{Role: "platform", Pool: pools.Platform})
		}
		if pools.Ingress != nil {
			bindings = append(bindings, metrics.PoolBinding{Role: "ingress", Pool: pools.Ingress})
		}
	}
	if jobsPool != nil {
		bindings = append(bindings, metrics.PoolBinding{Role: "jobs", Pool: jobsPool})
	}
	meter.RegisterPools(bindings)
	if jobsPool == nil {
		return nil
	}
	collectors := []func(context.Context){
		func(ctx context.Context) {
			jobs.CollectRiverBacklog(ctx, jobsPool, meter)
		},
	}
	if pools != nil && pools.Platform != nil {
		platform := pools.Platform
		collectors = append(collectors, func(ctx context.Context) {
			opsignals.Collect(ctx, platform, meter)
		})
	}
	return collectors
}

func environValue(environ []string, key string) string {
	prefix := key + "="
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

func attachmentStorage(environ []string) (attachments.Storage, error) {
	keys := []string{
		"CHATISP_ATTACHMENTS_S3_ENDPOINT",
		"CHATISP_ATTACHMENTS_S3_REGION",
		"CHATISP_ATTACHMENTS_S3_BUCKET",
		"CHATISP_ATTACHMENTS_S3_ACCESS_KEY",
		"CHATISP_ATTACHMENTS_S3_SECRET_KEY",
	}
	values := make(map[string]string, len(keys))
	var missing []string
	for _, key := range keys {
		value := strings.TrimSpace(environValue(environ, key))
		values[key] = value
		if value == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) == len(keys) {
		return attachments.UnavailableStorage(), nil
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("armazenamento de anexos incompleto: %s", strings.Join(missing, ", "))
	}
	storage, err := attachments.NewS3(attachments.S3Config{
		Endpoint:     values["CHATISP_ATTACHMENTS_S3_ENDPOINT"],
		Region:       values["CHATISP_ATTACHMENTS_S3_REGION"],
		Bucket:       values["CHATISP_ATTACHMENTS_S3_BUCKET"],
		AccessKey:    values["CHATISP_ATTACHMENTS_S3_ACCESS_KEY"],
		SecretKey:    values["CHATISP_ATTACHMENTS_S3_SECRET_KEY"],
		SessionToken: strings.TrimSpace(environValue(environ, "CHATISP_ATTACHMENTS_S3_SESSION_TOKEN")),
		PathStyle:    strings.EqualFold(strings.TrimSpace(environValue(environ, "CHATISP_ATTACHMENTS_S3_PATH_STYLE")), "true"),
	})
	if err != nil {
		return nil, errors.New("armazenamento de anexos recusou a configuração explícita")
	}
	return storage, nil
}

func tenantAttachmentLimits(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (attachments.Limits, error) {
	var plan, reserve, file int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((limits->>'attachment_plan_bytes')::bigint, 0),
		       COALESCE((limits->>'attachment_reserve_bytes')::bigint, 0),
		       COALESCE((limits->>'attachment_file_bytes')::bigint, 0)
		  FROM tenants
		 WHERE id = $1
		   AND id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`, tenantID).Scan(&plan, &reserve, &file)
	if err != nil || plan <= 0 || file <= 0 || reserve < 0 {
		return attachments.Limits{}, attachments.ErrStorage
	}
	return attachments.Limits{PlanBytes: plan, IncomingReserveBytes: reserve, FileBytes: file}, nil
}

func openWorkerPool(ctx context.Context, rawURL string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, errors.New("url de banco de jobs inválida")
	}
	poolCfg.MaxConns = 8
	poolCfg.MinConns = 0
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "chatisp_jobs"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, errors.New("falha ao abrir pool de jobs")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errors.New("falha ao conectar ao banco de jobs")
	}
	return pool, nil
}

func exportOpenAPI(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("openapi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", defaultOpenAPIOutput(), "arquivo de saída")
	flags.Usage = func() {
		fmt.Fprint(stderr, "uso: chatisp openapi [-output caminho]\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("argumentos inválidos para openapi")
	}
	if flags.NArg() != 0 {
		return errors.New("argumentos inválidos para openapi")
	}
	body, err := httpapi.OpenAPIJSON()
	if err != nil {
		return errors.New("falha ao gerar OpenAPI")
	}
	if err := writeFileAtomic(*output, body); err != nil {
		return errors.New("falha ao gravar OpenAPI")
	}
	fmt.Fprintf(stdout, "OpenAPI gravado em %s\n", *output)
	return nil
}

func defaultOpenAPIOutput() string {
	if _, err := os.Stat("go.mod"); err == nil {
		return filepath.Join("api", "openapi.json")
	}
	if _, err := os.Stat(filepath.Join("backend", "go.mod")); err == nil {
		return filepath.Join("backend", "api", "openapi.json")
	}
	return filepath.Join("api", "openapi.json")
}

func writeFileAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(dir, ".openapi-*.json")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
