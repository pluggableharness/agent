package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers"
)

// otlpHTTPProtocol is the OTEL_EXPORTER_OTLP_PROTOCOL value selecting the
// HTTP transport, per the OTel env-var spec. Any other value (including
// unset) is treated as the gRPC transport, matching the SDK's own default.
const otlpHTTPProtocol = "http/protobuf"

// Provider is the minimal surface Bootstrap hands back to a plugin author —
// a public interface local to this package, not *internal/telemetry.Provider
// itself. internal/ packages are importable only from within this module
// (go-layout.md), so an out-of-tree plugin repo consuming pkg/telemetry via
// the module proxy cannot name *internal/telemetry.Provider in its own code
// at all: returning the concrete internal type made it usable only as an
// opaque value passed straight back into this package's own functions,
// defeating the point of pkg/ as the third-party-consumable surface
// (pkg/kernel/doc.go's "this package deliberately does not import
// internal/" note documents the same boundary; pkg/telemetry is the one
// deliberate, documented exception allowed to import internal/telemetry
// internally, but its exported surface still MUST stay expressible outside
// this module). *internal/telemetry.Provider satisfies this interface
// structurally — Go interfaces are implicit, so no change to
// internal/telemetry was needed for Bootstrap's new return type to compile.
//
// Instruments() and Config() are deliberately not part of this interface:
// both return internal/telemetry-only types (*Instruments, Config), and
// re-exporting either is a metrics-API redesign larger than this fix's
// scope — a plugin author has no path to either through this package
// today. That's a known, tracked gap, not an oversight.
type Provider interface {
	// Shutdown flushes and closes the underlying tracer/meter/logger
	// providers. Idempotent — safe to call more than once, returning the
	// first call's result every time (internal/telemetry.Provider.Shutdown's
	// own doc comment explains why that guard exists).
	Shutdown(ctx context.Context) error

	// ForceFlush flushes any spans/log records queued in their batch
	// processors without waiting for the normal export interval. A no-op
	// for a disabled signal.
	ForceFlush(ctx context.Context) error

	// Tracer returns this process's tracer for starting spans.
	Tracer() trace.Tracer
}

// Bootstrap constructs a Provider for a plugin subprocess, reading
// configuration from the process environment rather than requiring the
// plugin author to hand-build an internal/telemetry.Config — there is no
// kernel-to-plugin config-passing RPC for this today (kernel-callbacks.md
// defines no such primitive), so environment variables are the only
// channel available in v0.
//
// serviceName should identify the plugin itself (its manifest name is a
// good choice). Call the returned shutdown func before the process exits
// so any batched spans/metrics are flushed — typically via defer in main.
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is unset, Bootstrap wires the noop
// driver rather than guessing a default collector address: a plugin
// subprocess should not assume network access to a collector unless an
// operator or the kernel explicitly configured one. Producer/resource
// attribution (which plugin, which version) is not passed as an argument
// here — it reaches the Resource via OTEL_RESOURCE_ATTRIBUTES
// (resource.WithFromEnv, internal/telemetry.BuildResource), which the
// kernel stamps into this process's environment at launch
// (internal/telemetry.ResourceEnv).
func Bootstrap(ctx context.Context, serviceName string) (Provider, func(context.Context) error, error) {
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = serviceName

	backendName := "noop"
	if endpoint, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT"); ok {
		cfg.Endpoint = endpoint
		backendName = "otlpgrpc"
		if os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") == otlpHTTPProtocol {
			backendName = "otlphttp"
		}
	}

	backend, err := drivers.New(backendName, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: bootstrap: %w", err)
	}

	provider, err := telemetry.New(ctx, cfg, backend, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: bootstrap: %w", err)
	}

	return provider, provider.Shutdown, nil
}
