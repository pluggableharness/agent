package kernel

import (
	"context"
	"log/slog"
)

// fanoutHandler writes every record to each of its targets.
//
// The kernel needs exactly two destinations and no policy between them:
// an operator's terminal (a stderr text handler, always) and the OTel logs
// bridge (internal/telemetry.Provider.SlogHandler, only when the operator
// enabled telemetry and the logs signal). slog ships no multiplexer, and
// the alternative — choosing one destination — either loses the operator's
// own console output or silently drops the logs signal
// (.claude/rules/logging-telemetry.md treats both as mandatory).
//
// A one-target fanout behaves exactly like that target, so the common
// telemetry-off case pays only one indirect call.
type fanoutHandler struct {
	targets []slog.Handler
}

// newFanoutHandler returns a Handler over every non-nil target. With a
// single target it returns that target unwrapped; with none it returns a
// fanout that discards, which is a legal slog.Handler and never a nil one.
func newFanoutHandler(targets ...slog.Handler) slog.Handler {
	live := make([]slog.Handler, 0, len(targets))
	for _, t := range targets {
		if t != nil {
			live = append(live, t)
		}
	}
	if len(live) == 1 {
		return live[0]
	}
	return &fanoutHandler{targets: live}
}

// Enabled reports whether any target would handle a record at this level.
func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, t := range h.targets {
		if t.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle writes r to every target that accepts this level.
//
// A target's error is deliberately not propagated past the remaining
// targets: a broken OTel exporter must not stop the operator's terminal
// from getting the line. The first error is returned once every target has
// been given the record.
func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, t := range h.targets {
		if !t.Enabled(ctx, r.Level) {
			continue
		}
		// Each target may retain the record, so each gets its own clone.
		if err := t.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WithAttrs returns a fanout over every target's own WithAttrs.
func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.targets))
	for i, t := range h.targets {
		next[i] = t.WithAttrs(attrs)
	}
	return &fanoutHandler{targets: next}
}

// WithGroup returns a fanout over every target's own WithGroup.
func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.targets))
	for i, t := range h.targets {
		next[i] = t.WithGroup(name)
	}
	return &fanoutHandler{targets: next}
}

var _ slog.Handler = (*fanoutHandler)(nil)
