---
paths:
  - "internal/**/*.go"
---

# Logging & telemetry are mandatory, not optional polish

Any `internal/` package or function in scope (see below) MUST integrate both
`log/slog` (the sole sanctioned logging mechanism, `go-style.md`) and
`internal/telemetry` (the OTel-native tracing/metrics module). This is part
of the definition of "done" for such code — a change that skips it MUST be
treated as incomplete, not as a follow-up to file later. Requirement
keywords (MUST/SHOULD/MAY) are RFC 2119, matching `docs/specifications/*`
and every other file in `.claude/rules/`.

## Scope — instrumentation is mandatory whenever code:

- **Performs I/O** — network calls, subprocess exec, file I/O, sqlite
  access, any `hashicorp/go-plugin` RPC.
- **Implements a driver** (`go-layout.md`'s driver pattern). Every driver
  method that does real work MUST log entry/exit at `DEBUG` and MUST wrap
  non-trivial operations in a span.
- **Implements a gRPC handler** — a `KernelCallbackService` method, a
  plugin-category server method, or anything serving an RPC. MUST log
  `DEBUG` on entry and `WARN`/`ERROR` on failure, MUST be wrapped in a span.
- **Crosses a plugin process boundary**, in either direction.
- **Implements a kernel-loop attach point** — a hook dispatch, a turn step,
  a tool/model call. MUST use the named `Start*` helpers in
  `internal/telemetry/span.go` — MUST NOT hand-roll a parallel
  `tracer.Start(...)` call anywhere else in the tree.

## Exemption — pure-domain packages MUST NOT instrument

`internal/policy`, `internal/agentprofile`, and any future package of the
same shape (I/O-free, deterministic, single-threaded, ~95%-covered per
`go-testing.md`) **MUST NOT** import `log/slog` or `internal/telemetry`
directly. No exceptions.

Why this is absolute, not a judgment call: instrumentation is itself a side
effect — a `slog.Handler` write or an OTel span-start append is I/O-adjacent.
Injecting either into a pure function breaks the exact contract the package
exists to provide and risks leaking non-determinism into replay-critical
code (`determinism.md`). The **caller** logs or wraps a span around the pure
function's result — the pure function itself stays untouched, always.

Test for whether the exemption applies: the package's own tests run at
~95% coverage using only in-memory inputs/outputs, with zero fakes for
external systems. If yes, it's pure domain, full stop.

## Logging rules — non-negotiable

- **`log/slog` only.** No `fmt.Println`/`log.Printf`, ever, anywhere in
  scope (already `go-style.md`; restated because every rule below depends
  on it).
- **Six-level vocabulary, no substitutes**: `TRACE < DEBUG < INFO < WARN <
  ERROR < FATAL`, from `internal/log/level.go` /
  `docs/specifications/kernel-callbacks.md#log`. MUST use `internal/log`'s
  `LevelTrace`/`LevelFatal` constants. MUST NOT invent a second definition
  of "trace severity" as a raw `slog.Level` value.
  - **TRACE** — noisy, step-by-step internal detail. Off by default.
  - **DEBUG** — diagnostic detail for troubleshooting one package: driver
    entry/exit, decision branches taken, cache hits/misses.
  - **INFO** — normal lifecycle an operator cares about by default: driver
    startup, a handshake succeeding, a session starting/ending, a
    migration running.
  - **WARN** — a recoverable anomaly: a retry, a fallback path taken, a
    deprecated config path in use.
  - **ERROR** — an operation failed and is being handled. MUST be paired
    with returning the error. MUST NOT both log and return the same error
    (`go-style.md`: a function returns an error or logs it, never both).
  - **FATAL** — a severity label only. MUST NOT terminate the process or
    plugin under any circumstance.
- **MUST attach structured correlation attributes** wherever applicable —
  `session_id`, `producer_category`/`producer_name`/`producer_version` —
  matching the field names `internal/log/handler.go` already attaches to
  plugin-sourced entries, so kernel-native and plugin-sourced logs
  correlate under identical field names.
- **MUST NOT log secrets, at any level, including `TRACE`.**
  `docs/specifications/configuration/blocks-reference.md`'s secret-handling
  rule (`hclsecret`-marked provider values, API keys, tokens) has zero
  log-level exceptions — "it's just a debug log" is never a defense.

## Telemetry rules — non-negotiable

- Every driver implementation MUST record a span per non-trivial operation
  and MUST increment metrics via `internal/telemetry.Instruments`. An ad
  hoc `time.Since` log line or a package-local counter is never an
  acceptable substitute.
- MUST reuse an existing `Start*` helper from `internal/telemetry/span.go`;
  if none fits, add one there — following that file's exact
  `(context.Context, trace.Span)` pattern, ended via `telemetry.EndSpan` —
  rather than hand-rolling a `tracer.Start` call somewhere else.
- **Cardinality discipline is non-negotiable, regardless of which package
  is emitting.** Unbounded identifiers (session IDs, turn indices, request
  IDs) go on spans only — MUST NOT become a metric attribute, ever. A new
  metric attribute MUST reuse an existing low-cardinality key
  (`ToolNameKey`, `ModelIDKey`, etc.) rather than inventing an unbounded
  one.
- `context.Context` MUST carry the live span through every call boundary.
  MUST NOT construct a fresh `context.Background()` partway through a call
  chain that has an active span — doing so silently severs trace
  parentage without erroring, making the break invisible until someone is
  staring at a broken trace tree.
- Any new plugin-category client/server, kernel-callback implementation,
  or gRPC-dialing/serving driver MUST wire
  `internal/telemetry.Provider`'s `ClientHandler()`/`ServerHandler()` into
  its `grpc.WithStatsHandler`/`grpc.StatsHandler` options
  (`internal/telemetry/grpchooks.go`). MUST NOT hand-roll a separate
  trace-context propagation mechanism.
- Replay-path code (`docs/specifications/state-backend.md`) MUST select the
  `noop` telemetry driver, unconditionally, no exceptions. Telemetry MUST
  NOT persist `trace_id`/`span_id` into any table and MUST NOT recompute a
  cost/token figure the kernel already computed elsewhere
  (`determinism.md`).

## Enforcement checklist — apply at review time, not just when writing

- [ ] Every new I/O-touching function has an entry-level `DEBUG` log, a
      span if it crosses a process/network boundary, and logs at `ERROR`
      only where the error is swallowed (never both logged and returned).
- [ ] Every new driver instruments identically to its siblings in the same
      `drivers/` directory — a driver instrumenting differently from its
      siblings is a bug, not a style choice.
- [ ] Pure-domain packages (`policy`, `agentprofile`, and anything in that
      mold) have **zero** `log/slog` or `internal/telemetry` imports.
- [ ] No new metric name exists outside `internal/telemetry.Instruments`.
- [ ] No unbounded value (session ID, turn index, request ID) appears as a
      metric attribute anywhere.
