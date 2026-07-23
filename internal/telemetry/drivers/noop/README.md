# internal/telemetry/drivers/noop

The `telemetry.Backend` that discards everything at the export boundary.

## What this package does

- `TraceExporter` returns the OTel SDK's own `tracetest.NoopExporter`.
- `MetricReader` returns a `sdkmetric.ManualReader` that is never
  collected — data is tracked in bounded per-instrument aggregation state
  but never read out or exported.
- `LogExporter` returns a hand-written discarding `sdklog.Exporter` — the
  SDK ships no noop log exporter (unlike `tracetest.NewNoopExporter` for
  traces), so this one is three trivial no-op methods.

## How it fits in

Selected whenever `settings.telemetry = false`, and — as a hard rule
(see `internal/telemetry/CLAUDE.md`) — for every replayed session. Unlike
`Config.TracesEnabled`/`MetricsEnabled = false` (which bypasses the SDK
pipeline entirely via an OTel no-op provider), selecting this driver still
builds a real SDK pipeline — the sampler runs, spans are created and
processed — but nothing ever leaves the process. See
`internal/telemetry/CLAUDE.md` for why both mechanisms exist and aren't
redundant.
