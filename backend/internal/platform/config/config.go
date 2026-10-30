// Package config loads process settings from the environment.
// Failures name the variable and never repeat the supplied value.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"chatisp/internal/platform/crypto"
)

// Mode is a long-running or one-shot process mode.
type Mode string

const (
	ModeAPI     Mode = "api"
	ModeWorker  Mode = "worker"
	ModeMigrate Mode = "migrate"
	ModeAll     Mode = "all"
)

// Config is the validated runtime configuration.
type Config struct {
	Env                 string        `env:"CHATISP_ENV,required,notEmpty"`
	HTTPAddr            string        `env:"CHATISP_HTTP_ADDR" envDefault:"127.0.0.1:8080"`
	MetricsAddr         string        `env:"CHATISP_METRICS_ADDR" envDefault:"127.0.0.1:9090"`
	DatabaseURL         string        `env:"CHATISP_DATABASE_URL"`
	AppDatabaseURL      string        `env:"CHATISP_APP_DATABASE_URL"`
	PlatformDatabaseURL string        `env:"CHATISP_PLATFORM_DATABASE_URL"`
	IngressDatabaseURL  string        `env:"CHATISP_INGRESS_DATABASE_URL"`
	JobsDatabaseURL     string        `env:"CHATISP_JOBS_DATABASE_URL"`
	LogLevel            string        `env:"CHATISP_LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout     time.Duration `env:"CHATISP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	MigrationsDir       string        `env:"CHATISP_MIGRATIONS_DIR" envDefault:"db/migrations"`
	SMTPAddr            string        `env:"CHATISP_SMTP_ADDR"`
	SMTPUsername        string        `env:"CHATISP_SMTP_USERNAME"`
	SMTPPassword        string        `env:"CHATISP_SMTP_PASSWORD"`
	SMTPFrom            string        `env:"CHATISP_SMTP_FROM"`
	PanelOriginsRaw     string        `env:"CHATISP_PANEL_ORIGINS"`
	PanelOrigins        []string      `env:"-"`
	DataKeys            string        `env:"CHATISP_DATA_KEYS"`
	DataKeysFile        string        `env:"CHATISP_DATA_KEYS_FILE"`
	DataKeyVersion      string        `env:"CHATISP_DATA_KEY_VERSION"`
	KeyRing             *crypto.Ring  `env:"-"`
	MetaSimulation      bool          `env:"CHATISP_META_SIMULATION"`
	MetaStandInURL      string        `env:"CHATISP_META_STANDIN_URL"`
	MetaVerifyToken     string        `env:"CHATISP_META_VERIFY_TOKEN"`
	MetaAppID           string        `env:"CHATISP_META_APP_ID"`
}

// Load reads environ as KEY=VALUE entries and ignores the process environment.
func Load(environ []string) (Config, error) {
	return load(environ, true)
}

// LoadRuntime loads configuration without requiring the migration owner DSN.
func LoadRuntime(environ []string) (Config, error) {
	return load(environ, false)
}

func load(environ []string, requireOwner bool) (Config, error) {
	values := make(map[string]string, len(environ))
	for _, item := range environ {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		values[key] = value
	}
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: values}); err != nil {
		if requireOwner && strings.TrimSpace(values["CHATISP_DATABASE_URL"]) == "" {
			return Config{}, errors.Join(formatConfigError([]string{"CHATISP_DATABASE_URL"}, nil), explainEnvError(err))
		}
		return Config{}, explainEnvError(err)
	}
	if err := cfg.validate(requireOwner); err != nil {
		return Config{}, err
	}
	origins, ok := parsePanelOrigins(cfg.PanelOriginsRaw)
	if !ok {
		return Config{}, formatConfigError(nil, []string{"CHATISP_PANEL_ORIGINS"})
	}
	cfg.PanelOrigins = origins
	if strings.TrimSpace(cfg.DataKeys) != "" || strings.TrimSpace(cfg.DataKeysFile) != "" {
		ring, err := crypto.Load(cfg.DataKeys, cfg.DataKeysFile, cfg.DataKeyVersion)
		if err != nil {
			name := "CHATISP_DATA_KEYS"
			if strings.TrimSpace(cfg.DataKeysFile) != "" {
				name = "CHATISP_DATA_KEYS_FILE"
			}
			return Config{}, formatConfigError(nil, []string{name})
		}
		cfg.KeyRing = ring
	}
	return cfg, nil
}

// Validate checks semantic rules. Error text contains variable names only.
func (c Config) Validate() error {
	return c.validate(true)
}

func (c Config) validate(requireOwner bool) error {
	var missing []string
	var invalid []string

	switch c.Env {
	case "":
		missing = append(missing, "CHATISP_ENV")
	case "development", "test", "production":
	default:
		invalid = append(invalid, "CHATISP_ENV")
	}

	if strings.TrimSpace(c.DatabaseURL) == "" && requireOwner {
		missing = append(missing, "CHATISP_DATABASE_URL")
	} else if strings.TrimSpace(c.DatabaseURL) != "" && !validDatabaseURL(c.DatabaseURL) {
		invalid = append(invalid, "CHATISP_DATABASE_URL")
	}

	if !validListenAddr(c.HTTPAddr) {
		invalid = append(invalid, "CHATISP_HTTP_ADDR")
	}
	if !validListenAddr(c.MetricsAddr) {
		invalid = append(invalid, "CHATISP_METRICS_ADDR")
	}
	if validListenAddr(c.HTTPAddr) && validListenAddr(c.MetricsAddr) && sameFixedAddr(c.HTTPAddr, c.MetricsAddr) {
		invalid = append(invalid, "CHATISP_HTTP_ADDR", "CHATISP_METRICS_ADDR")
	}

	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "debug", "info", "warn", "error":
	default:
		invalid = append(invalid, "CHATISP_LOG_LEVEL")
	}

	if c.ShutdownTimeout < time.Second || c.ShutdownTimeout > 2*time.Minute {
		invalid = append(invalid, "CHATISP_SHUTDOWN_TIMEOUT")
	}

	if strings.TrimSpace(c.MigrationsDir) == "" || strings.ContainsRune(c.MigrationsDir, 0) || len(c.MigrationsDir) > 4096 {
		invalid = append(invalid, "CHATISP_MIGRATIONS_DIR")
	}

	appSet := strings.TrimSpace(c.AppDatabaseURL) != ""
	platformSet := strings.TrimSpace(c.PlatformDatabaseURL) != ""
	if appSet != platformSet {
		if !appSet {
			invalid = append(invalid, "CHATISP_APP_DATABASE_URL")
		}
		if !platformSet {
			invalid = append(invalid, "CHATISP_PLATFORM_DATABASE_URL")
		}
	}
	if appSet && !validDatabaseURL(c.AppDatabaseURL) {
		invalid = append(invalid, "CHATISP_APP_DATABASE_URL")
	}
	if platformSet && !validDatabaseURL(c.PlatformDatabaseURL) {
		invalid = append(invalid, "CHATISP_PLATFORM_DATABASE_URL")
	}
	if strings.TrimSpace(c.IngressDatabaseURL) != "" && !validDatabaseURL(c.IngressDatabaseURL) {
		invalid = append(invalid, "CHATISP_INGRESS_DATABASE_URL")
	}
	if strings.TrimSpace(c.JobsDatabaseURL) != "" && !validDatabaseURL(c.JobsDatabaseURL) {
		invalid = append(invalid, "CHATISP_JOBS_DATABASE_URL")
	}

	if c.Env == "production" {
		if c.MetaSimulation {
			invalid = append(invalid, "CHATISP_META_SIMULATION")
		}
		if strings.TrimSpace(c.MetaStandInURL) != "" {
			invalid = append(invalid, "CHATISP_META_STANDIN_URL")
		}
	}
	if strings.TrimSpace(c.MetaStandInURL) != "" && !validStandInURL(c.MetaStandInURL) {
		invalid = append(invalid, "CHATISP_META_STANDIN_URL")
	}

	if !noControlChars(c.SMTPAddr) || len(c.SMTPAddr) > 256 {
		invalid = append(invalid, "CHATISP_SMTP_ADDR")
	}
	if !noControlChars(c.SMTPUsername) || len(c.SMTPUsername) > 256 {
		invalid = append(invalid, "CHATISP_SMTP_USERNAME")
	}
	if !noControlChars(c.SMTPPassword) || len(c.SMTPPassword) > 512 {
		invalid = append(invalid, "CHATISP_SMTP_PASSWORD")
	}
	if !noControlChars(c.SMTPFrom) || len(c.SMTPFrom) > 320 {
		invalid = append(invalid, "CHATISP_SMTP_FROM")
	}
	smtpTouched := c.SMTPAddr != "" || c.SMTPUsername != "" || c.SMTPPassword != "" || c.SMTPFrom != ""
	if smtpTouched {
		if strings.TrimSpace(c.SMTPAddr) == "" || !validListenAddr(c.SMTPAddr) {
			invalid = append(invalid, "CHATISP_SMTP_ADDR")
		}
		if !validMailbox(c.SMTPFrom) {
			invalid = append(invalid, "CHATISP_SMTP_FROM")
		}
		if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
			invalid = append(invalid, "CHATISP_SMTP_USERNAME", "CHATISP_SMTP_PASSWORD")
		}
	}
	if _, ok := parsePanelOrigins(c.PanelOriginsRaw); !ok {
		invalid = append(invalid, "CHATISP_PANEL_ORIGINS")
	}
	keyMissing, keyInvalid := c.keyProblem()
	if keyMissing != "" {
		missing = append(missing, keyMissing)
	}
	invalid = append(invalid, keyInvalid...)

	return formatConfigError(missing, invalid)
}

func (c Config) keyProblem() (string, []string) {
	fileSet := strings.TrimSpace(c.DataKeysFile) != ""
	envSet := strings.TrimSpace(c.DataKeys) != ""
	versionSet := strings.TrimSpace(c.DataKeyVersion) != ""
	if fileSet && envSet {
		return "", []string{"CHATISP_DATA_KEYS", "CHATISP_DATA_KEYS_FILE"}
	}
	if !fileSet && !envSet {
		if versionSet {
			return "", []string{"CHATISP_DATA_KEY_VERSION"}
		}
		if c.Env == "production" {
			return "CHATISP_DATA_KEYS", nil
		}
		return "", nil
	}
	if !versionSet {
		return "", []string{"CHATISP_DATA_KEY_VERSION"}
	}
	if _, err := crypto.Load(c.DataKeys, c.DataKeysFile, c.DataKeyVersion); err != nil {
		if fileSet {
			return "", []string{"CHATISP_DATA_KEYS_FILE"}
		}
		return "", []string{"CHATISP_DATA_KEYS"}
	}
	return "", nil
}

func noControlChars(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00")
}

func validMailbox(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 320 || strings.ContainsAny(value, " \t\r\n<>") {
		return false
	}
	at := strings.IndexByte(value, '@')
	return at > 0 && at == strings.LastIndexByte(value, '@') && at < len(value)-1
}

func parsePanelOrigins(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		parsed, err := url.Parse(part)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return nil, false
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return nil, false
		}
		if parsed.Hostname() == "" {
			return nil, false
		}
		out = append(out, parsed.Scheme+"://"+parsed.Host)
	}
	return out, true
}

func validDatabaseURL(raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return false
	}
	return parsed.Host != ""
}

func validStandInURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Hostname() == "graph.facebook.com" || strings.HasSuffix(parsed.Hostname(), ".facebook.com") {
		return false
	}
	if !isLoopbackHost(parsed.Host) {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.Path, "..") || strings.Contains(parsed.Path, "\\") {
		return false
	}
	return true
}

func isLoopbackHost(hostport string) bool {
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

func validListenAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return false
	}
	if host == "" {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 || strings.ContainsAny(host, " \t/@") {
		return false
	}
	return true
}

func sameFixedAddr(left, right string) bool {
	if left != right {
		return false
	}
	_, port, err := net.SplitHostPort(left)
	if err != nil {
		return true
	}
	// Port 0 asks the kernel for an ephemeral port, so two listeners may share the text.
	return port != "0"
}

func explainEnvError(err error) error {
	var missing []string
	var invalid []string
	unknown := false
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		switch typed := current.(type) {
		case env.AggregateError:
			for _, child := range typed.Errors {
				walk(child)
			}
		case *env.AggregateError:
			if typed != nil {
				for _, child := range typed.Errors {
					walk(child)
				}
			}
		case env.VarIsNotSetError:
			missing = append(missing, typed.Key)
		case env.EmptyVarError:
			missing = append(missing, typed.Key)
		case env.ParseError:
			invalid = append(invalid, envName(typed.Name))
		case env.LoadFileContentError:
			if typed.Key != "" {
				invalid = append(invalid, typed.Key)
			} else {
				unknown = true
			}
		default:
			unknown = true
		}
	}
	walk(err)
	if unknown && len(missing) == 0 && len(invalid) == 0 {
		invalid = append(invalid, "desconhecida")
	}
	explained := formatConfigError(missing, invalid)
	if explained == nil {
		return errors.New("configuração inválida")
	}
	return explained
}

func envName(field string) string {
	names := map[string]string{
		"Env":                 "CHATISP_ENV",
		"HTTPAddr":            "CHATISP_HTTP_ADDR",
		"MetricsAddr":         "CHATISP_METRICS_ADDR",
		"DatabaseURL":         "CHATISP_DATABASE_URL",
		"AppDatabaseURL":      "CHATISP_APP_DATABASE_URL",
		"PlatformDatabaseURL": "CHATISP_PLATFORM_DATABASE_URL",
		"IngressDatabaseURL":  "CHATISP_INGRESS_DATABASE_URL",
		"JobsDatabaseURL":     "CHATISP_JOBS_DATABASE_URL",
		"LogLevel":            "CHATISP_LOG_LEVEL",
		"ShutdownTimeout":     "CHATISP_SHUTDOWN_TIMEOUT",
		"MigrationsDir":       "CHATISP_MIGRATIONS_DIR",
		"SMTPAddr":            "CHATISP_SMTP_ADDR",
		"SMTPUsername":        "CHATISP_SMTP_USERNAME",
		"SMTPPassword":        "CHATISP_SMTP_PASSWORD",
		"SMTPFrom":            "CHATISP_SMTP_FROM",
		"PanelOriginsRaw":     "CHATISP_PANEL_ORIGINS",
		"DataKeys":            "CHATISP_DATA_KEYS",
		"DataKeysFile":        "CHATISP_DATA_KEYS_FILE",
		"DataKeyVersion":      "CHATISP_DATA_KEY_VERSION",
		"MetaSimulation":      "CHATISP_META_SIMULATION",
		"MetaStandInURL":      "CHATISP_META_STANDIN_URL",
		"MetaVerifyToken":     "CHATISP_META_VERIFY_TOKEN",
		"MetaAppID":           "CHATISP_META_APP_ID",
	}
	if name, ok := names[field]; ok {
		return name
	}
	return "desconhecida"
}

func formatConfigError(missing, invalid []string) error {
	missing = uniqueSorted(missing)
	invalid = uniqueSorted(invalid)
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "configuração obrigatória ausente: "+strings.Join(missing, ", "))
	}
	if len(invalid) > 0 {
		parts = append(parts, "configuração inválida: "+strings.Join(invalid, ", "))
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	return slices.Compact(values)
}

// ParseMode accepts the long-running and migration modes.
func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case ModeAPI, ModeWorker, ModeMigrate, ModeAll:
		return Mode(value), nil
	default:
		return "", fmt.Errorf("modo desconhecido: use api, worker, migrate ou all")
	}
}
