# internal/telemetry/drivers/otlphttp

The `telemetry.Backend` that exports over OTLP/HTTP, for an operator
whose collector isn't reachable over gRPC (e.g. behind an HTTP-only
ingress/proxy).

## What this package does

- `TraceExporter` constructs an `otlptracehttp` exporter from
  `cfg.Endpoint`/`cfg.Insecure`.
- `MetricReader` constructs an `otlpmetrichttp` exporter wrapped in a
  `sdkmetric.PeriodicReader` firing every `cfg.ExportInterval`.
- `LogExporter` constructs an `otlploghttp` exporter — no periodic-reader
  wrapping needed; the log SDK's batching lives on the processor side
  (`sdklog.NewBatchProcessor`, constructed by `telemetry.New` itself).

## How it fits in

Selected via `settings.observability.protocol = "http"`
(`internal/config`'s `decodeObservability`) or, for a plugin subprocess,
`OTEL_EXPORTER_OTLP_PROTOCOL = "http/protobuf"`
(`pkg/telemetry.Bootstrap`). See `internal/telemetry/drivers/otlpgrpc/CLAUDE.md`
for the same construction-vs-shutdown network caveat, which applies here too.
