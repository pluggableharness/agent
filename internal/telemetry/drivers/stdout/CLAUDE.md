# internal/telemetry/drivers/stdout — agent notes

- **Don't add configuration knobs here.** `New()` intentionally takes no
  arguments — this is a debug aid, not a production sink. If an operator
  needs a configurable file destination, rotation, or format, that's a
  new driver, not an option added to this one.

- **This package sits just under the 80% coverage floor (~78-79%),
  deliberately not padded with a fake test.** `TraceExporter`,
  `MetricReader`, and `LogExporter` each wrap a `stdouttrace`/
  `stdoutmetric`/`stdoutlog` constructor whose only possible error source
  (verified by reading all three: `trace.go`, `exporter.go` in each
  module) is an internal, unexported `observ.NewInstrumentation` call —
  unreachable from outside the module via any public `Option`. The
  `if err != nil` branch in each of our three methods is genuine
  defensive code, not an undertested path; there is no way to
  legitimately trigger it in a test without reaching into OTel SDK
  internals we don't have access to. Don't "fix" this by injecting a fake
  writer or similar contortion — it wouldn't exercise the actual branch
  anyway (the failure is in ID/instrumentation setup, not in the
  `io.Writer`).
