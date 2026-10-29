package urlguard

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportPinsLiteralAndDNS(t *testing.T) {
	resolver := &mapResolver{answers: map[string][]netip.Addr{
		"example.test": {netip.MustParseAddr("1.2.3.4"), netip.MustParseAddr("8.8.8.8")},
	}}
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, mustPolicy(t), WithResolver(resolver), WithDialer(dialer), WithTimeout(time.Second))

	body := getBody(t, client, Scope{}, "http://1.1.1.1/health")
	if body != "ok" {
		t.Fatal(body)
	}
	if resolver.calls.Load() != 0 {
		t.Fatalf("literal resolved %d times", resolver.calls.Load())
	}
	assertDial(t, dialer.snapshot(), "tcp4", "1.1.1.1:80")

	dialer.reset()
	body = getBody(t, client, Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}, "http://example.test/health")
	if body != "ok" {
		t.Fatal(body)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("lookups %d", resolver.calls.Load())
	}
	assertDial(t, dialer.snapshot(), "tcp4", "1.2.3.4:80")
	waitClosed(t, dialer.conns())
}

func TestTransportRejectsBeforeDial(t *testing.T) {
	resolver := &mapResolver{answers: map[string][]netip.Addr{
		"example.test": {netip.MustParseAddr("1.2.3.4"), netip.MustParseAddr("10.0.0.1")},
		"mapped.test":  {netip.MustParseAddr("::ffff:1.2.3.4")},
		"local.test":   {netip.MustParseAddr("127.0.0.1")},
		"meta.test":    {netip.MustParseAddr("169.254.169.254")},
		"loop.test":    {netip.MustParseAddr("1.2.3.4")},
	}}
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, mustPolicy(t), WithResolver(resolver), WithDialer(dialer))
	cases := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://[::1]/",
		"http://[::ffff:8.8.8.8]/",
		"http://169.254.169.254/",
		"http://user:s3cret@1.1.1.1/",
		"file:///etc/passwd",
		"gopher://1.1.1.1/",
		"http://1.1.1.1:22/",
		"http://1.1.1.1:8080/",
		"http://localhost/x",
		"http://metadata.google.internal/",
		"http://example.test/x",
		"http://mapped.test/x",
		"http://local.test/x",
		"http://meta.test/x",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			before := dialer.count()
			_, err := client.Get(context.Background(), Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}, raw)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "://") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatal(err)
			}
			if dialer.count() != before {
				t.Fatalf("dialed forbidden target %v", dialer.snapshot())
			}
		})
	}
	if dialer.count() != 0 {
		t.Fatal(dialer.snapshot())
	}
}

func TestDNSRebindingAndRedirectDoNotConnect(t *testing.T) {
	flip := &flipResolver{first: netip.MustParseAddr("1.2.3.4"), second: netip.MustParseAddr("127.0.0.1")}
	dialer := &scriptDialer{respond: func(addr string) fakeResp {
		return fakeResp{status: 302, location: "http://stable.test/next"}
	}}
	client := mustClient(t, mustPolicy(t, WithMaxRedirects(2)), WithResolver(flip), WithDialer(dialer))
	_, err := client.Get(context.Background(), Scope{}, "http://stable.test/start")
	if !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if flip.calls.Load() != 2 {
		t.Fatalf("lookups %d", flip.calls.Load())
	}
	got := dialer.snapshot()
	if len(got) != 1 || got[0] != "tcp4 1.2.3.4:80" {
		t.Fatalf("dials %v", got)
	}
	waitClosed(t, dialer.conns())

	resolver := &mapResolver{answers: map[string][]netip.Addr{
		"good.test": {netip.MustParseAddr("1.2.3.4")},
		"evil.test": {netip.MustParseAddr("169.254.169.254")},
	}}
	dialer = &scriptDialer{respond: func(addr string) fakeResp {
		if addr == "1.2.3.4:80" {
			return fakeResp{status: 302, location: "http://evil.test/meta"}
		}
		return okResponse(addr)
	}}
	client = mustClient(t, mustPolicy(t), WithResolver(resolver), WithDialer(dialer))
	_, err = client.Get(context.Background(), Scope{}, "http://good.test/")
	if !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "evil.test") || strings.Contains(err.Error(), "169.254") {
		t.Fatal(err)
	}
	got = dialer.snapshot()
	if len(got) != 1 || got[0] != "tcp4 1.2.3.4:80" {
		t.Fatalf("dials %v", got)
	}

	dialer.reset()
	literal := &scriptDialer{respond: func(string) fakeResp {
		return fakeResp{status: 302, location: "http://127.0.0.1/secret"}
	}}
	client = mustClient(t, mustPolicy(t), WithResolver(resolver), WithDialer(literal))
	_, err = client.Get(context.Background(), Scope{}, "http://1.2.3.4/start")
	if !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if snapshot := literal.snapshot(); len(snapshot) != 1 || snapshot[0] != "tcp4 1.2.3.4:80" {
		t.Fatalf("dials %v", snapshot)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatal(err)
	}
}

func TestSingleLookupPinsAddress(t *testing.T) {
	seq := &seqResolver{seq: [][]netip.Addr{
		{netip.MustParseAddr("1.2.3.4")},
		{netip.MustParseAddr("10.0.0.9")},
	}}
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, mustPolicy(t), WithResolver(seq), WithDialer(dialer))
	body := getBody(t, client, Scope{}, "http://once.test/health")
	if body != "ok" {
		t.Fatal(body)
	}
	if seq.calls.Load() != 1 {
		t.Fatalf("second lookup happened: %d", seq.calls.Load())
	}
	assertDial(t, dialer.snapshot(), "tcp4", "1.2.3.4:80")
}

func TestEnvironmentProxyCannotBypass(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("http_proxy", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://user:s3cret@127.0.0.1:9")
	t.Setenv("https_proxy", "http://user:s3cret@127.0.0.1:9")
	t.Setenv("ALL_PROXY", "http://169.254.169.254:80")
	t.Setenv("NO_PROXY", "")
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, mustPolicy(t), WithResolver(&mapResolver{}), WithDialer(dialer))
	body := getBody(t, client, Scope{}, "http://8.8.8.8/health")
	if body != "ok" {
		t.Fatal(body)
	}
	assertDial(t, dialer.snapshot(), "tcp4", "8.8.8.8:80")
	for _, addr := range dialer.snapshot() {
		if strings.Contains(addr, "127.0.0.1") || strings.Contains(addr, "169.254") {
			t.Fatal(addr)
		}
	}
}

func TestPrivateGrantDoesNotCrossScope(t *testing.T) {
	p := mustPolicy(t, WithGrant(Grant{
		TenantID:     "tenant-a",
		ConnectionID: "conn-1",
		Hosts:        []string{"erp.internal"},
		Prefixes:     []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")},
	}))
	resolver := &mapResolver{answers: map[string][]netip.Addr{
		"erp.internal":   {netip.MustParseAddr("10.1.2.3")},
		"other.internal": {netip.MustParseAddr("10.1.2.3")},
	}}
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, p, WithResolver(resolver), WithDialer(dialer))
	allowed := Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}
	if body := getBody(t, client, allowed, "http://erp.internal/orders"); body != "ok" {
		t.Fatal(body)
	}
	assertDial(t, dialer.snapshot(), "tcp4", "10.1.2.3:80")

	for _, tc := range []struct {
		scope Scope
		raw   string
	}{
		{Scope{TenantID: "tenant-b", ConnectionID: "conn-1"}, "http://erp.internal/orders"},
		{Scope{TenantID: "tenant-a", ConnectionID: "conn-2"}, "http://erp.internal/orders"},
		{allowed, "http://other.internal/orders"},
		{allowed, "http://10.9.9.9/orders"},
		{Scope{}, "http://10.1.2.3/orders"},
	} {
		before := dialer.count()
		_, err := client.Get(context.Background(), tc.scope, tc.raw)
		if !errors.Is(err, ErrDestination) || dialer.count() != before {
			t.Fatalf("%s %v dials %v", tc.raw, err, dialer.snapshot())
		}
	}
}

func TestRedirectLimitBytesAndClose(t *testing.T) {
	dialer := &scriptDialer{respond: func(addr string) fakeResp {
		switch addr {
		case "1.1.1.1:80":
			return fakeResp{status: 302, location: "http://8.8.8.8/two"}
		case "8.8.8.8:80":
			return fakeResp{status: 302, location: "http://1.0.0.1/three"}
		default:
			return okResponse(addr)
		}
	}}
	client := mustClient(t, mustPolicy(t, WithMaxRedirects(1)), WithDialer(dialer))
	_, err := client.Get(context.Background(), Scope{}, "http://1.1.1.1/one")
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatal(err)
	}
	got := dialer.snapshot()
	if len(got) != 2 || got[0] != "tcp4 1.1.1.1:80" || got[1] != "tcp4 8.8.8.8:80" {
		t.Fatalf("dials %v", got)
	}
	waitClosed(t, dialer.conns())

	dialer = &scriptDialer{respond: func(string) fakeResp {
		return fakeResp{status: 200, body: "hello"}
	}}
	client = mustClient(t, mustPolicy(t), WithDialer(dialer), WithMaxBytes(4))
	_, err = client.Get(context.Background(), Scope{}, "http://1.2.3.4/")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	waitClosed(t, dialer.conns())

	dialer = &scriptDialer{respond: func(string) fakeResp {
		return fakeResp{status: 200, body: "hello", chunked: true}
	}}
	client = mustClient(t, mustPolicy(t), WithDialer(dialer), WithMaxBytes(4))
	resp, err := client.Get(context.Background(), Scope{}, "http://1.2.3.4/")
	if err != nil {
		t.Fatal(err)
	}
	buf, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrTooLarge) || len(buf) > 4 {
		t.Fatalf("buf %q err %v", buf, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, dialer.conns())

	dialer = &scriptDialer{respond: okResponse}
	client = mustClient(t, mustPolicy(t), WithDialer(dialer), WithMaxBytes(8))
	resp, err = client.Get(context.Background(), Scope{}, "http://9.9.9.9/health")
	if err != nil {
		t.Fatal(err)
	}
	buf, err = io.ReadAll(resp.Body)
	if err != nil || string(buf) != "ok" {
		t.Fatalf("%q %v", buf, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, dialer.conns())
}

func TestTimeoutAndCancellation(t *testing.T) {
	dialer := &scriptDialer{wait: true}
	client := mustClient(t, mustPolicy(t), WithDialer(dialer), WithTimeout(200*time.Millisecond))
	_, err := client.Get(context.Background(), Scope{}, "http://1.2.3.4/slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "1.2.3.4") {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dialer = &scriptDialer{respond: okResponse}
	client = mustClient(t, mustPolicy(t), WithDialer(dialer), WithTimeout(0))
	_, err = client.Get(ctx, Scope{}, "http://1.2.3.4/health")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if dialer.count() != 0 {
		t.Fatal(dialer.snapshot())
	}
}

func TestDialFailureIsRedacted(t *testing.T) {
	dialer := &scriptDialer{err: errors.New("dial tcp 1.2.3.4:80: connection refused")}
	client := mustClient(t, mustPolicy(t), WithDialer(dialer))
	_, err := client.Get(context.Background(), Scope{}, "https://1.2.3.4/health")
	if !errors.Is(err, ErrAccess) || strings.Contains(err.Error(), "1.2.3.4") {
		t.Fatal(err)
	}
	assertDial(t, dialer.snapshot(), "tcp4", "1.2.3.4:443")
}

func TestNilHooksFailClosed(t *testing.T) {
	client := mustClient(t, mustPolicy(t))
	if _, err := client.Get(context.Background(), Scope{}, "http://1.1.1.1/"); !errors.Is(err, ErrAccess) {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), Scope{}, "http://example.test/"); !errors.Is(err, ErrDNS) {
		t.Fatal(err)
	}
	var resolver Resolver = SystemResolver()
	var dialer Dialer = SystemDialer()
	if resolver == nil || dialer == nil {
		t.Fatal("system hooks")
	}
}

func TestResolverErrorIsRedacted(t *testing.T) {
	resolver := &mapResolver{err: errors.New("lookup secret.example: no such host")}
	dialer := &scriptDialer{respond: okResponse}
	client := mustClient(t, mustPolicy(t), WithResolver(resolver), WithDialer(dialer))
	_, err := client.Get(context.Background(), Scope{}, "http://secret.example/x")
	if !errors.Is(err, ErrDNS) || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if dialer.count() != 0 {
		t.Fatal(dialer.snapshot())
	}
}

func mustClient(t *testing.T, p *Policy, opts ...ClientOption) *Client {
	t.Helper()
	c, err := NewClient(p, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func getBody(t *testing.T, c *Client, scope Scope, raw string) string {
	t.Helper()
	resp, err := c.Get(context.Background(), scope, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func assertDial(t *testing.T, got []string, network, address string) {
	t.Helper()
	want := network + " " + address
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want %s", got, want)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := netip.ParseAddr(host); err != nil {
		t.Fatal(err)
	}
}

func waitClosed(t *testing.T, conns []*trackConn) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ready := len(conns) > 0
		for _, conn := range conns {
			if !conn.closed.Load() {
				ready = false
			}
		}
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("response body was not closed")
}

func okResponse(string) fakeResp {
	return fakeResp{status: 200, body: "ok"}
}

type fakeResp struct {
	status   int
	location string
	body     string
	chunked  bool
}

type trackConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *trackConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

type scriptDialer struct {
	mu      sync.Mutex
	addrs   []string
	tracked []*trackConn
	respond func(addr string) fakeResp
	wait    bool
	err     error
}

func (d *scriptDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.addrs = append(d.addrs, network+" "+address)
	respond := d.respond
	wait := d.wait
	dialErr := d.err
	d.mu.Unlock()
	if wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if dialErr != nil {
		return nil, dialErr
	}
	client, server := net.Pipe()
	tracked := &trackConn{Conn: client}
	d.mu.Lock()
	d.tracked = append(d.tracked, tracked)
	d.mu.Unlock()
	go serveHTTP(server, respond(address))
	return tracked, nil
}

func (d *scriptDialer) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.addrs))
	copy(out, d.addrs)
	return out
}

func (d *scriptDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.addrs)
}

func (d *scriptDialer) reset() {
	d.mu.Lock()
	d.addrs = nil
	d.tracked = nil
	d.mu.Unlock()
}

func (d *scriptDialer) conns() []*trackConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*trackConn, len(d.tracked))
	copy(out, d.tracked)
	return out
}

func serveHTTP(conn net.Conn, resp fakeResp) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	for lines := 0; lines < 100; lines++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" {
			break
		}
	}
	status := resp.status
	if status == 0 {
		status = 200
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 ")
	b.WriteString(strconv.Itoa(status))
	b.WriteString(" X\r\nConnection: close\r\n")
	if resp.location != "" {
		b.WriteString("Location: ")
		b.WriteString(resp.location)
		b.WriteString("\r\n")
	}
	if resp.chunked {
		b.WriteString("Transfer-Encoding: chunked\r\n\r\n")
		b.WriteString(strconv.FormatInt(int64(len(resp.body)), 16))
		b.WriteString("\r\n")
		b.WriteString(resp.body)
		b.WriteString("\r\n0\r\n\r\n")
	} else {
		b.WriteString("Content-Length: ")
		b.WriteString(strconv.Itoa(len(resp.body)))
		b.WriteString("\r\n\r\n")
		b.WriteString(resp.body)
	}
	_, _ = io.WriteString(conn, b.String())
}

type mapResolver struct {
	calls   atomic.Int32
	answers map[string][]netip.Addr
	err     error
}

func (r *mapResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	addrs, ok := r.answers[host]
	if !ok {
		return nil, errors.New("lookup " + host + ": no such host")
	}
	out := make([]netip.Addr, len(addrs))
	copy(out, addrs)
	return out, nil
}

type flipResolver struct {
	calls  atomic.Int32
	first  netip.Addr
	second netip.Addr
}

func (r *flipResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.calls.Add(1) == 1 {
		return []netip.Addr{r.first}, nil
	}
	return []netip.Addr{r.second}, nil
}

type seqResolver struct {
	calls atomic.Int32
	seq   [][]netip.Addr
}

func (r *seqResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := int(r.calls.Add(1)) - 1
	if n >= len(r.seq) {
		n = len(r.seq) - 1
	}
	out := make([]netip.Addr, len(r.seq[n]))
	copy(out, r.seq[n])
	return out, nil
}
