package urlguard

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Resolver looks up a hostname at the moment of access.
// net.Resolver implements it. Tests inject a fixed answer set.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer opens a connection to an address that already passed the policy.
// The address is an IP and a port, never a name that would be resolved again.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// SystemResolver is the process resolver. Using it is an explicit opt-in.
// A nil resolver never touches the network and fails closed.
func SystemResolver() Resolver { return net.DefaultResolver }

// SystemDialer dials an IP address. It does not choose the destination;
// Client resolves and validates before calling it.
func SystemDialer() Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
}

// ClientOption configures a Client.
type ClientOption func(*Client) error

// WithResolver sets the DNS resolver used at dial time.
func WithResolver(r Resolver) ClientOption {
	return func(c *Client) error {
		if r == nil {
			return ErrConfig
		}
		c.resolver = r
		return nil
	}
}

// WithDialer sets the dialer used after an address has been accepted.
// The dialer cannot widen the policy: forbidden targets never reach it.
func WithDialer(d Dialer) ClientOption {
	return func(c *Client) error {
		if d == nil {
			return ErrConfig
		}
		c.dial = d
		return nil
	}
}

// WithTimeout bounds a request that has no deadline of its own.
// Zero keeps only the caller context. The maximum is one minute.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) error {
		if d < 0 || d > time.Minute {
			return ErrConfig
		}
		c.timeout = d
		c.timeoutSet = true
		return nil
	}
}

// WithMaxBytes limits a response body. The maximum accepted setting is 32 MiB.
func WithMaxBytes(n int64) ClientOption {
	return func(c *Client) error {
		if n <= 0 || n > 32<<20 {
			return ErrConfig
		}
		c.maxBytes = n
		return nil
	}
}

// Client fetches one URL under a Policy.
// It does not use HTTP_PROXY or the default transport. Each redirect is
// checked again, and the TCP connection uses only the address Select returned.
type Client struct {
	policy     *Policy
	resolver   Resolver
	dial       Dialer
	timeout    time.Duration
	timeoutSet bool
	maxBytes   int64
	http       *http.Client
}

// NewClient builds a client that fails closed when no resolver or dialer is set.
// The default body limit is 1 MiB and the default timeout is 10 seconds.
func NewClient(p *Policy, opts ...ClientOption) (*Client, error) {
	if p == nil {
		return nil, ErrConfig
	}
	c := &Client{
		policy:   p,
		timeout:  10 * time.Second,
		maxBytes: 1 << 20,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, ErrConfig
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if !c.timeoutSet && c.timeout == 0 {
		c.timeout = 10 * time.Second
	}
	c.http = &http.Client{
		Transport: c.transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return c, nil
}

// Get fetches an absolute URL with the GET method.
func (c *Client) Get(ctx context.Context, scope Scope, rawURL string) (*http.Response, error) {
	if c == nil || c.policy == nil {
		return nil, ErrConfig
	}
	if _, err := c.policy.Inspect(scope, rawURL); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrDestination
	}
	return c.Do(ctx, scope, req)
}

// Do sends req after the policy accepts its URL.
// The caller closes the body. Redirect bodies are closed inside Do.
func (c *Client) Do(ctx context.Context, scope Scope, req *http.Request) (resp *http.Response, err error) {
	if c == nil || c.policy == nil || c.http == nil {
		return nil, ErrConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if c.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
		}
	}
	defer func() {
		if err != nil && cancel != nil {
			cancel()
		}
	}()

	current, err := c.prepare(ctx, scope, req)
	if err != nil {
		return nil, err
	}
	var hops int
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		resp, err = c.http.Do(current)
		if err != nil {
			return nil, sanitize(err)
		}
		if !redirectStatus(resp.StatusCode) {
			return c.finish(resp, cancel)
		}
		if resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
			if current.Method != http.MethodGet && current.Method != http.MethodHead {
				resp.Body.Close()
				return nil, ErrRedirect
			}
		}
		if hops >= c.policy.maxRedirects {
			resp.Body.Close()
			return nil, ErrTooManyRedirects
		}
		nextMethod := http.MethodGet
		if resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
			nextMethod = current.Method
		}
		loc, locErr := resp.Location()
		resp.Body.Close()
		if locErr != nil || loc == nil {
			return nil, ErrRedirect
		}
		hops++
		current, err = c.follow(ctx, scope, nextMethod, loc)
		if err != nil {
			return nil, err
		}
	}
}

func (c *Client) prepare(ctx context.Context, scope Scope, req *http.Request) (*http.Request, error) {
	if req == nil || req.URL == nil {
		return nil, ErrDestination
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, ErrDestination
	}
	target, err := c.policy.Inspect(scope, req.URL.String())
	if err != nil {
		return nil, err
	}
	out := req.Clone(c.withState(ctx, scope, target))
	out.URL = target.URL
	out.Host = ""
	out.RequestURI = ""
	out.Close = true
	return out, nil
}

func (c *Client) follow(ctx context.Context, scope Scope, method string, loc *url.URL) (*http.Request, error) {
	target, err := c.policy.Inspect(scope, loc.String())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.withState(ctx, scope, target), method, target.URL.String(), nil)
	if err != nil {
		return nil, ErrRedirect
	}
	req.URL = target.URL
	req.Host = ""
	req.Close = true
	return req, nil
}

func (c *Client) finish(resp *http.Response, cancel context.CancelFunc) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		if cancel != nil {
			cancel()
		}
		return nil, ErrAccess
	}
	if resp.ContentLength > c.maxBytes {
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, ErrTooLarge
	}
	resp.Body = &limitedBody{rc: resp.Body, left: c.maxBytes, cancel: cancel}
	return resp, nil
}

func (c *Client) withState(ctx context.Context, scope Scope, target Target) context.Context {
	return context.WithValue(ctx, dialKey{}, dialState{
		Scope:  scope,
		Host:   target.Host,
		Port:   target.Port,
		Scheme: target.URL.Scheme,
	})
}

func (c *Client) transport() *http.Transport {
	return &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			// A nil Proxy uses ProxyFromEnvironment and would skip this policy.
			return nil, nil
		},
		DialContext:           c.dialContext,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ResponseHeaderTimeout: c.headerTimeout(),
		TLSHandshakeTimeout:   c.headerTimeout(),
		ExpectContinueTimeout: time.Second,
	}
}

func (c *Client) headerTimeout() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return 10 * time.Second
}

func (c *Client) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	state, ok := ctx.Value(dialKey{}).(dialState)
	if !ok || state.Host == "" || !c.policy.portAllowed(state.Scheme, state.Port) {
		return nil, ErrDestination
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || !sameHost(host, state.Host) {
		return nil, ErrDestination
	}
	pn, err := strconv.Atoi(port)
	if err != nil || pn != state.Port {
		return nil, ErrPort
	}
	pin, err := c.pin(ctx, state)
	if err != nil {
		return nil, err
	}
	if c.dial == nil {
		return nil, ErrAccess
	}
	// Dial the pinned IP. Passing the name here would resolve it again.
	return c.dial.DialContext(ctx, networkOf(pin), net.JoinHostPort(pin.String(), port))
}

func (c *Client) pin(ctx context.Context, state dialState) (netip.Addr, error) {
	addr, literal, err := interpretHost(state.Host)
	if err != nil {
		return netip.Addr{}, ErrDestination
	}
	if literal {
		return c.policy.Select(state.Scope, state.Host, []netip.Addr{addr})
	}
	if c.resolver == nil {
		return netip.Addr{}, ErrDNS
	}
	addrs, err := c.resolver.LookupNetIP(ctx, "ip", state.Host)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return netip.Addr{}, err
		}
		return netip.Addr{}, ErrDNS
	}
	return c.policy.Select(state.Scope, state.Host, addrs)
}

func networkOf(addr netip.Addr) string {
	if addr.Is4() {
		return "tcp4"
	}
	return "tcp6"
}

type dialKey struct{}

type dialState struct {
	Scope  Scope
	Host   string
	Port   int
	Scheme string
}

func redirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func sanitize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	for _, sentinel := range []error{
		ErrScheme, ErrPort, ErrUserinfo, ErrDestination, ErrDNS, ErrRedirect,
		ErrTooManyRedirects, ErrTooLarge, ErrAccess, ErrConfig, ErrGrant,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return ErrAccess
}

type limitedBody struct {
	rc       io.ReadCloser
	left     int64
	tooBig   bool
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
	scratch  [1]byte
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.tooBig {
		return 0, ErrTooLarge
	}
	if len(p) == 0 {
		return 0, nil
	}
	if b.left <= 0 {
		n, err := b.rc.Read(b.scratch[:])
		if n > 0 {
			b.tooBig = true
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.rc.Close()
		if b.cancel != nil {
			b.cancel()
		}
	})
	return b.closeErr
}
