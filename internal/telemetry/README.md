# internal/telemetry

PluggableHarness Agent's OTel-native tracing, metrics, and logs module: distributed
spans across the kernel's turn loop and the `hashicorp/go-plugin`
subprocess boundary, a fixed set of metric instruments mirroring the
kernel's own cost/bounds bookkeeping (`state-backend.md` §4.3,
`agent-loop.md` §3.1), and the bridge that carries `internal/log`'s
plugin-relayed log entries (and the kernel's own `log/slog` output) into
the same OTel pipeline, trace-correlated for free. Built directly on the
`go.opentelemetry.io/otel` SDK — this package's types and conventions
*are* the OTel data model, not a native abstraction with an OTel exporter
bolted on.

## What this package does

- `telemetry.go` — the `Backend` driver interface (the swappable exporter
  family) and `Provider`, the object a caller constructs via `New` and
  tears down via `Shutdown`.
- `span.go` — one `Start*` helper per kernel-loop attach point: sessions,
  turns, hook dispatches, hook subscribers, model calls, tool execution,
  policy evaluation, and `RunSession` spawns. Each returns `(context.Context,
  trace.Span)` so the span rides the live call chain into downstream RPCs.
- `instrument.go` / `usage.go` — the metric instruments and `RecordUsage`,
  which takes the kernel's already-computed cost/token figures rather than
  recomputing them.
- `resource.go` — builds the OTel `Resource` (service identity, producer
  attribution, operator-configured extra attributes, ambient environment).
- `propagation.go` — the W3C Trace Context propagator used to cross the
  plugin gRPC boundary.
- `grpchooks.go` — `ClientHandler`/`ServerHandler` (the `otelgrpc` stats
  handlers that make spans nest across process boundaries) and
  `ResourceEnv` (what the kernel stamps into a plugin subprocess's
  environment at launch).
- `sloghandler.go` — `Provider.SlogHandler`, an `otelslog`-backed
  `slog.Handler` — the seam that lets `internal/log.NewServer(...)` (and
  any of the kernel's own `log/slog` output, per the top-level
  `internal/CLAUDE.md` logging/telemetry rule) emit through this same
  OTel pipeline, trace-correlated automatically via ctx, with zero
  changes to `internal/log` itself.
- `config.go` — `Config`, this package's own HCL/cty-free configuration
  shape; `internal/config` translates its `Observability` type into this.
- `drivers/` — the exporter backends: `otlpgrpc`, `otlphttp`, `stdout`,
  `noop`, and `fake` (the test double). Each implements all three
  capabilities (`TraceExporter`/`MetricReader`/`LogExporter`). See
  `drivers/README.md`.

## How it fits in

This package is **kernel-internal instrumentation, not a hook
subscriber**. The RunTurn loop (not yet built) calls the `Start*` helpers
directly at its 18 numbered steps and around each of the 9 hook-point
dispatches (`agent-loop.md` §2, §4) — an `observe`-mode hook subscriber
can't do this, since its return value is discarded and it never holds the
live call-chain context. See `CLAUDE.md` for the full reasoning and for
the hard rules (never persisted, replay uses `noop`, cardinality
discipline) this package depends on staying true.

Nothing in the kernel (RunTurn, the plugin host, `cmd/agent`) exists
yet — this package is fully buildable and unit-tested today against the
`fake` driver; wiring it into the real loop is future work once that loop
exists.
