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
}

type driverFactory struct {
	cfg DriverFactoryConfig
}

// NewDriverFactory constructs a DriverFactory.
// Explicit fake drivers are accepted only when Simulation is true.
func NewDriverFactory(cfg DriverFactoryConfig) (DriverFactory, error) {
	if !cfg.Simulation && cfg.FakeDriver != nil {
		return nil, ErrSimulationOnly
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
				GraphVersion:  "v23.0",
				BaseURL:       f.cfg.StandInURL,
				PhoneNumberID: material.PhoneNumberID,
				AccessToken:   material.AccessToken,
			})
		}
		return whatsapp.NewFake(), nil
	}

	return metacloud.Activate(metacloud.Config{
		Mode:          metacloud.ModeOfficial,
		GraphVersion:  "v23.0",
		PhoneNumberID: material.PhoneNumberID,
		AccessToken:   material.AccessToken,
		PrimaryDocURL: "https://developers.facebook.com/docs/whatsapp/cloud-api/messages",
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
		return metacloud.OfficialMediaFetcher(false, "v23.0")
	}
	return whatsapp.DisabledFetcher{}, nil
}
