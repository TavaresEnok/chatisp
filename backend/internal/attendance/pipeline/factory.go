package pipeline

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

// DriverFactoryConfig configures driver creation for WhatsApp Cloud API.
type DriverFactoryConfig struct {
	Simulation bool
	StandInURL string
	FakeDriver whatsapp.TextDriver
	// Official is the explicit authorization to send through the real Graph
	// API. Without it and without Simulation the factory is disabled: every
	// DriverFor returns ErrDispatchDisabled and no fake stands in for Meta.
	Official bool
	// GraphVersion and PrimaryDocURL are required in official mode and are
	// never defaulted: the version is the open decision D2-03. The local
	// stand-in keeps its own fixed path value (simulationGraphVersion).
	GraphVersion  string
	PrimaryDocURL string
}

// simulationGraphVersion is only the path segment the local stand-in serves.
// It is not the product's Graph version.
const simulationGraphVersion = "v23.0"

type driverFactory struct {
	cfg DriverFactoryConfig
}

// NewDriverFactory constructs a DriverFactory.
// Explicit fake drivers are accepted only when Simulation is true.
func NewDriverFactory(cfg DriverFactoryConfig) (DriverFactory, error) {
	if !cfg.Simulation && cfg.FakeDriver != nil {
		return nil, ErrSimulationOnly
	}
	if cfg.Simulation && cfg.Official {
		return nil, ErrInvalid
	}
	return &driverFactory{cfg: cfg}, nil
}

// DriverFor returns a whatsapp.TextDriver configured for the given material.
func (f *driverFactory) DriverFor(_ context.Context, _, _ uuid.UUID, material configuration.Material) (whatsapp.TextDriver, error) {
	if f.cfg.Simulation {
		if f.cfg.FakeDriver != nil {
			return f.cfg.FakeDriver, nil
		}
		if f.cfg.StandInURL != "" {
			return metacloud.Activate(metacloud.Config{
				Mode:          metacloud.ModeSimulation,
				GraphVersion:  simulationGraphVersion,
				BaseURL:       f.cfg.StandInURL,
				PhoneNumberID: material.PhoneNumberID,
				AccessToken:   material.AccessToken,
			})
		}
		return whatsapp.NewFake(), nil
	}
	if !f.cfg.Official {
		return nil, ErrDispatchDisabled
	}

	return metacloud.Activate(metacloud.Config{
		Mode:          metacloud.ModeOfficial,
		GraphVersion:  f.cfg.GraphVersion,
		PhoneNumberID: material.PhoneNumberID,
		AccessToken:   material.AccessToken,
		PrimaryDocURL: f.cfg.PrimaryDocURL,
	})
}

// WorkersConfig provides dependencies for the pipeline's River worker handlers.
type WorkersConfig struct {
	DB                 *db.Store
	IngressPool        *pgxpool.Pool
	JobsClient         *jobs.Client
	AttendanceService  service.Service
	RouteResolver      RouteResolver
	MaterialResolver   MaterialResolver
	CredentialReporter CredentialReporter
	DriverFactory      DriverFactory
	Logger             *slog.Logger
	TriageInboxes      map[uuid.UUID]bool
	MediaStorage       attachments.Storage
	MediaFetcher       whatsapp.MediaFetcher
	OfficialMedia      bool
	Simulation         bool
	// GraphVersion is required for OfficialMedia and is never defaulted.
	GraphVersion string
}

// NewWorkers builds the jobs.Handlers containing Ingress, Inbound, Outbound and Lease handlers.
func NewWorkers(cfg WorkersConfig) (jobs.Handlers, error) {
	if cfg.DB == nil || cfg.IngressPool == nil || cfg.JobsClient == nil ||
		cfg.MaterialResolver == nil || cfg.CredentialReporter == nil || cfg.DriverFactory == nil {
		return jobs.Handlers{}, ErrInvalid
	}

	ingress := NewIngressHandler(cfg.IngressPool, cfg.JobsClient, cfg.Logger, cfg.RouteResolver)
	automation := &Automation{DB: cfg.DB, Jobs: cfg.JobsClient, TriageInboxes: cfg.TriageInboxes}
	inbound := NewInboundHandler(cfg.DB, cfg.AttendanceService, automation)
	if handler, ok := inbound.(*inboundHandler); ok {
		handler.jobs = cfg.JobsClient
	}
	outbound := NewOutboundHandler(cfg.DB, cfg.AttendanceService, cfg.MaterialResolver, cfg.CredentialReporter, cfg.DriverFactory)
	outbound.(*outboundHandler).triageInboxes = cfg.TriageInboxes
	lease := NewOutboundLease(cfg.DB)
	fetcher, err := selectMediaFetcher(cfg)
	if err != nil {
		return jobs.Handlers{}, err
	}

	media := NewMediaHandler(cfg.DB, cfg.MediaStorage, fetcher, cfg.MaterialResolver).(*mediaHandler)
	// Same resolver the attachment API uses, so inbound bytes share the budget.
	media.limits = cfg.AttendanceService.AttachmentLimits
	return jobs.Handlers{
		Ingress:    ingress,
		Inbound:    inbound,
		Outbound:   outbound,
		Lease:      lease,
		Automation: automation,
		Media:      media,
	}, nil
}

// selectMediaFetcher chooses the downloader for this process.
// Simulation never builds the Graph client. An explicit fetcher is the
// stand-in used by tests and by the simulated process. OfficialMedia builds
// the guarded Graph client and refuses to run together with simulation.
func selectMediaFetcher(cfg WorkersConfig) (whatsapp.MediaFetcher, error) {
	if cfg.OfficialMedia && cfg.Simulation {
		return nil, ErrInvalid
	}
	if cfg.MediaFetcher != nil {
		if cfg.OfficialMedia {
			return nil, ErrInvalid
		}
		return cfg.MediaFetcher, nil
	}
	if cfg.OfficialMedia {
		return metacloud.OfficialMediaFetcher(false, cfg.GraphVersion)
	}
	return whatsapp.DisabledFetcher{}, nil
}
