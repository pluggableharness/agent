# internal/telemetry/drivers/otlpgrpc

The `telemetry.Backend` that exports over OTLP/gRPC — the default
production driver (`telemetry.DefaultConfig.Backend`), matching this
repo's existing gRPC-everywhere posture (`grpc.md`).

## What this package does

- `TraceExporter` constructs an `otlptracegrpc` exporter from
  `cfg.Endpoint`/`cfg.Insecure`.
- `MetricReader` constructs an `otlpmetricgrpc` exporter wrapped in a
  `sdkmetric.PeriodicReader` firing every `cfg.ExportInterval`.
- `LogExporter` constructs an `otlploggrpc` exporter — no periodic-reader
  wrapping needed; the log SDK's batching lives on the processor side
  (`sdklog.NewBatchProcessor`, constructed by `telemetry.New` itself).

## How it fits in

`New(ctx, opts...)` on both exporters uses `grpc.NewClient` under the
hood, which dials lazily — construction never blocks on, or requires, a
reachable collector. Actually exporting (and therefore `Shutdown`, which
flushes) does require one; see `CLAUDE.md`.
