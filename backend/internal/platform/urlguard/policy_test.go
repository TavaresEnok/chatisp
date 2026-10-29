package urlguard

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestInspectLiteralAndName(t *testing.T) {
	p := mustPolicy(t)
	target, err := p.Inspect(Scope{}, "https://1.1.1.1/health")
	if err != nil {
		t.Fatal(err)
	}
	if !target.Literal || target.Port != 443 || target.Addr.String() != "1.1.1.1" || target.URL.Host != "1.1.1.1" {
		t.Fatalf("target %+v", target)
	}
	if target.URL.User != nil || strings.Contains(target.URL.String(), "@") {
		t.Fatal(target.URL)
	}

	target, err = p.Inspect(Scope{}, "http://[2606:4700:4700::1111]/v")
	if err != nil {
		t.Fatal(err)
	}
	if target.Port != 80 || target.URL.Host != "[2606:4700:4700::1111]" {
		t.Fatalf("target %+v", target)
	}

	target, err = p.Inspect(Scope{}, "http://Example.TEST./a")
	if err != nil {
		t.Fatal(err)
	}
	if target.Literal || target.Host != "example.test" || target.Port != 80 {
		t.Fatalf("target %+v", target)
	}
}

func TestInspectRejectsUnsafeURL(t *testing.T) {
	p := mustPolicy(t)
	cases := []struct {
		raw  string
		want error
	}{
		{"file:///etc/passwd", ErrScheme},
		{"gopher://1.1.1.1/", ErrScheme},
		{"ftp://1.1.1.1/", ErrScheme},
		{"dict://1.1.1.1/", ErrScheme},
		{"ldap://1.1.1.1/", ErrScheme},
		{"ws://1.1.1.1/", ErrScheme},
		{"javascript:alert(1)", ErrScheme},
		{"http://user:s3cret@1.1.1.1/x", ErrUserinfo},
		{"https://alice:s3cret@example.test/x", ErrUserinfo},
		{"http://1.1.1.1:22/", ErrPort},
		{"http://1.1.1.1:8080/", ErrPort},
		{"http://1.1.1.1:443/", ErrPort},
		{"https://1.1.1.1:80/", ErrPort},
		{"https://1.1.1.1:8443/", ErrPort},
		{"http://1.1.1.1:0/", ErrPort},
		{"http://1.1.1.1:65536/", ErrPort},
		{"http://127.0.0.1/", ErrDestination},
		{"http://127.0.0.2/", ErrDestination},
		{"http://[::1]/", ErrDestination},
		{"http://0.0.0.0/", ErrDestination},
		{"http://[::]/", ErrDestination},
		{"http://169.254.169.254/latest/meta-data", ErrDestination},
		{"http://169.254.1.1/", ErrDestination},
		{"http://[fe80::1]/", ErrDestination},
		{"http://10.1.2.3/", ErrDestination},
		{"http://172.16.0.1/", ErrDestination},
		{"http://192.168.1.1/", ErrDestination},
		{"http://[fd00::1]/", ErrDestination},
		{"http://100.64.0.1/", ErrDestination},
		{"http://[::ffff:8.8.8.8]/", ErrDestination},
		{"http://[::ffff:10.0.0.1]/", ErrDestination},
		{"http://[::ffff:127.0.0.1]/", ErrDestination},
		{"http://[::FFFF:169.254.169.254]/", ErrDestination},
		{"http://192.0.2.1/", ErrDestination},
		{"http://198.51.100.1/", ErrDestination},
		{"http://203.0.113.1/", ErrDestination},
		{"http://[2001:db8::1]/", ErrDestination},
		{"http://198.18.0.1/", ErrDestination},
		{"http://240.0.0.1/", ErrDestination},
		{"http://0.1.2.3/", ErrDestination},
		{"http://224.0.0.1/", ErrDestination},
		{"http://255.255.255.255/", ErrDestination},
		{"http://localhost/", ErrDestination},
		{"http://localhost./", ErrDestination},
		{"http://api.localhost/", ErrDestination},
		{"http://metadata.google.internal/", ErrDestination},
		{"http://2130706433/", ErrDestination},
		{"http://0x7f000001/", ErrDestination},
		{"http://127.1/", ErrDestination},
		{"http://127.0.0.01/", ErrDestination},
		{"http://0177.0.0.1/", ErrDestination},
		{"http://8.8.8/", ErrDestination},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			_, err := p.Inspect(Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}, tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "://")) {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectDNSSetIsConservative(t *testing.T) {
	p := mustPolicy(t)
	public := netip.MustParseAddr("1.2.3.4")
	other := netip.MustParseAddr("8.8.8.8")
	got, err := p.Select(Scope{}, "example.test", []netip.Addr{public, other})
	if err != nil || got != public {
		t.Fatalf("got %v %v", got, err)
	}

	sets := [][]netip.Addr{
		{public, netip.MustParseAddr("10.0.0.1")},
		{public, netip.MustParseAddr("127.0.0.1")},
		{public, netip.MustParseAddr("169.254.169.254")},
		{public, netip.MustParseAddr("::ffff:8.8.8.8")},
		{netip.MustParseAddr("::ffff:1.2.3.4")},
		nil,
	}
	for _, set := range sets {
		_, err := p.Select(Scope{}, "example.test", set)
		if len(set) <= 1 {
			if err == nil {
				t.Fatal("expected reject")
			}
			continue
		}
		if !errors.Is(err, ErrDNS) {
			t.Fatalf("set %v got %v", set, err)
		}
	}
	_, err = p.Select(Scope{}, "localhost", []netip.Addr{public})
	if !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
}

func TestPrivateGrantStaysInsideScope(t *testing.T) {
	prefix := netip.MustParsePrefix("10.8.0.0/16")
	p := mustPolicy(t, WithGrant(Grant{
		TenantID:     "tenant-a",
		ConnectionID: "conn-1",
		Hosts:        []string{"ERP.Internal."},
		Prefixes:     []netip.Prefix{prefix},
	}))
	ip := netip.MustParseAddr("10.8.1.4")
	outside := netip.MustParseAddr("10.9.0.1")
	allowed := Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}

	got, err := p.Select(allowed, "erp.internal", []netip.Addr{ip})
	if err != nil || got != ip {
		t.Fatalf("grant %v %v", got, err)
	}
	if _, err := p.Inspect(allowed, "http://erp.internal/orders"); err != nil {
		t.Fatal(err)
	}
	denied := []struct {
		scope Scope
		host  string
		addrs []netip.Addr
	}{
		{Scope{TenantID: "tenant-b", ConnectionID: "conn-1"}, "erp.internal", []netip.Addr{ip}},
		{Scope{TenantID: "tenant-a", ConnectionID: "conn-2"}, "erp.internal", []netip.Addr{ip}},
		{Scope{}, "erp.internal", []netip.Addr{ip}},
		{allowed, "other.internal", []netip.Addr{ip}},
		{allowed, "erp.internal", []netip.Addr{ip, outside}},
		{allowed, "10.8.1.4", []netip.Addr{ip}},
	}
	for _, tc := range denied {
		_, err := p.Select(tc.scope, tc.host, tc.addrs)
		if err == nil {
			t.Fatalf("inherited %s %s", tc.scope.TenantID, tc.host)
		}
	}

	literal := mustPolicy(t, WithGrant(Grant{
		TenantID:     "tenant-a",
		ConnectionID: "conn-1",
		Prefixes:     []netip.Prefix{netip.MustParsePrefix("10.1.2.3/32"), netip.MustParsePrefix("fd00::/64")},
	}))
	scope := Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}
	if _, err := literal.Inspect(scope, "http://10.1.2.3/erp"); err != nil {
		t.Fatal(err)
	}
	if _, err := literal.Inspect(scope, "http://[fd00::1]/erp"); err != nil {
		t.Fatal(err)
	}
	if _, err := literal.Inspect(Scope{TenantID: "tenant-a", ConnectionID: "conn-9"}, "http://10.1.2.3/erp"); !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if _, err := literal.Inspect(scope, "http://[::ffff:10.1.2.3]/erp"); !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if _, err := literal.Inspect(scope, "http://127.0.0.1/"); !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
	if _, err := literal.Inspect(scope, "http://169.254.169.254/"); !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}
}

func TestGrantRejectsGlobalException(t *testing.T) {
	cases := []Grant{
		{},
		{TenantID: "tenant-a", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
		{ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1"},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("::/0")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/7")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("8.8.8.8/32")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Hosts: []string{"localhost"}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Hosts: []string{"*"}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.1.2.3/16")}},
	}
	for _, g := range cases {
		_, err := NewPolicy(WithGrant(g))
		if !errors.Is(err, ErrGrant) {
			t.Fatalf("grant %+v got %v", g, err)
		}
	}
	if _, err := NewPolicy(WithMaxRedirects(-1)); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := NewPolicy(nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

func TestIPv4CompatibleRangeRejected(t *testing.T) {
	p := mustPolicy(t, WithGrant(Grant{
		TenantID:     "tenant-a",
		ConnectionID: "conn-1",
		Hosts:        []string{"erp.internal"},
		Prefixes:     []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/64")},
	}))
	scope := Scope{TenantID: "tenant-a", ConnectionID: "conn-1"}
	literals := []string{
		"http://[::7f00:1]/",
		"http://[::7F00:1]/",
		"http://[0:0:0:0:0:0:7f00:1]/",
		"http://[0000:0000:0000:0000:0000:0000:7f00:0001]/",
		"http://[::127.0.0.1]/",
		"http://[0:0:0:0:0:0:127.0.0.1]/",
		"http://[::8.8.8.8]/",
		"http://[::808:808]/",
		"http://[::10.0.0.1]/",
		"http://[::a00:1]/",
		"http://[::2]/",
	}
	for _, raw := range literals {
		t.Run(raw, func(t *testing.T) {
			_, err := p.Inspect(scope, raw)
			if !errors.Is(err, ErrDestination) {
				t.Fatalf("got %v", err)
			}
			_, err = p.Inspect(Scope{}, raw)
			if !errors.Is(err, ErrDestination) {
				t.Fatalf("empty scope got %v", err)
			}
		})
	}

	compat := []netip.Addr{
		netip.MustParseAddr("::7f00:1"),
		netip.MustParseAddr("::127.0.0.1"),
		netip.MustParseAddr("::8.8.8.8"),
		netip.MustParseAddr("::10.0.0.1"),
		netip.MustParseAddr("::2"),
	}
	for _, addr := range compat {
		_, err := p.Select(scope, "erp.internal", []netip.Addr{addr})
		if !errors.Is(err, ErrDestination) {
			t.Fatalf("grant select %s got %v", addr, err)
		}
		_, err = p.Select(Scope{}, "example.test", []netip.Addr{addr})
		if !errors.Is(err, ErrDestination) {
			t.Fatalf("dns %s got %v", addr, err)
		}
		_, err = p.Select(Scope{TenantID: "tenant-b", ConnectionID: "conn-1"}, "erp.internal", []netip.Addr{addr})
		if !errors.Is(err, ErrDestination) {
			t.Fatalf("other tenant %s got %v", addr, err)
		}
	}

	public := netip.MustParseAddr("1.2.3.4")
	global6 := netip.MustParseAddr("2606:4700:4700::1111")
	private := netip.MustParseAddr("10.1.2.3")
	mixed := [][]netip.Addr{
		{public, netip.MustParseAddr("::7f00:1")},
		{global6, netip.MustParseAddr("::8.8.8.8")},
		{private, netip.MustParseAddr("::10.0.0.1")},
		{public, global6, netip.MustParseAddr("::127.0.0.1")},
	}
	for _, set := range mixed {
		_, err := p.Select(scope, "erp.internal", set)
		if !errors.Is(err, ErrDNS) {
			t.Fatalf("mixed grant %v got %v", set, err)
		}
		_, err = p.Select(scope, "example.test", set)
		if !errors.Is(err, ErrDNS) {
			t.Fatalf("mixed name %v got %v", set, err)
		}
	}

	if _, err := p.Inspect(scope, "https://[2606:4700:4700::1111]/v"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Select(scope, "example.test", []netip.Addr{global6})
	if err != nil || got != global6 {
		t.Fatalf("global %v %v", got, err)
	}
	got, err = p.Select(Scope{}, "example.test", []netip.Addr{public, global6})
	if err != nil || got != public {
		t.Fatalf("pair %v %v", got, err)
	}
	if _, err := p.Inspect(scope, "http://[::ffff:8.8.8.8]/"); !errors.Is(err, ErrDestination) {
		t.Fatal(err)
	}

	denied := []Grant{
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("::/96")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("::7f00:1/128")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("::8.8.8.8/128")}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Hosts: []string{"::7f00:1"}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Hosts: []string{"::127.0.0.1"}},
		{TenantID: "tenant-a", ConnectionID: "conn-1", Prefixes: []netip.Prefix{netip.MustParsePrefix("fc00::/7")}, Hosts: []string{"::8.8.8.8"}},
	}
	for _, g := range denied {
		_, err := NewPolicy(WithGrant(g))
		if !errors.Is(err, ErrGrant) {
			t.Fatalf("grant %+v got %v", g, err)
		}
	}
}

func mustPolicy(t *testing.T, opts ...Option) *Policy {
	t.Helper()
	p, err := NewPolicy(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
