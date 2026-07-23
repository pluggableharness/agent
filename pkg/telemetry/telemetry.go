package telemetry

import (
	"context"
	"fmt"
	"os"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers"
)

// otlpHTTPProtocol is the OTEL_EXPORTER_OTLP_PROTOCOL value selecting the
// HTTP transport, per the OTel env-var spec. Any other value (including
// unset) is treated as the gRPC transport, matching the SDK's own default.
const otlpHTTPProtocol = "http/protobuf"

// Bootstrap constructs a telemetry.Provider for a plugin subprocess,
// reading configuration from the process environment rather than
// requiring the plugin author to hand-build an internal/telemetry.Config —
// there is no kernel-to-plugin config-passing RPC for this today
// (kernel-callbacks.md defines no such primitive), so environment
// variables are the only channel available in v0.
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
func Bootstrap(ctx context.Context, serviceName string) (*telemetry.Provider, func(context.Context) error, error) {
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
