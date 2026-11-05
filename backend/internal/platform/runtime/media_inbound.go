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
// Only an authorized official process builds the Graph client, with the
// explicitly configured version (never a default). Simulation and a disabled
// integration get a fetcher that does not dial and reports ErrMediaDisabled.
func FetcherForProcess(official bool, graphVersion string) (whatsapp.MediaFetcher, error) {
	if !official {
		return whatsapp.DisabledFetcher{}, nil
	}
	return OfficialMediaFetcher(false, graphVersion)
}
