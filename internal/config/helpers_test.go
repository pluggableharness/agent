package config

import (
	"context"
	"testing"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

// ptr returns a pointer to v, for building the *int expectations of the
// optional-integer fields (Hook.TimeoutMS, Settings.MaxDepth) whose whole
// point is that nil and an explicit 0 differ.
func ptr[T any](v T) *T { return &v }

// testProvider returns a *telemetry.Provider wired to a fresh fake backend
// (internal/telemetry/drivers/fake), for tests that call LoadFile and need
// a non-nil Provider without a real OTel collector. Mirrors
// internal/telemetry/span_test.go's newTestProvider helper.
func testProvider(t *testing.T) *telemetry.Provider {
	t.Helper()
	prov, _ := testProviderWithBackend(t)
	return prov
}

// testProviderWithBackend is like testProvider but also returns the fake
// backend, for a test that needs to assert on recorded spans (e.g. that
// LoadFile records exactly one config.load span) rather than just needing
// a non-nil Provider to pass through.
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
