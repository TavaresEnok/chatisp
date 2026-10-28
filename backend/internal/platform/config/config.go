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
	Env             string        `env:"CHATISP_ENV,required,notEmpty"`
	HTTPAddr        string        `env:"CHATISP_HTTP_ADDR" envDefault:"127.0.0.1:8080"`
	MetricsAddr     string        `env:"CHATISP_METRICS_ADDR" envDefault:"127.0.0.1:9090"`
	DatabaseURL     string        `env:"CHATISP_DATABASE_URL,required,notEmpty"`
	LogLevel        string        `env:"CHATISP_LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"CHATISP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	MigrationsDir   string        `env:"CHATISP_MIGRATIONS_DIR" envDefault:"db/migrations"`
}

// Load reads environ as KEY=VALUE entries and ignores the process environment.
func Load(environ []string) (Config, error) {
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
		return Config{}, explainEnvError(err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks semantic rules. Error text contains variable names only.
func (c Config) Validate() error {
	var missing []string
	var invalid []string

	switch c.Env {
	case "":
		missing = append(missing, "CHATISP_ENV")
	case "development", "test", "production":
	default:
		invalid = append(invalid, "CHATISP_ENV")
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		missing = append(missing, "CHATISP_DATABASE_URL")
	} else if !validDatabaseURL(c.DatabaseURL) {
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

	return formatConfigError(missing, invalid)
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
		"Env":             "CHATISP_ENV",
		"HTTPAddr":        "CHATISP_HTTP_ADDR",
		"MetricsAddr":     "CHATISP_METRICS_ADDR",
		"DatabaseURL":     "CHATISP_DATABASE_URL",
		"LogLevel":        "CHATISP_LOG_LEVEL",
		"ShutdownTimeout": "CHATISP_SHUTDOWN_TIMEOUT",
		"MigrationsDir":   "CHATISP_MIGRATIONS_DIR",
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
