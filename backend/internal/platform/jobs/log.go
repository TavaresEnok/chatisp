package jobs

import (
	"context"
	"log/slog"
	"strings"
)

func newLogger(base *slog.Logger) *slog.Logger {
	if base == nil {
		base = slog.New(slog.DiscardHandler)
	}
	return slog.New(omitPayload{next: base.Handler()})
}

// omitPayload drops values that can carry job arguments, metadata or error text.
type omitPayload struct {
	next slog.Handler
}

func (o omitPayload) Enabled(ctx context.Context, level slog.Level) bool {
	return o.next.Enabled(ctx, level)
}

func (o omitPayload) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	rec.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(omitAttr(attr))
		return true
	})
	return o.next.Handle(ctx, clean)
}

func (o omitPayload) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		cleaned[i] = omitAttr(attr)
	}
	return omitPayload{next: o.next.WithAttrs(cleaned)}
}

func (o omitPayload) WithGroup(name string) slog.Handler {
	return omitPayload{next: o.next.WithGroup(name)}
}

func omitAttr(attr slog.Attr) slog.Attr {
	switch strings.ToLower(attr.Key) {
	case "error", "err", "args", "job_args", "metadata", "encoded_args", "payload":
		return slog.String(attr.Key, "[omitido]")
	default:
		return attr
	}
}
