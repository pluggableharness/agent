# Observability

The fourth kernel-owned (non-plugin) spec, alongside [`kernel-callbacks.md`](kernel-callbacks.md), [`state-backend.md`](state-backend.md), and [`event-bus.md`](event-bus.md). It covers how a plugin's own traces, metrics, and logs reach the operator's configured collector via `KernelCallbackService.ExportSpans`/`.RecordMetrics`/`.GetTelemetryConfig` ([`kernel-callbacks.md#exportspans`](kernel-callbacks.md#exportspans), [`kernel-callbacks.md#recordmetrics`](kernel-callbacks.md#recordmetrics), [`kernel-callbacks.md#gettelemetryconfig`](kernel-callbacks.md#gettelemetryconfig)) — the wire shape lives there; this document covers the relay model, why it exists, and the one place tracing and metrics deliberately diverge. `Log`'s equivalent relay ([`kernel-callbacks.md#log`](kernel-callbacks.md#log)) already existed before this document and is not repeated here.

## The relay model

A plugin subprocess's spans and metrics are relayed through the kernel to a single, kernel-configured collector, rather than each plugin process exporting OTLP directly to a collector of its own. Concretely: a plugin builds an ordinary OTel SDK pipeline (`tracer.Start(...)`, real spans, real instruments), but the SDK's exporter is one that ships batches to the kernel via `ExportSpans`/`RecordMetrics` instead of opening its own network connection to a collector. The kernel forwards `ExportSpans`' spans to the collector essentially unchanged (see "Span relay is transparent" below); `RecordMetrics` is handled differently (see "The tracing/metrics asymmetry").

**This reverses an earlier design.** A span-funnel-through-the-kernel RPC was previously considered and rejected in favor of direct per-process OTLP export, on the reasoning that trace nesting across the plugin boundary already worked via ordinary W3C `traceparent` propagation over the kernel-callback channel's gRPC stats handlers (`internal/telemetry/grpchooks.go`'s `ClientHandler`/`ServerHandler`), so a funnel seemed like unneeded indirection. This revision reverses that call, for two reasons that outweigh the extra serialization hop:

- **A plugin subprocess should not need network egress or collector credentials to be observable.** Direct export means every plugin process needs outbound access to wherever the collector lives, and (for an authenticated collector) its own copy of whatever credential that requires. Relaying through the kernel means only the kernel needs that access and that credential — a plugin subprocess's network footprint stays limited to the local gRPC connection it already has to the kernel.
- **The kernel becomes the single place sampling and export configuration lives.** `GetTelemetryConfig` (`kernel-callbacks.md#gettelemetryconfig`) already makes the kernel the authority a plugin asks "is tracing on, at what ratio" — relaying the actual span data through the same connection means there is exactly one collector endpoint, one sampling ratio, and one export cadence to reason about operationally, not one per plugin process plus the kernel's own.

**Trace-context propagation across the plugin boundary is unchanged by this reversal.** `traceparent` still crosses the gRPC boundary via the otelgrpc stats handlers exactly as it always has — relaying a span's *export* through the kernel is a transport decision about where finished spans go, not a second, competing propagation mechanism for how an in-flight call's trace context gets from the kernel to a plugin or back. `.claude/rules/logging-telemetry.md`'s ban on hand-rolled trace-context propagation governs that separate concern and is untouched here.

### Span relay is transparent

The kernel cannot feed a relayed span into its own `sdktrace` pipeline the way it instruments its own code: `sdktrace.ReadOnlySpan` carries an unexported method and is unimplementable outside the SDK itself, and re-creating a span via the kernel's own `tracer.Start` would assign that span a fresh `trace_id`/`span_id`, silently severing it from the plugin-internal parent/child relationships it already had. So the relay bypasses the kernel's SDK pipeline entirely and speaks OTLP directly: `go.opentelemetry.io/otel/exporters/otlp/otlptrace.Client`'s `UploadTraces` takes already-built `tracepb.ResourceSpans` and ships them, unmodified, to the configured collector. The kernel MUST NOT alter a relayed span's `trace_id`, `span_id`, `parent_span_id`, or timestamps before forwarding it — the one thing it adds is producer attribution on the exported resource, server-derived from the callback connection exactly as `Emit`/`Log` attribute their own callers, never read from the span's own fields (there is no field on `trace.v1.Span` for a plugin to declare its own identity into, by the same anti-spoof reasoning `kernel-callbacks.md#the-callback-channel` already applies everywhere else on this service).

## The tracing/metrics asymmetry

`RecordMetrics` is deliberately **not** a transparent relay, and this is a permanent design choice, not a gap to "fix" toward symmetry with `ExportSpans` later:

- There is no metrics equivalent of `otlptrace.Client` to bypass through — `otlpmetricgrpc` exposes no comparable already-batched-protobuf uploader interface, so a transparent relay isn't even the path of least resistance here the way it is for spans.
- More importantly, `.claude/rules/logging-telemetry.md`'s cardinality rule is non-negotiable: an unbounded identifier (a session ID, a turn index, a request ID) MUST never become a metric attribute, on pain of silently breaking a metrics backend's cardinality budget. A plugin-supplied `metric.v1.MetricRecord.attributes` map is, by construction, an open set the kernel cannot pre-validate against any fixed vocabulary — a transparent relay would hand an arbitrary third-party plugin exactly the ability this rule exists to prevent.

Instead, the kernel treats a `RecordMetrics` call as an observation against its **own** instruments: it lazily creates (or reuses) an instrument named `plugin.{category}.{name}.{metric_name}` — the same server-derived producer identity used to build an event-bus topic — on its own `MeterProvider`, and records the observation there. Attribute keys beyond a bounded per-instrument set are dropped, with a throttled `WARN` log identifying which keys were dropped and how many times, so an over-cardinality plugin is visible in the logs rather than silently truncated with no signal at all. Spans keep unbounded attributes (a span's attributes go on the span only, per the existing cardinality rule's own carve-out for span-only unbounded data); metrics never do, on any code path, plugin-sourced or kernel-native.

## `GetTelemetryConfig` caching

A plugin SHOULD call `GetTelemetryConfig` once at process startup (typically from the same bootstrap call that wires its OTel SDK pipeline) and cache the result for its process lifetime — `traces_enabled`/`metrics_enabled`/`logs_enabled`/`log_level`/`sampling_ratio` are operator configuration ([`configuration/blocks-reference.md#observability`](configuration/blocks-reference.md#observability)), not something that changes mid-process. A plugin's `TracingEnabled()`/log-level check is expected to be a cached field read, never a per-call RPC round-trip — see [`kernel-callbacks.md#gettelemetryconfig`](kernel-callbacks.md#gettelemetryconfig).

## Telemetry never replays and never persists

[`state-backend.md`](state-backend.md) already establishes that telemetry is the one thing that must *not* replay faithfully: trace/span IDs are non-deterministic by construction (`crypto/rand`-backed), so there is no `trace_id`/`span_id` column anywhere in the schema, and replay MUST select a no-op telemetry driver unconditionally rather than attempt to reproduce identical IDs. This document restates that MUST because it now governs a second thing besides the kernel's own native instrumentation: a relayed plugin span or metric observation MUST NOT be written to `events`, `cost_ledger`, or `plan_items` either, and a replay session MUST NOT call `ExportSpans`/`RecordMetrics` against a real collector on the plugin's behalf — the same no-op-driver rule that already governs the kernel's own spans governs anything relayed through it.

Relatedly, telemetry never owns a number it didn't originate: `RecordMetrics`/`ExportSpans` observe usage/cost/token figures the kernel's own cost-ledger write already computed ([`model/protocol.md#cost-computation`](model/protocol.md#cost-computation)) — a plugin's own span attributes MAY carry those same figures for tracing convenience, but the kernel never recomputes or treats a telemetry-carried figure as authoritative over the persisted one.

## Required vs. optional support

| Capability | Level | Notes |
|---|---|---|
| Spans relayed via `ExportSpans` reach the operator's configured collector unmodified | MUST | "Span relay is transparent" |
| Kernel alters a relayed span's identity/timestamps | MUST NOT | "Span relay is transparent" |
| `RecordMetrics` observations recorded against kernel-owned instruments, not relayed as OTLP | MUST | "The tracing/metrics asymmetry" |
| Metric attribute keys bounded per instrument, excess dropped with a throttled `WARN` | MUST | "The tracing/metrics asymmetry" |
| A plugin caches `GetTelemetryConfig` for its process lifetime rather than polling | SHOULD | "`GetTelemetryConfig` caching" |
| Replay selects the no-op telemetry driver unconditionally | MUST | "Telemetry never replays and never persists" |
| A relayed span/metric persisted to `events`/`cost_ledger`/`plan_items` | MUST NOT | "Telemetry never replays and never persists" |

## Open questions

- Whether `RecordMetrics`' per-instrument bounded-attribute-key set should be operator-configurable (a fixed default vs. an `agent.hcl`-declared allowlist per metric name) — shipped with a fixed default in this revision; a config surface is a plausible follow-up once real plugin metric usage shows what's actually needed.
- Whether a future revision should let a plugin request higher-than-configured sampling for a specific span (a "force-sample this trace" escape hatch) — no candidate use case has surfaced yet.
