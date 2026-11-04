package metacloud

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/platform/urlguard"
)

type stubResolver struct{ addrs []netip.Addr }

func (s stubResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return s.addrs, nil
}

type stubDialer struct{ calls int }

func (d *stubDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, errors.New("sem rede")
}

func TestGraphFetcherStaysBehindURLGuard(t *testing.T) {
	if _, err := OfficialMediaFetcher(true, "v23.0"); !errors.Is(err, errMediaSimulation) {
		t.Fatalf("simulação: %v", err)
	}
	dialer := &stubDialer{}
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := urlguard.NewClient(policy,
		urlguard.WithResolver(stubResolver{addrs: []netip.Addr{netip.MustParseAddr("169.254.169.254")}}),
		urlguard.WithDialer(dialer),
		urlguard.WithMaxBytes(1<<20),
	)
	if err != nil {
		t.Fatal(err)
	}
	fetcher, err := NewGraphFetcher("v23.0", guard)
	if err != nil {
		t.Fatal(err)
	}
	token := "segredo-de-teste"
	_, err = fetcher.Fetch(context.Background(), whatsapp.MediaRef{MediaID: "100", AccessToken: token, TenantID: "t", ConnectorID: "c"})
	if err == nil || dialer.calls != 0 {
		t.Fatalf("metadata privado discou %d: %v", dialer.calls, err)
	}
	if errors.Is(err, errMediaFetch) && containsToken(err, token) {
		t.Fatal("erro expôs o token")
	}
	_, err = fetcher.Fetch(context.Background(), whatsapp.MediaRef{MediaID: "abc", AccessToken: token})
	if err == nil || dialer.calls != 0 {
		t.Fatalf("id inválido: calls=%d err=%v", dialer.calls, err)
	}
}

func containsToken(err error, token string) bool {
	return err != nil && token != "" && (err.Error() == token || len(err.Error()) > 0 && contains(err.Error(), token))
}

func contains(s, part string) bool {
	return len(part) > 0 && len(s) >= len(part) && (s == part || len(s) > 0 && (func() bool {
		for i := 0; i+len(part) <= len(s); i++ {
			if s[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})())
}
