package runtime

import (
	"context"
	"errors"
	"testing"

	"chatisp/internal/channel/whatsapp"
)

func TestOfficialMediaFetcherRefusesSimulation(t *testing.T) {
	fetcher, err := OfficialMediaFetcher(true, "v23.0")
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

func TestFetcherForProcessStaysLocalInSimulation(t *testing.T) {
	fetcher, err := FetcherForProcess(true, "v23.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fetcher.(whatsapp.DisabledFetcher); !ok {
		t.Fatalf("simulação montou %T", fetcher)
	}
	_, err = fetcher.Fetch(context.Background(), whatsapp.MediaRef{MediaID: "100"})
	if !errors.Is(err, whatsapp.ErrMediaDisabled) {
		t.Fatalf("stand-in: %v", err)
	}
}
