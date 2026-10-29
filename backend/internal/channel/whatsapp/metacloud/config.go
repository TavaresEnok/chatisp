package metacloud

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ModeOfficial is the real Graph host. Activation does not dial it.
	ModeOfficial = "official"
	// ModeSimulation is an unambiguous local stand-in.
	ModeSimulation = "simulation"

	// OfficialHost is the only host accepted for a live Graph endpoint.
	OfficialHost = "graph.facebook.com"
	// PrimaryDocHost is the only host accepted as a version source.
	PrimaryDocHost = "developers.facebook.com"

	// RejectionTokenInvalid is a stable code for channel degradation.
	// It is not a Graph message and it never contains the token.
	RejectionTokenInvalid = "token_invalid"

	// MaxTextRunes is the Cloud API text body limit documented for messages.
	MaxTextRunes = 4096
	// MaxCallbackBytes is the documented biz_opaque_callback_data limit.
	MaxCallbackBytes = 512
	// MaxRawBytes is the webhook body this decoder will expand.
	MaxRawBytes = 1 << 20

	// maxRequestTimeout caps a send whose context has no deadline.
	// Callers should still set a deadline. A timeout after the request is
	// written stays uncertain, the same as a caller deadline.
	maxRequestTimeout = 30 * time.Second
)

// Config is the explicit endpoint configuration.
// GraphVersion is never defaulted. Official activation also requires the
// primary documentation URL that was used to accept that version. This
// package does not fetch that URL.
type Config struct {
	Mode          string
	GraphVersion  string
	PrimaryDocURL string
	PhoneNumberID string
	AccessToken   string
	// BaseURL is required for simulation and must be a loopback URL.
	// Official mode leaves it empty or uses https://graph.facebook.com.
	BaseURL    string
	HTTPClient *http.Client
}

type endpoint struct {
	mode       string
	version    string
	phoneID    string
	token      string
	base       *url.URL
	simulation bool
	client     *http.Client
}

func activate(cfg Config) (endpoint, error) {
	version, err := parseVersion(cfg.GraphVersion)
	if err != nil {
		return endpoint{}, err
	}
	phone, err := parsePhoneID(cfg.PhoneNumberID)
	if err != nil {
		return endpoint{}, err
	}
	token, err := parseAccessToken(cfg.AccessToken)
	if err != nil {
		return endpoint{}, err
	}
	switch cfg.Mode {
	case ModeOfficial:
		base, err := officialBase(cfg.BaseURL)
		if err != nil {
			return endpoint{}, err
		}
		if err := parsePrimaryDoc(cfg.PrimaryDocURL); err != nil {
			return endpoint{}, err
		}
		return endpoint{
			mode: ModeOfficial, version: version, phoneID: phone, token: token,
			base: base, simulation: false, client: newClient(cfg.HTTPClient),
		}, nil
	case ModeSimulation:
		base, err := simulationBase(cfg.BaseURL)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{
			mode: ModeSimulation, version: version, phoneID: phone, token: token,
			base: base, simulation: true, client: newClient(cfg.HTTPClient),
		}, nil
	default:
		return endpoint{}, errMode
	}
}

func parseVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errVersionMissing
	}
	if len(value) < 4 || len(value) > 16 || value[0] != 'v' {
		return "", errVersionInvalid
	}
	dot := strings.IndexByte(value, '.')
	if dot <= 1 || dot == len(value)-1 || strings.Count(value, ".") != 1 {
		return "", errVersionInvalid
	}
	major, minor := value[1:dot], value[dot+1:]
	if !digits(major) || !digits(minor) || major == "0" {
		return "", errVersionInvalid
	}
	return value, nil
}

func parsePhoneID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errPhoneMissing
	}
	if len(value) > 32 || !digits(value) {
		return "", errPhoneInvalid
	}
	return value, nil
}

func parseAccessToken(value string) (string, error) {
	if value == "" {
		return "", errTokenMissing
	}
	if len(value) > 4096 {
		return "", errTokenInvalid
	}
	for _, r := range value {
		if r <= 0x20 || r >= 0x7f {
			return "", errTokenInvalid
		}
	}
	return value, nil
}

func parsePrimaryDoc(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errDocMissing
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != PrimaryDocHost || u.User != nil {
		return errDocInvalid
	}
	if u.Path == "" || u.Path == "/" || strings.Contains(u.Path, "..") {
		return errDocInvalid
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return errDocInvalid
	}
	return nil
}

func officialBase(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return &url.URL{Scheme: "https", Host: OfficialHost}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != OfficialHost || u.Port() != "" || u.User != nil {
		return nil, errOfficialEndpoint
	}
	if u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errOfficialEndpoint
	}
	return &url.URL{Scheme: "https", Host: OfficialHost}, nil
}

func simulationBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errSimulationEndpoint
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errSimulationEndpoint
	}
	if u.Hostname() == OfficialHost || strings.HasSuffix(u.Hostname(), ".facebook.com") {
		return nil, errSimulationEndpoint
	}
	if !loopback(u.Host) {
		return nil, errSimulationEndpoint
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "\\") {
		return nil, errSimulationEndpoint
	}
	path := strings.TrimRight(u.Path, "/")
	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: path}, nil
}

func loopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (e endpoint) messageURL() (*url.URL, error) {
	if e.base == nil || e.version == "" || e.phoneID == "" {
		return nil, errMode
	}
	u := *e.base
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(e.base.Path, "/") + "/" + e.version + "/" + e.phoneID + "/messages"
	if e.simulation {
		if !loopback(u.Host) || u.Hostname() == OfficialHost {
			return nil, errSimulationEndpoint
		}
		return &u, nil
	}
	if u.Scheme != "https" || u.Hostname() != OfficialHost {
		return nil, errOfficialEndpoint
	}
	return &u, nil
}

func newClient(base *http.Client) *http.Client {
	var rt http.RoundTripper
	if base != nil && base.Transport != nil {
		rt = base.Transport
	}
	if t, ok := rt.(*http.Transport); ok {
		cloned := t.Clone()
		cloned.Proxy = nil
		rt = cloned
	} else if rt == nil {
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			cloned := def.Clone()
			cloned.Proxy = nil
			rt = cloned
		} else {
			rt = &http.Transport{Proxy: nil}
		}
	}
	timeout := maxRequestTimeout
	if base != nil && base.Timeout > 0 && base.Timeout < timeout {
		timeout = base.Timeout
	}
	return &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Timeout is the backstop when ctx has no deadline. It cannot be
		// zero: a caller without a deadline must not block on the response.
		Timeout: timeout,
	}
}

// Clock supplies ingestion time. The decoder does not call time.Now.
type Clock func() time.Time
