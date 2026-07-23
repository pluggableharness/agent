# internal/telemetry/drivers

The driver selector for `internal/telemetry` (`go-layout.md`'s driver
pattern): `New(name, cfg)` is the sole place in the kernel that switches
on a telemetry backend name.

## What this package does

- `drivers.go` — `New(name string, cfg telemetry.Config) (telemetry.Backend,
  error)`, over the five recognized names: `"otlpgrpc"`, `"otlphttp"`,
  `"stdout"`, `"noop"`, `"fake"`. Unknown names return `ErrUnknownDriver`.

## How it fits in

Each sub-package (`otlpgrpc/`, `otlphttp/`, `stdout/`, `noop/`, `fake/`)
implements `telemetry.Backend` — all three capabilities
(`TraceExporter`/`MetricReader`/`LogExporter`) — and imports only
`internal/telemetry`, never a sibling driver. This package is the only
one that imports every driver at once. Adding a new exporter backend
means adding a new sub-package plus one `case` here; nothing else in the
kernel should ever branch on a driver name.
