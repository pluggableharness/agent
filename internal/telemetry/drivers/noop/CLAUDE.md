# internal/telemetry/drivers/noop — agent notes

- **Don't "optimize" `MetricReader` into something that skips SDK
  aggregation entirely.** A `ManualReader` that's never `Collect`ed still
  gives real span/metric-creation code paths a workout (useful for
  catching a panic in instrumentation code during testing) while
  guaranteeing nothing is exported. If a genuinely zero-overhead path is
  what's wanted, that's `Config.TracesEnabled`/`MetricsEnabled = false`
  in the parent package, not this driver — see
  `internal/telemetry/CLAUDE.md`.
- **This is the mandatory replay backend.** Anything that replays a
  session (`state-backend.md`) must select this driver unconditionally,
  ignoring whatever the operator's `agent.hcl` configured for live
  sessions.
