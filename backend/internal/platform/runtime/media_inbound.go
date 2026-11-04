package runtime

import (
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
)

// OfficialMediaFetcher returns the Graph downloader. Simulation refuses it
// before any client is built, so a local stand-in never dials Meta.
func OfficialMediaFetcher(simulation bool, graphVersion string) (whatsapp.MediaFetcher, error) {
	return metacloud.OfficialMediaFetcher(simulation, graphVersion)
}

// FetcherForProcess selects the inbound downloader for the running mode.
// Simulation returns a stand-in that does not dial. The Graph client is built
// only when simulation is off.
func FetcherForProcess(simulation bool, graphVersion string) (whatsapp.MediaFetcher, error) {
	if simulation {
		return whatsapp.DisabledFetcher{}, nil
	}
	return OfficialMediaFetcher(false, graphVersion)
}
