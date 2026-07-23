package log

import (
	"context"
	"log/slog"
)

// collectAttrs flattens a slog.Record's attributes into a map, for
// assertions in tests. Later duplicate keys win, matching slog's own
// last-value-wins behavior for repeated keys.
func collectAttrs(r slog.Record) map[string]any {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

// fakeHandler is a hand-written slog.Handler fake (per go-testing.md: fakes,
// not mocking frameworks) that captures every Record it receives instead of
// writing it anywhere, so a test can assert directly on the Record's level,
// message, and attributes.
type fakeHandler struct {
	minLevel slog.Level
	records  []slog.Record
}

func (h *fakeHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *fakeHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *fakeHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *fakeHandler) WithGroup(_ string) slog.Handler      { return h }
