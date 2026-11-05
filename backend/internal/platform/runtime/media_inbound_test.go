package runtime

import (
	"context"
	"errors"
	"testing"

	"chatisp/internal/channel/whatsapp"
)

func TestOfficialMediaFetcherRefusesSimulation(t *testing.T) {
	fetcher, err := OfficialMediaFetcher(true, "v99.0")
	if err == nil || fetcher != nil {
		t.Fatalf("simulação não deve criar fetcher: %v %#v", err, fetcher)
	}
	if _, err := OfficialMediaFetcher(false, ""); err == nil {
		t.Fatal("versão ausente")
	}
	if !errors.Is(err, err) && err.Error() == "" {
		t.Fatal(err)
	}
}

// Only an authorized official process builds the Graph downloader, and only
// with an explicit version. Simulation and a disabled integration never dial.
func TestFetcherForProcessDialsOnlyWhenOfficial(t *testing.T) {
	fetcher, err := FetcherForProcess(false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fetcher.(whatsapp.DisabledFetcher); !ok {
		t.Fatalf("processo sem autorização montou %T", fetcher)
	}
	_, err = fetcher.Fetch(context.Background(), whatsapp.MediaRef{MediaID: "100"})
	if !errors.Is(err, whatsapp.ErrMediaDisabled) {
		t.Fatalf("desabilitado: %v", err)
	}
	if _, err := FetcherForProcess(true, ""); err == nil {
		t.Fatal("oficial sem versão explícita foi aceito")
	}
	official, err := FetcherForProcess(true, "v99.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := official.(whatsapp.DisabledFetcher); ok {
		t.Fatal("oficial autorizado não montou o cliente Graph")
	}
}
