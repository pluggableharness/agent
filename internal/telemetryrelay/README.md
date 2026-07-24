# internal/telemetryrelay

The kernel side of the span-relay model (`docs/specifications/observability.md#the-relay-model`): a plugin relays its own completed trace spans to the kernel via `KernelCallbackService.ExportSpans` (`docs/specifications/kernel-callbacks.md`'s `ExportSpans`), and this package turns that batch into a real OTLP upload to the operator's configured collector.

## What this package does

- `convert.go` translates one `pluggableharness.trace.v1.Span` into its OTLP wire equivalent (`tracepb.Span`) — trace/span/parent-span ids from hex strings to raw bytes, the span-kind and status-code enums, timestamps to Unix-nanos, and a `google.protobuf.Struct` attribute set into OTLP `KeyValue`/`AnyValue` pairs (recursively, for a nested struct or list).
- `relay.go`'s `Relay` type groups a batch of spans into OTLP `ScopeSpans` by their declared `InstrumentationScope`, attaches a `Resource` built from the calling plugin's producer identity, and uploads the result via a wrapped `otlptrace.Client` — the same `Client` interface `internal/telemetry.Backend.TraceUploader` returns.

## How it fits in

`internal/telemetry`'s own `sdktrace.TracerProvider` is deliberately not involved: `sdktrace.ReadOnlySpan` can't be implemented outside the SDK, and re-creating a relayed span through the kernel's own tracer would assign it a fresh trace/span id, severing it from the parent/child relationships it already had in the plugin's own process. This package is the transparent bypass that lets a relayed span reach a collector with its original identity intact.

A `Relay` is constructed per launched plugin (mirroring `internal/kernelcallback`'s "one `Server` per plugin instance" shape) from that plugin's own `Backend.TraceUploader()` result, and `internal/kernelcallback`'s `ExportSpans` handler calls `Relay.Upload` per RPC.
