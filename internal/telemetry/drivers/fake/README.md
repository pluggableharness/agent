# internal/telemetry/drivers/fake

The `telemetry.Backend` test double for `internal/telemetry` and its
consumers — a hand-written fake (`go-testing.md`), not a mock.

## What this package does

- `Backend.Spans` — a `tracetest.InMemoryExporter`; after calling
  `Provider.ForceFlush`, read `Spans.GetSpans()` to assert on recorded
  spans (name, attributes, parentage, status).
- `Backend.Metrics` — a `sdkmetric.ManualReader`; call
  `Metrics.Collect(ctx, &rm)` directly to pull current instrument state
  into a `metricdata.ResourceMetrics`.
- `Backend.Logs` — a hand-written `LogRecorder` (`sdk/log` ships no
  `tracetest`-equivalent for logs); after calling `Provider.ForceFlush`,
  read `Logs.Records()` to assert on recorded `sdklog.Record`s (body,
  severity, trace/span ID).

## How it fits in

Used throughout `internal/telemetry`'s own test suite and by any future
package that needs to assert on telemetry output without a real OTLP
collector. `New()` returns fresh, independent recorders each call — never
share one `*Backend` across two `Provider`s expecting isolation.
