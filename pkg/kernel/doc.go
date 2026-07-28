// Package kernel is the plugin-author-facing entry point into the
// KernelCallbackService callback channel (specifications/kernel-callbacks.md)
// — the connection every plugin subprocess, regardless of category, is
// handed back to the kernel at handshake time over the fixed callback
// broker ID (pkg/common.CallbackBrokerID). A plugin author calls Dial
// once from within their category's own GRPCServer method and keeps the
// returned *Client for the process's lifetime.
//
// Client wraps the generated pkg/kernel/proto/v1.KernelCallbackServiceClient
// with the ergonomic surface most plugin authors actually want:
//
//   - NewSlogHandler builds a log/slog.Handler that batches records and
//     flushes them via Log — a plugin author gets ordinary structured
//     logging with no per-line RPC cost and no knowledge that Log exists
//     at all.
//   - NewSpanExporter builds a go.opentelemetry.io/otel/sdk/trace.SpanExporter
//     that relays completed spans via ExportSpans — a plugin author wires
//     an ordinary OTel SDK TracerProvider and writes normal
//     tracer.Start(...) code; the relay transport is invisible.
//   - Client.Metrics builds a recorder that relays observations via
//     RecordMetrics — the metrics counterpart to NewSpanExporter, and the
//     only route a plugin has to a metric at all. The kernel owns the
//     instruments and bounds their attribute cardinality, neither of
//     which a plugin can do for itself; that is why this relays rather
//     than exporting off-process.
//   - LoadTelemetryConfig/TracingEnabled/MetricsEnabled/LogsEnabled/LogLevel/
//     SamplingRatio cache GetTelemetryConfig's result once at startup
//     (specifications/observability.md#gettelemetryconfig-caching) —
//     a plugin checks these as cached field reads, never a per-call RPC.
//   - Publish/Subscribe wrap the event bus (specifications/event-bus.md):
//     Publish is a thin one-line call; Subscribe owns the stream-receive
//     goroutine and delivers events to a caller-supplied handler, so a
//     plugin author writes a handler, not stream plumbing.
//   - Frontend state-surface helpers (frontend.go): GetSessionState,
//     SubmitInput (returns turn_id), session lifecycle, plan/interactive
//     resolution, metadata publish/list/retract, StreamDeltas, and the
//     slash/action unaries — see specifications/frontend/ and
//     specifications/kernel-callbacks.md.
//
// This package deliberately does not import anything under internal/ —
// pkg/ is the plugin-author-consumable surface and internal/ is
// kernel-only; a few small facts (the six-level log severity boundaries)
// are duplicated here rather than imported, see level.go's doc comment.
package kernel
