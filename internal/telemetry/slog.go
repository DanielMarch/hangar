package telemetry

import (
	"context"
	"io"
	"log/slog"
)

// RedactingHandler wraps an slog.Handler (JSON in production, text in
// development) and redacts every attribute before it reaches the wrapped
// handler — recursively, through slog.Group, map[string]any, slices and
// nested structs (Roadmap Phase 0 design note; Principle 8).
type RedactingHandler struct {
	next slog.Handler
}

var _ slog.Handler = (*RedactingHandler)(nil)

// NewRedactingHandler wraps next.
func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

// NewJSONLogger builds the standard HANGAR logger: JSON output through the
// redacting handler. cmd/hangar selects this for HANGAR_LOG_FORMAT=json and a
// plain slog.NewTextHandler (also wrapped) for =text (development only).
func NewJSONLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(NewRedactingHandler(h))
}

// NewTextLogger builds a development-mode logger (HANGAR_LOG_FORMAT=text).
func NewTextLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(NewRedactingHandler(h))
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, nr)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &RedactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

// redactAttr redacts a single attribute, recursing into slog.KindGroup
// (nested `slog.Group(...)` attrs) and delegating everything else — structs,
// maps, slices, and config.Secret values reached via slog.Any — to the
// reflection-based walker in redact.go.
func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve() // flush any slog.LogValuer (e.g. config.Secret) first

	if sensitiveKey.MatchString(a.Key) && a.Value.Kind() != slog.KindGroup {
		return slog.Attr{Key: a.Key, Value: slog.StringValue(redacted)}
	}

	switch a.Value.Kind() {
	case slog.KindGroup:
		group := a.Value.Group()
		out := make([]slog.Attr, len(group))
		for i, ga := range group {
			out[i] = redactAttr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}

	case slog.KindAny:
		return slog.Attr{Key: a.Key, Value: slog.AnyValue(redactAny(a.Value.Any()))}

	default:
		return a
	}
}
