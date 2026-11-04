package outwebhook

import (
	"net"
	"net/url"

	"chatisp/internal/platform/urlguard"
)

// AllowDestination asks urlguard before a subscription is stored or a delivery
// is attempted. Production refuses private and loopback addresses. Test and
// development still refuse private addresses, and allow an HTTP(S) loopback
// literal on any port so a local receiver can run.
func AllowDestination(policy *urlguard.Policy, mode, raw string) error {
	if policy == nil {
		return urlguard.ErrConfig
	}
	_, err := policy.Inspect(urlguard.Scope{}, raw)
	if err == nil {
		return nil
	}
	if mode != "test" && mode != "development" {
		return err
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil || parsed == nil || parsed.User != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return err
	}
	if parsed.Hostname() == "" || parsed.Fragment != "" {
		return err
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return err
	}
	return nil
}
