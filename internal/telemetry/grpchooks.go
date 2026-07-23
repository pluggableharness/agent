package telemetry

import (
	"net/url"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc/stats"
)

// ClientHandler returns a grpc.StatsHandler that injects the active
// span's W3C traceparent into outbound gRPC metadata and starts a client
// span for each RPC, using p's tracer/meter and propagator.
//
// Wire it into grpc.WithStatsHandler(...) on whichever side of the
// hashicorp/go-plugin boundary is dialing: the kernel is the client for a
// plugin's category RPCs (StreamCompletion, Invoke, ...); a plugin is the
// client when calling back into the kernel over the KernelCallbackService
// (RunSession, CountTokens, Emit, Log). Pair with ServerHandler on the
// other side so a span crosses the process boundary as parent/child
// rather than starting a disconnected new trace — see CLAUDE.md.
func (p *Provider) ClientHandler() stats.Handler {
	return otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(p.tracerProvider),
		otelgrpc.WithMeterProvider(p.meterProvider),
		otelgrpc.WithPropagators(Propagator),
	)
}

// ServerHandler returns a grpc.StatsHandler that extracts a remote
// traceparent from inbound gRPC metadata and starts a server span as its
// child, using p's tracer/meter and propagator.
//
// Wire it into grpc.StatsHandler(...) on whichever side is serving: a
// plugin is the server for its own category RPCs; the kernel is the
// server for the KernelCallbackService. See ClientHandler.
func (p *Provider) ServerHandler() stats.Handler {
	return otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(p.tracerProvider),
		otelgrpc.WithMeterProvider(p.meterProvider),
		otelgrpc.WithPropagators(Propagator),
	)
}

// ResourceEnv returns the OTEL_RESOURCE_ATTRIBUTES value the kernel should
// stamp into a plugin subprocess's environment at launch
// (plugin-runtime.md's exec.CommandContext), so the plugin's own
// pkg/telemetry.Bootstrap — reading resource.WithFromEnv() — inherits the
// kernel-authenticated producer identity by default without the plugin
// author doing anything. The format is the OTel spec's comma-separated
// key=value list (https://opentelemetry.io/docs/specs/otel/resource/sdk/#specifying-resource-information-via-an-environment-variable).
//
// category, name, and version are the plugin's manifest identity as the
// kernel resolved it (the lockfile-pinned producer, per registry.md) —
// not a value the plugin subprocess supplies about itself. Values are
// URL-encoded per the OTel spec's env-var format, since a plugin or
// version string isn't guaranteed free of commas/equals signs.
func ResourceEnv(category, name, version string) string {
	pairs := []string{
		string(ProducerCategoryKey) + "=" + url.QueryEscape(category),
		string(ProducerNameKey) + "=" + url.QueryEscape(name),
		string(ProducerVersionKey) + "=" + url.QueryEscape(version),
	}
	return strings.Join(pairs, ",")
}
