// Package urlguard checks outbound URLs before a connection is opened.
// ERP, bot, webhook and download adapters use it to keep loopback, metadata
// and foreign private networks unreachable. A private ERP address is allowed
// only by a grant bound to one tenant and one connection.
package urlguard

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const maxURLLength = 2048

// Errors returned to callers are safe for logs and responses.
// They never include the URL, the host or credentials.
var (
	ErrConfig           = errors.New("configuração de saída inválida")
	ErrGrant            = errors.New("autorização privada inválida")
	ErrScheme           = errors.New("protocolo não permitido")
	ErrPort             = errors.New("porta não permitida")
	ErrUserinfo         = errors.New("credencial na URL não é permitida")
	ErrDestination      = errors.New("destino não permitido")
	ErrDNS              = errors.New("resolução DNS rejeitada")
	ErrRedirect         = errors.New("redirecionamento rejeitado")
	ErrTooManyRedirects = errors.New("limite de redirecionamentos excedido")
	ErrTooLarge         = errors.New("resposta excede o limite de bytes")
	ErrAccess           = errors.New("falha ao acessar o destino")
)

const (
	classHard = iota
	classPrivate
	classPublic
)

// Scope is the tenant connection that a request acts for.
// An empty scope can reach public destinations only.
type Scope struct {
	TenantID     string
	ConnectionID string
}

// Grant allows non-public addresses for a single tenant connection.
// Prefixes must sit inside an explicit private or shared range.
// An empty host list allows only IP literals inside those prefixes.
// A host list does not extend the grant to any other tenant or connection.
type Grant struct {
	TenantID     string
	ConnectionID string
	Hosts        []string
	Prefixes     []netip.Prefix
}

// Target is a URL that passed the syntactic and literal-address checks.
// A hostname is resolved later, at dial time.
type Target struct {
	URL     *url.URL
	Host    string
	Port    int
	Literal bool
	Addr    netip.Addr
}

// Policy is an immutable outbound allowlist.
type Policy struct {
	ports        map[string]map[int]struct{}
	grants       []grant
	maxRedirects int
}

type grant struct {
	tenantID     string
	connectionID string
	hosts        []string
	prefixes     []netip.Prefix
}

// Option configures a Policy.
type Option func(*Policy) error

// WithMaxRedirects sets how many redirects may be followed.
// Zero refuses every redirect. The upper bound is ten.
func WithMaxRedirects(n int) Option {
	return func(p *Policy) error {
		if n < 0 || n > 10 {
			return ErrConfig
		}
		p.maxRedirects = n
		return nil
	}
}

// WithGrant adds one scoped private-network exception.
func WithGrant(g Grant) Option {
	return func(p *Policy) error {
		parsed, err := compileGrant(g)
		if err != nil {
			return err
		}
		p.grants = append(p.grants, parsed)
		return nil
	}
}

// NewPolicy allows HTTP on port 80 and HTTPS on port 443.
// It follows at most three redirects. Nothing private is allowed until a grant is added.
func NewPolicy(opts ...Option) (*Policy, error) {
	p := &Policy{
		ports: map[string]map[int]struct{}{
			"http":  {80: {}},
			"https": {443: {}},
		},
		maxRedirects: 3,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, ErrConfig
		}
		if err := opt(p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Inspect checks the scheme, credentials, port and, for a literal, the address.
// It does not resolve DNS. Callers must run Select on the answers at dial time.
func (p *Policy) Inspect(scope Scope, raw string) (Target, error) {
	if p == nil {
		return Target{}, ErrConfig
	}
	if raw == "" || len(raw) > maxURLLength || strings.ContainsAny(raw, " \t\r\n\\") {
		return Target{}, ErrDestination
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Host == "" {
		if err == nil && parsed != nil && parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Target{}, ErrScheme
		}
		return Target{}, ErrDestination
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Target{}, ErrScheme
	}
	if parsed.User != nil {
		return Target{}, ErrUserinfo
	}
	if strings.HasSuffix(parsed.Host, ":") || strings.Contains(parsed.Host, "@") {
		return Target{}, ErrDestination
	}
	host, err := normalizeHost(parsed.Hostname())
	if err != nil {
		return Target{}, err
	}
	addr, literal, err := interpretHost(host)
	if err != nil {
		return Target{}, err
	}
	port, err := portOf(parsed)
	if err != nil {
		return Target{}, err
	}
	if !p.portAllowed(parsed.Scheme, port) {
		return Target{}, ErrPort
	}
	if literal {
		if _, err := p.Select(scope, host, []netip.Addr{addr}); err != nil {
			return Target{}, err
		}
	}
	out := &url.URL{
		Scheme:   parsed.Scheme,
		Host:     hostPort(host, port, parsed.Scheme),
		Path:     parsed.Path,
		RawPath:  parsed.RawPath,
		RawQuery: parsed.RawQuery,
	}
	if strings.ContainsAny(out.Path, "\\\r\n") || strings.ContainsAny(out.RawQuery, "\\\r\n") {
		return Target{}, ErrDestination
	}
	return Target{URL: out, Host: host, Port: port, Literal: literal, Addr: addr}, nil
}

// Select accepts a DNS answer set only when every address is allowed.
// One forbidden answer rejects the whole set, so a mixed or rebinding
// response cannot be used. The returned address is the one to dial.
func (p *Policy) Select(scope Scope, host string, addrs []netip.Addr) (netip.Addr, error) {
	if p == nil {
		return netip.Addr{}, ErrConfig
	}
	host, err := normalizeHost(host)
	if err != nil {
		return netip.Addr{}, ErrDestination
	}
	if _, _, err := interpretHost(host); err != nil {
		return netip.Addr{}, err
	}
	if len(addrs) == 0 {
		return netip.Addr{}, ErrDNS
	}
	for _, addr := range addrs {
		if err := p.allow(scope, host, addr); err != nil {
			if len(addrs) > 1 {
				return netip.Addr{}, ErrDNS
			}
			return netip.Addr{}, err
		}
	}
	return addrs[0], nil
}

func (p *Policy) allow(scope Scope, host string, addr netip.Addr) error {
	if blockedName(host) {
		return ErrDestination
	}
	switch classify(addr) {
	case classPublic:
		return nil
	case classPrivate:
		if p.granted(scope, host, addr) {
			return nil
		}
		return ErrDestination
	default:
		return ErrDestination
	}
}

func (p *Policy) granted(scope Scope, host string, addr netip.Addr) bool {
	tenant, okT := cleanID(scope.TenantID)
	conn, okC := cleanID(scope.ConnectionID)
	if !okT || !okC {
		return false
	}
	for i := range p.grants {
		g := &p.grants[i]
		if g.tenantID != tenant || g.connectionID != conn {
			continue
		}
		if !containsAddr(g.prefixes, addr) {
			continue
		}
		if len(g.hosts) == 0 {
			if hostIsAddr(host, addr) {
				return true
			}
			continue
		}
		if hostListed(g.hosts, host) {
			return true
		}
	}
	return false
}

func (p *Policy) portAllowed(scheme string, port int) bool {
	ports := p.ports[scheme]
	if ports == nil {
		return false
	}
	_, ok := ports[port]
	return ok
}

func compileGrant(g Grant) (grant, error) {
	tenant, okT := cleanID(g.TenantID)
	conn, okC := cleanID(g.ConnectionID)
	if !okT || !okC || len(g.Prefixes) == 0 {
		return grant{}, ErrGrant
	}
	prefixes := make([]netip.Prefix, len(g.Prefixes))
	for i, prefix := range g.Prefixes {
		if !prefixGrantable(prefix) {
			return grant{}, ErrGrant
		}
		prefixes[i] = prefix
	}
	hosts := make([]string, 0, len(g.Hosts))
	for _, host := range g.Hosts {
		normalized, err := normalizeHost(host)
		if err != nil {
			return grant{}, ErrGrant
		}
		addr, literal, err := interpretHost(normalized)
		if err != nil {
			return grant{}, ErrGrant
		}
		if literal && (classify(addr) != classPrivate || !containsAddr(prefixes, addr)) {
			return grant{}, ErrGrant
		}
		hosts = append(hosts, normalized)
	}
	return grant{tenantID: tenant, connectionID: conn, hosts: hosts, prefixes: prefixes}, nil
}

func prefixGrantable(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix.Bits() == 0 || prefix != prefix.Masked() {
		return false
	}
	for _, super := range grantSupernets {
		if super.Bits() <= prefix.Bits() && super.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func classify(addr netip.Addr) int {
	if !addr.IsValid() || addr.Zone() != "" || addr.Is4In6() {
		return classHard
	}
	if containsAddr(hardPrefixes, addr) || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return classHard
	}
	if addr.IsPrivate() || containsAddr(cgnatPrefixes, addr) {
		return classPrivate
	}
	if addr.IsGlobalUnicast() {
		return classPublic
	}
	return classHard
}

func containsAddr(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func portOf(u *url.URL) (int, error) {
	if u.Port() == "" {
		if u.Scheme == "https" {
			return 443, nil
		}
		return 80, nil
	}
	n, err := strconv.Atoi(u.Port())
	if err != nil || n < 1 || n > 65535 {
		return 0, ErrPort
	}
	return n, nil
}

func hostPort(host string, port int, scheme string) string {
	def := 80
	if scheme == "https" {
		def = 443
	}
	if port == def {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func sameHost(a, b string) bool {
	an, aerr := normalizeHost(a)
	bn, berr := normalizeHost(b)
	if aerr != nil || berr != nil {
		return false
	}
	aa, ea := netip.ParseAddr(an)
	bb, eb := netip.ParseAddr(bn)
	if ea == nil || eb == nil {
		return ea == nil && eb == nil && aa == bb
	}
	return an == bn
}

func normalizeHost(host string) (string, error) {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " \t\r\n\\/@#%") {
		return "", ErrDestination
	}
	return host, nil
}

func interpretHost(host string) (netip.Addr, bool, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr, true, nil
	}
	if ambiguousNumeric(host) || blockedName(host) || !validDNS(host) {
		return netip.Addr{}, false, ErrDestination
	}
	return netip.Addr{}, false, nil
}

func ambiguousNumeric(host string) bool {
	lower := strings.ToLower(host)
	if strings.Contains(lower, "0x") {
		return true
	}
	if host != "" && allDigits(host) {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	numeric := 0
	for _, part := range parts {
		if part == "" || !allDigits(part) {
			return false
		}
		numeric++
		if len(part) > 1 && part[0] == '0' {
			return true
		}
		n, err := strconv.Atoi(part)
		if err != nil || n > 255 {
			return true
		}
	}
	return numeric > 0 && numeric != 4
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func blockedName(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	switch host {
	case "metadata.google.internal", "metadata.internal":
		return true
	default:
		return false
	}
}

func validDNS(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func cleanID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

func hostIsAddr(host string, addr netip.Addr) bool {
	parsed, err := netip.ParseAddr(host)
	return err == nil && parsed == addr
}

func hostListed(hosts []string, host string) bool {
	for _, item := range hosts {
		if item == host {
			return true
		}
	}
	return false
}

func mustPrefix(s string) netip.Prefix {
	return netip.MustParsePrefix(s)
}

var hardPrefixes = []netip.Prefix{
	mustPrefix("0.0.0.0/8"),
	mustPrefix("127.0.0.0/8"),
	mustPrefix("169.254.0.0/16"),
	mustPrefix("192.0.0.0/24"),
	mustPrefix("192.0.2.0/24"),
	mustPrefix("192.88.99.0/24"),
	mustPrefix("198.18.0.0/15"),
	mustPrefix("198.51.100.0/24"),
	mustPrefix("203.0.113.0/24"),
	mustPrefix("224.0.0.0/4"),
	mustPrefix("240.0.0.0/4"),
	mustPrefix("::/128"),
	mustPrefix("::1/128"),
	// Deprecated IPv4-compatible range (::a.b.c.d). Distinct from IPv4-mapped (::ffff:0:0/96).
	mustPrefix("::/96"),
	mustPrefix("::ffff:0:0/96"),
	mustPrefix("100::/64"),
	mustPrefix("2001::/32"),
	mustPrefix("2001:db8::/32"),
	mustPrefix("2002::/16"),
	mustPrefix("64:ff9b::/96"),
	mustPrefix("64:ff9b:1::/48"),
	mustPrefix("fe80::/10"),
	mustPrefix("fec0::/10"),
	mustPrefix("ff00::/8"),
}

var cgnatPrefixes = []netip.Prefix{
	mustPrefix("100.64.0.0/10"),
}

var grantSupernets = []netip.Prefix{
	mustPrefix("10.0.0.0/8"),
	mustPrefix("172.16.0.0/12"),
	mustPrefix("192.168.0.0/16"),
	mustPrefix("100.64.0.0/10"),
	mustPrefix("fc00::/7"),
}
