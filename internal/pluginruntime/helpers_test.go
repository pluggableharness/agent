package pluginruntime

import (
	"context"
	"log/slog"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// fakeHandler is a hand-written slog.Handler fake (go-testing.md: fakes,
// not mocking frameworks) that captures every Record it receives, mirroring
// internal/log's and internal/kernelcallback's own fakeHandler.
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

// collectAttrs flattens a slog.Record's attributes into a map, for
// assertions.
func collectAttrs(r slog.Record) map[string]any {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

// fakeCallbackServer is a hand-written kernelv1.KernelCallbackServiceServer
// fake, standing in for a real internal/kernelcallback.Server in tests that
// don't need actual Log delegation.
type fakeCallbackServer struct {
	kernelv1.UnimplementedKernelCallbackServiceServer
}
