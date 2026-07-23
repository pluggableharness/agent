# internal/telemetry/drivers/otlpgrpc — agent notes

- **Construction never touches the network; shutdown does.** A real bug
  surfaced during development: a unit test that called
  `Provider.Shutdown` against an unreachable `localhost:4317` blocked for
  the OTLP client's full ~10s export-timeout before erroring
  `connection refused`. Construction (`New`, `TraceExporter`,
  `MetricReader`) is safe in a hermetic unit test; asserting that
  `Shutdown`/export actually succeeds requires a real collector and
  belongs in a `//go:build integration` test, not here.
- **If a unit test does call `Shutdown` against a fake address**, bound
  it with a short `context.WithTimeout` and don't assert on the error —
  see `internal/telemetry/drivers/otlpgrpc/otlpgrpc_test.go` and
  `pkg/telemetry/telemetry_test.go` for the pattern.
