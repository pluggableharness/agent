// Package telemetryrelay implements the kernel side of the span-relay
// model described in docs/specifications/observability.md#the-relay-model:
// translating a batch of plugin-relayed pluggableharness.trace.v1.Span
// messages (kernel-callbacks.md's ExportSpans) into OTLP
// tracepb.ResourceSpans and uploading them via an otlptrace.Client.
//
// This bypasses internal/telemetry's own sdktrace.TracerProvider
// entirely, on purpose: sdktrace.ReadOnlySpan is unimplementable outside
// the SDK (an unexported method), and re-creating a relayed span through
// this process's own tracer would assign it a fresh trace_id/span_id,
// silently severing it from the parent/child relationships it already
// had in the originating plugin's own process. Relay.Upload therefore
// translates the wire Span directly into the OTLP wire format and hands
// it to internal/telemetry.Backend's TraceUploader-returned Client,
// unmodified in every identity/timing field.
//
// A Relay is not owned by internal/telemetry.Provider — internal/
// kernelcallback constructs one per launched plugin (mirroring
// internal/kernelcallback's own "one Server per plugin instance" shape)
// from that plugin's Backend.TraceUploader() Client, and is responsible
// for calling Client.Stop when the plugin's callback connection closes.
package telemetryrelay
