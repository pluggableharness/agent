# internal/telemetry/drivers/stdout

The `telemetry.Backend` that writes pretty-printed spans and metrics as
JSON to stdout — a dev/debug driver for running the kernel without a real
OTLP collector nearby.

## What this package does

- `TraceExporter` returns a `stdouttrace` exporter with pretty-printing on.
- `MetricReader` returns a `stdoutmetric` exporter wrapped in a
  `sdkmetric.PeriodicReader` using the SDK's default interval.
- `LogExporter` returns a `stdoutlog` exporter with pretty-printing on.

## How it fits in

Not configurable (no filename, no rotation, no interval override) —
it's a debug aid, not a production sink. Select it via
`settings.observability` or an operator's local override when there's
nothing else to point telemetry at.
