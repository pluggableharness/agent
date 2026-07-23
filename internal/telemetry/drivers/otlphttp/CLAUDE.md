# internal/telemetry/drivers/otlphttp — agent notes

- Same construction-vs-shutdown network caveat as
  `internal/telemetry/drivers/otlpgrpc` — see that package's `CLAUDE.md`.
  Construction is hermetic and safe in a unit test; asserting a real
  export/flush succeeds needs a real collector and belongs in a
  `//go:build integration` test.
