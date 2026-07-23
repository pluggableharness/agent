package registry

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

// testProvider returns a *telemetry.Provider wired to a fresh fake backend
// (internal/telemetry/drivers/fake), for tests that call LoadGlobalConfig,
// LoadLockFile, or VerifyChecksum and need a non-nil Provider without a real
// OTel collector. Mirrors internal/config/helpers_test.go's testProvider
// helper, which in turn mirrors internal/telemetry/span_test.go's
// newTestProvider.
func testProvider(t *testing.T) *telemetry.Provider {
	t.Helper()
	prov, _ := testProviderWithBackend(t)
	return prov
}

// testProviderWithBackend returns the same Provider testProvider does, plus
// the fake.Backend it's wired to, for a test that also needs to assert on
// recorded spans (internal/telemetry/drivers/fake/CLAUDE.md: force-flush the
// Provider before reading backend.Spans.GetSpans()).
func testProviderWithBackend(t *testing.T) (*telemetry.Provider, *fake.Backend) {
	t.Helper()
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"
	backend := fake.New()
	prov, err := telemetry.New(context.Background(), cfg, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return prov, backend
}

// flushedSpans force-flushes prov and returns everything backend recorded so
// far. Mirrors internal/telemetry/span_test.go's flushedSpans helper.
func flushedSpans(t *testing.T, prov *telemetry.Provider, backend *fake.Backend) tracetest.SpanStubs {
	t.Helper()
	if err := prov.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	return backend.Spans.GetSpans()
}

// captureLogs installs a DEBUG-level slog.Logger writing to an in-memory
// buffer as the process default for the duration of the calling test,
// restoring the previous default via t.Cleanup. Used to assert that an
// instrumented function's entry-level DEBUG log actually fires.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
