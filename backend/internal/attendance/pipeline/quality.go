package pipeline

import (
	"context"
	"errors"

	"chatisp/internal/channel/configuration"
	"chatisp/internal/channel/whatsapp/metacloud"
)

// QualityReader reads quality_rating with the same endpoint rules as sending.
// Simulation reads the local stand-in. Official mode needs the explicit Graph
// version and documentation URL; without them it fails before any request.
type QualityReader struct {
	cfg DriverFactoryConfig
}

// NewQualityReader returns a reader bound to the driver configuration.
// An explicit fake driver has no Graph node, so it is refused.
func NewQualityReader(cfg DriverFactoryConfig) (*QualityReader, error) {
	if cfg.FakeDriver != nil {
		return nil, ErrInvalid
	}
	if cfg.Simulation && cfg.StandInURL == "" {
		return nil, ErrInvalid
	}
	return &QualityReader{cfg: cfg}, nil
}

// ReadQuality implements configuration.QualityReader.
func (r *QualityReader) ReadQuality(ctx context.Context, m configuration.Material) (configuration.QualityResult, error) {
	if r == nil {
		return configuration.QualityResult{}, configuration.ErrQualityUnavailable
	}
	cfg := metacloud.Config{
		Mode:          metacloud.ModeOfficial,
		GraphVersion:  r.cfg.GraphVersion,
		PrimaryDocURL: r.cfg.PrimaryDocURL,
		PhoneNumberID: m.PhoneNumberID,
		AccessToken:   m.AccessToken,
	}
	if r.cfg.Simulation {
		cfg = metacloud.Config{
			Mode:          metacloud.ModeSimulation,
			GraphVersion:  simulationGraphVersion,
			BaseURL:       r.cfg.StandInURL,
			PhoneNumberID: m.PhoneNumberID,
			AccessToken:   m.AccessToken,
		}
	}
	driver, err := metacloud.Activate(cfg)
	if err != nil {
		return configuration.QualityResult{}, configuration.ErrQualityUnavailable
	}
	got, err := driver.QualityRating(ctx)
	switch {
	case err == nil:
		return configuration.QualityResult{Rating: got.Rating, Simulation: got.Simulation}, nil
	case errors.Is(err, metacloud.ErrQualityTokenInvalid):
		return configuration.QualityResult{}, configuration.ErrQualityToken
	case errors.Is(err, metacloud.ErrQualityRejected):
		return configuration.QualityResult{}, configuration.ErrQualityRefused
	default:
		return configuration.QualityResult{}, configuration.ErrQualityUnavailable
	}
}

var _ configuration.QualityReader = (*QualityReader)(nil)
