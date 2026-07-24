package telemetry_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pluggableharness/agent/pkg/telemetry"
)

// unsetEnvForTest removes key from the process environment for the
// duration of t, restoring its original value (or absence) on cleanup.
// t.Setenv only sets a value, never removes one, so this is needed for a
// test that specifically requires a variable to be absent.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	original, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if wasSet {
			if err := os.Setenv(key, original); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		}
	})
}

// TestBootstrap_returnsPublicProviderInterface confirms Bootstrap's return
// type is the package-local Provider interface (not the unexported-to-
// third-parties *internal/telemetry.Provider it wraps), and that all three
// interface methods are callable through it — the defect this test guards
// against is exactly the "returns an internal/ type a third-party plugin
// author cannot even name" bug described in Provider's doc comment.
func TestBootstrap_returnsPublicProviderInterface(t *testing.T) {
	// Not parallel: mutates process environment.
	unsetEnvForTest(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

	var provider telemetry.Provider
	provider, shutdown, err := telemetry.Bootstrap(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	if tracer := provider.Tracer(); tracer == nil {
		t.Error("Tracer() returned nil")
	}
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
}

func TestBootstrap_noEndpointUsesNoop(t *testing.T) {
	// Not parallel: mutates process environment.
	unsetEnvForTest(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

	provider, shutdown, err := telemetry.Bootstrap(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if provider == nil {
		t.Fatal("Bootstrap returned a nil provider with a nil error")
	}
	if shutdown == nil {
		t.Fatal("Bootstrap returned a nil shutdown func with a nil error")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// TestBootstrap_withEndpoint only asserts construction: it selects the
// otlpgrpc backend and returns a non-nil provider. It deliberately does
// NOT assert that shutdown succeeds — that would require a real
// reachable collector, which unit tests must not depend on
// (go-testing.md). Shutdown flushing against an actually-unreachable
// address is exactly the otlpgrpc/otlphttp integration tier's job.
func TestBootstrap_withEndpoint(t *testing.T) {
	// Not parallel: mutates process environment (t.Setenv).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	provider, shutdown, err := telemetry.Bootstrap(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if provider == nil {
		t.Fatal("Bootstrap returned a nil provider with a nil error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Logf("shutdown against an unreachable collector errored as expected: %v", err)
	}
}

// TestBootstrap_httpProtocol only asserts construction — see
// TestBootstrap_withEndpoint's comment for why shutdown's result isn't
// asserted here.
func TestBootstrap_httpProtocol(t *testing.T) {
	// Not parallel: mutates process environment (t.Setenv).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	provider, shutdown, err := telemetry.Bootstrap(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if provider == nil {
		t.Fatal("Bootstrap returned a nil provider with a nil error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Logf("shutdown against an unreachable collector errored as expected: %v", err)
	}
}
