# internal/telemetry — agent notes

- **This is kernel-internal instrumentation, not a hook subscriber.** The
  `Start*` helpers in `span.go` are called directly by the (not-yet-built)
  RunTurn loop at its 18 numbered steps and around each of the 9 hook-point
  dispatches (`agent-loop.md` §2, §4). An `observe`-mode subscriber can't do
  this job: its return value is discarded (`agent-loop.md` §4.1), so it has
  no way to thread a span through the kernel's real ctx into downstream
  provider/tool RPCs. Don't "simplify" this into a registered hook
  subscriber — it was considered and rejected for this reason.

- **Telemetry is strictly side-band — never persisted, never
  recomputed.** It MUST NOT write to the `events`, `cost_ledger`, or
  `plan_items` tables (no `trace_id`/`span_id` column anywhere), and
  `RecordUsage` MUST take the kernel's already-computed cost/token values
  rather than running a second computation — see `determinism.md`'s
  fallback-token-heuristic section for why a second computation path for
  the same numbers is a correctness bug waiting to happen, not just
  wasted work. Trace/span IDs are `crypto/rand` — non-deterministic by
  construction — which is exactly why they must never touch persisted
  state or replay would stop being byte-identical.

- **Replay MUST use the `noop` backend.** A replayed session must not
  re-emit production telemetry, and can't reproduce identical trace/span
  IDs anyway. Whatever wires `internal/telemetry` into the replay path
  must select `drivers.New("noop", cfg)` unconditionally, not whatever the
  operator's `agent.hcl` configured for live sessions.

- **Cardinality rule (load-bearing, silently breaks a metrics backend if
  violated).** `SessionIDKey`, `SessionParentIDKey`, `SessionRootIDKey`,
  and `TurnIndexKey` are unbounded and MUST only ever be attached to
  spans, never used as a metric attribute — see `attributes.go`'s doc
  comment. Every other attribute key in this package is deliberately
  bounded (a fixed enum, or bounded by the operator's configured
  tool/model set) and is safe on both spans and metrics.

- **`sdkmetric.MeterProvider.Shutdown` is NOT idempotent — this bit a
  test during development.** Unlike `sdktrace.TracerProvider.Shutdown`
  (internally guarded), `MeterProvider.Shutdown` unconditionally
  re-invokes the underlying reader's `Shutdown` on every call, which
  errors `"reader is shutdown"` on a second call. `Provider.Shutdown`
  wraps both in its own `sync.Once` for this reason — don't remove that
  guard thinking the SDK already handles it.

- **Driver pattern: the exporter backend is the driver, nothing else.**
  `Backend` returns OTel's own `SpanExporter`/`Reader` interfaces directly
  rather than a parallel abstraction — wrapping them again would be
  redundant indirection, the same reasoning `internal/log/CLAUDE.md` gives
  for not wrapping `slog.Handler`. This is, notably, the *first* package in
  the repo to actually use `go-layout.md`'s driver-subpackage pattern in
  practice (`internal/log`, `internal/config`, etc. don't have genuinely
  swappable backends) — treat it as the reference example.

- **Two distinct "do nothing" mechanisms exist on purpose, don't
  collapse them.** `Config.TracesEnabled`/`MetricsEnabled = false` bypasses
  the SDK pipeline entirely (an OTel no-op provider, zero span-creation
  overhead). Selecting the `noop` *driver* still builds a real SDK
  pipeline (sampler runs, spans are created and processed) but discards at
  the export boundary — useful for exercising the instrumentation code
  path without shipping data anywhere. Don't merge these into one flag.

- **`t.Setenv` + a package full of `t.Parallel()` tests is dangerous —
  a real flake happened during development.** A test that mutates
  `OTEL_RESOURCE_ATTRIBUTES` (or any process env var) to test
  `resource.WithFromEnv()` behavior raced against unrelated parallel tests
  in the same binary reading the same ambient environment via
  `BuildResource`. The fix was to delete that test rather than chase the
  exact scheduling semantics — `ResourceEnv`'s output format is already
  covered by two hermetic, non-env-mutating tests
  (`grpchooks_test.go`). If a future change needs to test the actual
  `WithFromEnv` round-trip, put it in a build-tagged integration test, not
  alongside this package's parallel unit suite.

- **Producer resource attribution for a plugin process is env-based, not
  a Go parameter passed down some call chain that doesn't exist.** A
  plugin subprocess's own identity reaches its `Resource` via
  `OTEL_RESOURCE_ATTRIBUTES`, which the kernel stamps into the subprocess
  environment at launch (`grpchooks.go`'s `ResourceEnv`,
  `plugin-runtime.md`'s `exec.CommandContext`) — `pkg/telemetry.Bootstrap`
  picks this up automatically via `resource.WithFromEnv()`. `BuildResource`
  also accepts an explicit `*commonv1.ProducerRef` parameter for the rarer
  case a caller already has one in hand; don't assume that's the only or
  primary path.

- **Span export now relays through `KernelCallbackService.ExportSpans` by
  default — this reverses an earlier decision, not a stale note to
  correct back.** A span-funnel RPC was originally considered and
  rejected in favor of direct per-process OTLP export; `specifications/observability.md#the-relay-model`
  records why that call was reversed (a plugin subprocess shouldn't need
  network egress/collector credentials of its own, and the kernel becomes
  the one place sampling/export config lives). `Backend.TraceUploader`
  (`telemetry.go`) plus `internal/telemetryrelay` plus
  `internal/kernelcallback`'s `ExportSpans` handler are that funnel,
  already implemented. **Trace-context propagation across the plugin
  boundary is unaffected by this** — nesting still comes entirely from
  W3C traceparent propagation over the gRPC boundary
  (`grpchooks.go`'s `ClientHandler`/`ServerHandler`); relay is a
  transport decision about where a *finished* span's bytes go, not a
  second mechanism for how an in-flight call's trace context crosses the
  boundary. Metrics deliberately do **not** get the same transparent
  relay — `RecordDynamicMetric` (`dynamicmetric.go`) records against
  kernel-owned instruments instead, per the cardinality rule below and
  `specifications/observability.md#the-tracing-metrics-asymmetry`.
  `pkg/telemetry.Bootstrap` itself hasn't been switched to build on this
  relay by default yet (tracked separately) — don't assume `Bootstrap`'s
  current env-var-driven direct export is the intended end state; it's
  what the plugin-facing SDK work will replace.

- **Corrections needed elsewhere**, tracked per project `CLAUDE.md`
  convention:
  1. ~~`configuration.md` §9 needs the `observability{}` sub-block and a
     real definition of what `telemetry = true/false` means~~ — done:
     §9 now documents the full `observability{}` block including
     `logs_enabled`, matching `internal/config`'s `settings.go`/`types.go`.
  2. ~~`internal/log/handler.go`'s `WithProducer`/`ProducerFromContext` is
     now needed by a *third* consumer (this package's future kernel-side
     callback-server span attribution, once that server exists) — extract
     it into a shared leaf package (e.g. `internal/producer`) rather than
     copying it again. Not done yet because the kernel-callback server
     itself doesn't exist yet either.~~ — done: the pair now lives in
     `internal/producer` as `producer.WithProducer`/`producer.FromContext`
     (renamed to drop package stutter). `internal/log` imports it like any
     other consumer; this package's future kernel-callback-server span
     attribution should do the same once that server exists.
  3. ~~Whether a span-funnel-through-the-kernel RPC belongs on
     `KernelCallbackService` — considered and rejected in favor of direct
     per-process OTLP export, see the bullet above (now superseded).~~ —
     reversed: `ExportSpans` (`kernel-callbacks.md`), `Backend.TraceUploader`
     (`telemetry.go`), and `internal/telemetryrelay` are that funnel, now
     implemented. `specifications/observability.md#the-relay-model`
     records the reversal's reasoning. `pkg/telemetry.Bootstrap` switching
     its *default* to build on this relay (rather than direct export) is
     separate, not-yet-done follow-up work.

## Logs integration (`sloghandler.go`)

- **`internal/log` needed zero source changes.** Its entire contract with
  the outside world is "give me a `*slog.Logger`." `slog.New(p.SlogHandler(name))`
  is a drop-in one. If you're tempted to add OTel-awareness inside
  `internal/log` itself — don't; that decoupling is the whole point, and
  it's what let this integration be purely additive.

- **Severity mapping is empirically confirmed exact, not approximate —
  verified by reading `otelslog`'s actual conversion code, not assumed.**
  `otelslog` computes `Severity = slog.Level(record.Level) + sevOffset`
  where `sevOffset = log.SeverityDebug - slog.LevelDebug = 9`. Given
  `internal/log`'s `LevelTrace = slog.LevelDebug - 4 = -8` and
  `LevelFatal = slog.LevelError + 4 = 12`, this lands on exactly
  `log.SeverityTrace1` (1) and `log.SeverityFatal1` (21) — not an
  approximate bucket, the *exact* named severity. This isn't a
  coincidence: both `log/slog`'s documented custom-level convention and
  OTel's four-sub-severities-per-band model use the same "4 units per
  step" spacing. `TestSlogHandler_severityMapping` locks this in — if it
  ever starts failing after an `otelslog`/`internal/log` version bump,
  that's a real signal to investigate, not a test to loosen.

- **No remapping wrapper was needed, and don't add one preemptively.**
  The plan for this work anticipated possibly needing a thin
  level-adjusting `slog.Handler` wrapper if the default mapping didn't
  fit. It fit exactly (see above) — `SlogHandler` just calls
  `otelslog.NewHandler` directly. If a future OTel/otelslog upgrade
  changes the arithmetic, fix it there, in this file — never in
  `internal/log`.

- **`sdk/log` v0.20.0 ships no logs equivalent of
  `tracetest.InMemoryExporter`.** Checked directly (module source, not
  guessed) — no `logtest` package exists in this version. `drivers/fake`'s
  `LogRecorder` is a hand-written in-memory `sdklog.Exporter` for this
  reason (`go-testing.md`: fakes are hand-written when nothing shipped
  fits). Similarly, `drivers/noop` hand-rolls a 3-method discarding
  `sdklog.Exporter` since the SDK ships no noop log exporter either
  (unlike `tracetest.NewNoopExporter()` for traces).

- **`sdklog.Exporter.Export` forbids retaining the records slice or its
  records without cloning.** `drivers/fake`'s `LogRecorder.Export` calls
  `Record.Clone()` before appending — don't "simplify" this to a direct
  append, it will alias SDK-internal state that gets reused/mutated after
  `Export` returns.

- **OTel Logs is pre-1.0 (`v0.20.0`/`v0.19.0` for the bridge) — expect
  more API churn than the trace/metric halves (`v1.44.0`).** Don't be
  surprised if a routine dependency bump here requires more than a
  version-number change; pin deliberately.

- **`Config.LogsEnabled`/`Provider.loggerProvider` follow the exact same
  enabled/disabled pattern as traces and metrics** — a disabled log
  signal gets `go.opentelemetry.io/otel/log/noop`'s `LoggerProvider`, never
  touching `Backend.LogExporter` at all. Keep all three signals'
  enable/disable logic symmetric if you touch `New`.
