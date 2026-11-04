package outwebhook

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"chatisp/internal/platform/urlguard"
)

func TestPosterPinsDNSAndRefusesRedirects(t *testing.T) {
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	dialer := &recordingDialer{status: 302, location: "http://evil.test/meta"}
	resolver := &scriptResolver{answers: map[string][][]netip.Addr{
		"good.test":  {{netip.MustParseAddr("1.2.3.4"), netip.MustParseAddr("10.0.0.1")}},
		"evil.test":  {{netip.MustParseAddr("169.254.169.254")}},
		"local.test": {{netip.MustParseAddr("127.0.0.1")}},
		"rebinding.test": {
			{netip.MustParseAddr("1.2.3.4")},
			{netip.MustParseAddr("127.0.0.1")},
		},
	}}
	poster := HTTPPoster{
		Policy: policy, Mode: "production", Resolver: resolver, Dialer: dialer,
		Client: &http.Client{
			Timeout: time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return nil
			},
		},
	}
	for _, raw := range []string{
		"http://good.test/hook",
		"http://local.test/hook",
		"http://10.1.2.3/hook",
		"http://127.0.0.1/hook",
	} {
		if _, err := poster.Post(context.Background(), raw, []byte(`{}`), http.Header{}); err == nil {
			t.Fatalf("%s foi aceito", raw)
		}
	}
	if len(dialer.snapshot()) != 0 {
		t.Fatalf("discagem indevida %v", dialer.snapshot())
	}
	if resolver.calls("evil.test") != 0 {
		t.Fatal("redirecionamento resolveu o destino")
	}

	dialer.status = 204
	resolver.answers["good.test"] = [][]netip.Addr{{netip.MustParseAddr("1.2.3.4")}}
	status, err := poster.Post(context.Background(), "http://good.test/hook", []byte(`{}`), http.Header{})
	if err != nil || status != 204 {
		t.Fatalf("entrega pública %d %v", status, err)
	}
	if got := dialer.snapshot(); len(got) != 1 || got[0] != "tcp4 1.2.3.4:80" {
		t.Fatalf("pino %v", got)
	}

	dialer.reset()
	dialer.status = 302
	dialer.location = "http://evil.test/meta"
	status, err = poster.Post(context.Background(), "http://rebinding.test/hook", []byte(`{}`), http.Header{})
	if err != nil || status != 302 {
		t.Fatalf("redirect %d %v", status, err)
	}
	if resolver.calls("rebinding.test") != 1 || resolver.calls("evil.test") != 0 {
		t.Fatalf("consultas %#v", resolver.seen)
	}
	if got := dialer.snapshot(); len(got) != 1 || got[0] != "tcp4 1.2.3.4:80" {
		t.Fatalf("redirect discou %v", got)
	}

	dialer.reset()
	loop := HTTPPoster{Policy: policy, Mode: "test", Resolver: resolver, Dialer: dialer}
	status, err = loop.Post(context.Background(), "http://127.0.0.1:9/hook", []byte(`{}`), http.Header{})
	if err != nil || status != 302 {
		t.Fatalf("loopback de teste %d %v", status, err)
	}
	if got := dialer.snapshot(); len(got) != 1 || got[0] != "tcp4 127.0.0.1:9" {
		t.Fatalf("loopback %v", got)
	}
	if _, err = loop.Post(context.Background(), "http://local.test/hook", []byte(`{}`), http.Header{}); err == nil {
		t.Fatal("nome que resolve para loopback foi aceito no teste")
	}
}

type scriptResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	seen    map[string]int
}

func (r *scriptResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	n := r.seen[host]
	r.seen[host] = n + 1
	sets := r.answers[host]
	if len(sets) == 0 {
		return nil, urlLookupError{}
	}
	if n >= len(sets) {
		return sets[len(sets)-1], nil
	}
	return sets[n], nil
}

func (r *scriptResolver) calls(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[host]
}

type urlLookupError struct{}

func (urlLookupError) Error() string { return "lookup" }

type recordingDialer struct {
	mu       sync.Mutex
	addrs    []string
	status   int
	location string
}

func (d *recordingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addrs = append(d.addrs, network+" "+address)
	status := d.status
	if status == 0 {
		status = 204
	}
	var extra string
	if d.location != "" {
		extra = "Location: " + d.location + "\r\n"
	}
	raw := "HTTP/1.1 " + itoa(status) + " X\r\n" + extra + "Content-Length: 0\r\nConnection: close\r\n\r\n"
	return &memConn{r: bufio.NewReader(strings.NewReader(raw))}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type memConn struct {
	r *bufio.Reader
}

func (c *memConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *memConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *memConn) Close() error                { return nil }
func (c *memConn) LocalAddr() net.Addr         { return stubAddr{} }
func (c *memConn) RemoteAddr() net.Addr        { return stubAddr{} }
func (c *memConn) SetDeadline(time.Time) error { return nil }
func (c *memConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

type stubAddr struct{}

func (stubAddr) Network() string { return "tcp" }
func (stubAddr) String() string  { return "pinned" }

func (d *recordingDialer) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]string(nil), d.addrs...)
	return out
}

func (d *recordingDialer) reset() {
	d.mu.Lock()
	d.addrs = nil
	d.mu.Unlock()
}
