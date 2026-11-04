package outwebhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"chatisp/internal/platform/urlguard"
)

// HTTPPoster posts one signed body. It resolves through urlguard and dials
// only an address the policy accepted. Redirects are never followed, even when
// the supplied Client would follow them. A literal loopback is dialed only in
// test and development; a name that resolves to loopback or a private address
// is refused in every mode.
type HTTPPoster struct {
	Policy   *urlguard.Policy
	Mode     string
	Client   *http.Client
	Resolver urlguard.Resolver
	Dialer   urlguard.Dialer
}

// Post refuses a destination that fails AllowDestination, then sends the body
// on the pinned transport.
func (p HTTPPoster) Post(ctx context.Context, rawURL string, body []byte, header http.Header) (int, error) {
	if err := AllowDestination(p.Policy, p.Mode, rawURL); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header = header.Clone()
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, nil
}

func (p HTTPPoster) httpClient() *http.Client {
	timeout := 10 * time.Second
	if p.Client != nil && p.Client.Timeout > 0 {
		timeout = p.Client.Timeout
	}
	transport := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			return nil, nil
		},
		DialContext:           p.dialContext,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p HTTPPoster) dialContext(ctx context.Context, _ string, address string) (net.Conn, error) {
	if p.Policy == nil {
		return nil, urlguard.ErrConfig
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return nil, urlguard.ErrDestination
	}
	pin, err := p.pin(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := p.Dialer
	if dialer == nil {
		dialer = urlguard.SystemDialer()
	}
	return dialer.DialContext(ctx, networkOf(pin), net.JoinHostPort(pin.String(), port))
}

func (p HTTPPoster) pin(ctx context.Context, host string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(host)
	if err == nil {
		if p.literalLoopback(addr) {
			return addr, nil
		}
		return p.Policy.Select(urlguard.Scope{}, host, []netip.Addr{addr})
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = urlguard.SystemResolver()
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, urlguard.ErrDNS
	}
	return p.Policy.Select(urlguard.Scope{}, host, addrs)
}

func (p HTTPPoster) literalLoopback(addr netip.Addr) bool {
	if p.Mode != "test" && p.Mode != "development" {
		return false
	}
	return addr.IsValid() && addr.IsLoopback() && !addr.Is4In6()
}

func networkOf(addr netip.Addr) string {
	if addr.Is4() {
		return "tcp4"
	}
	return "tcp6"
}
