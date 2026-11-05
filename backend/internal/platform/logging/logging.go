// Package logging writes JSON logs and removes values that could carry secrets.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog logger. Sensitive keys and values are replaced
// with "[redacted]" before they are written.
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       ParseLevel(level),
		ReplaceAttr: redact,
	})
	return slog.New(tenantHandler{handler})
}

type tenantKey struct{}

// WithTenant returns a context whose log records carry tenant_id. Use the
// Context variants of the logger (InfoContext, WarnContext, ErrorContext) so
// the handler can read it.
func WithTenant(ctx context.Context, tenant fmt.Stringer) context.Context {
	if tenant == nil {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, tenant.String())
}

// TenantFrom returns the tenant identifier stored by WithTenant.
func TenantFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(tenantKey{}).(string)
	return id, ok && id != ""
}

// tenantHandler adds tenant_id to every record logged with a tenant context.
type tenantHandler struct{ slog.Handler }

func (h tenantHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := TenantFrom(ctx); ok {
		r.AddAttrs(slog.String("tenant_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h tenantHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return tenantHandler{h.Handler.WithAttrs(attrs)}
}

func (h tenantHandler) WithGroup(name string) slog.Handler {
	return tenantHandler{h.Handler.WithGroup(name)}
}

// ParseLevel maps the configured name to a slog level. Unknown names become info.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Sensitive reports whether s looks like a credential, token or database URL.
func Sensitive(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "postgres://") || strings.Contains(lower, "postgresql://") {
		return true
	}
	if strings.Contains(lower, "://") && strings.Contains(s, "@") {
		return true
	}
	if strings.Contains(lower, "password=") || strings.Contains(lower, "passwd=") || strings.Contains(lower, "secret=") || strings.Contains(lower, "token=") {
		return true
	}
	if strings.Contains(lower, "bearer ") {
		return true
	}
	return false
}

func redact(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.TimeKey || attr.Key == slog.LevelKey || attr.Key == slog.SourceKey {
		return attr
	}
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, "[redacted]")
	}
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		if Sensitive(attr.Value.String()) {
			return slog.String(attr.Key, "[redacted]")
		}
	case slog.KindAny:
		switch v := attr.Value.Any().(type) {
		case error:
			if v != nil && Sensitive(v.Error()) {
				return slog.String(attr.Key, "[redacted]")
			}
		case string:
			if Sensitive(v) {
				return slog.String(attr.Key, "[redacted]")
			}
		}
	}
	return attr
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "password", "passwd", "secret", "token", "authorization", "database_url", "dsn", "cookie", "set_cookie", "credential", "api_key", "private_key", "app_secret":
		return true
	}
	if strings.Contains(normalized, "password") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "token") || strings.Contains(normalized, "credential") {
		return true
	}
	return false
}
