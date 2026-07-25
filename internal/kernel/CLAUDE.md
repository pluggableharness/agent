# internal/kernel — agent notes

## The turn stack is built lazily, and that is not an optimization

`turnStack` (turnstack.go) is the `session.TurnDriver` handed to `session.New`, and it builds the real `*turn.Driver` — plus everything under it — on the **first `RunTurn` call**, keyed on `turn.Request.SessionID`. This resolves a genuine circular requirement, not a performance concern:

- `plangate.Config.SessionID` is required at construction, and `plangate.New` **panics** without its other required fields.
- One `*circuitbreaker.Breaker` is scoped to one session and must be the **same instance** in `plangate.Config.Breaker` and `tooldispatch.Config.Breaker` — `internal/session` deliberately has no `Breaker` field because both consumers sit below its `TurnDriver` seam ([`internal/session/CLAUDE.md`](../session/CLAUDE.md)).
- Five packages (`contextassembly`, `modelcall`, `tooldispatch`, `hookdispatch`, `plangate`) each declare their event sink as `*statebackend.Session`'s own `Append*` signatures, so each needs that session's open handle at construction.

But **`internal/session` mints the session id and creates the session file itself**, inside `Runner.Run` — which is called with an already-constructed turn driver. `turn.Request.SessionID` is the first place the id becomes visible from above, so that is where the per-session half of the stack gets built.

The seam has two halves:

- **`sessionSink`** — an `atomic.Pointer[sessionstate.Live]` forwarder, the same shape and the same justification as [`internal/pluginhost`'s `callbackSlot`](../pluginhost/slot.go). Read that file before proposing a different mechanism.
- **The sink binds a `*sessionstate.Live`, never the raw `*statebackend.Session` that `Live` wraps.** It used to bind the raw handle, which persisted every kernel-originated event correctly and published none of them — `Live`'s `Append*` methods are what serialize a session's writes under one lock *and* republish each committed event onto the reserved `kernel.event.{kind}` topic ([`event-bus.md#the-kernel-namespace`](../../docs/specifications/event-bus.md)), so bypassing them meant a plugin subscribed to `kernel.event.*` saw other plugins' `Emit` calls and never a `message`, `tool_call`, `tool_result`, `plan`, or `apply`. `Live.AppendEvent`/`AppendMessage`/`AppendPlan` take the caller's own already-built `statebackend.Event`, so the five collaborators keep owning their event ids and timestamps exactly as before. The `Session()` accessor that made the old wiring possible has been removed; don't reintroduce it.

### The one window where the sink is unbound

`session-start` is dispatched *before* the first turn, so `hookdispatch`'s event sink is still unbound when it fires. A `hook_error` at `session-start` is therefore logged and counted but not persisted. This is tolerable rather than papered over: `hookdispatch` explicitly accepts a nil sink and treats `hook_error` persistence as best-effort, and [`internal/plangate/CLAUDE.md`](../plangate/CLAUDE.md) already records that the *absence* of a `hook_error` proves nothing. Closing it properly means `internal/session` handing out its open session handle at creation time; do that there, not with a heuristic here.

### One root session per process

`driverFor` refuses a second, different session id outright. Quietly rebuilding the stack would rebind the shared sink out from under the first session. When sub-agents land (`RunSession` is still `codes.Unimplemented`), the fix is a `sessionID -> stack` map plus one `sessionSink` per session — not relaxing the refusal.

## Defaults chosen here, and why they are judgment calls

None of these has a spec-mandated value. They follow the framing `internal/config` already uses for `DefaultHookTimeoutMS`/`DefaultToolTimeoutMS`: documented, changeable in one place, never presented as protocol.

| Constant | Value | Reasoning |
|---|---|---|
| `breakerConsecutiveThreshold` | 3 | [`plan-apply-gate.md#circuit-breaker-on-repeated-denials`](../../docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials) names "N consecutive" and no N, and is a SHOULD. Three back-to-back denials is the shortest run that is unambiguously a storm rather than a model exploring adjacent calls after one refusal. |
| `breakerWindowSize` / `breakerWindowThreshold` | 20 / 8 | Catches the oscillating case the consecutive counter misses (deny, deny, allow, deny, …), which never reaches three in a row but is still looping against a wall. |
| `sessionMaxRetries` | 20 | [`error-recovery.md#model-provider-errors`](../../docs/specifications/agent-loop/error-recovery.md) requires a session-wide cap tracked separately from `settings.retry.max_retries` (default 5) but names no figure. Twenty allows four fully-exhausted attempt chains before the kernel stops paying for a provider that is evidently down. |
| `shutdownTimeout` | 15s | More than `pluginhost.Supervisor`'s own drain-then-kill needs across a handful of plugins; short enough that an operator does not reach for `kill -9`. |

Timeouts are **not** re-defaulted here — `hookTimeout`/`toolTimeout` fall back to `config.DefaultHookTimeoutMS`/`DefaultToolTimeoutMS`, so there is exactly one source of truth. Same for `doomLoopConfig` (falls back to `doomloop.DefaultConfig`) and `maxDepth` (uses `math.MaxInt32`, the same "effectively unbounded" sentinel `internal/kernelcallback` and `internal/session` already agree on).

## No implicit hook subscriptions exist yet

`hookdispatch.NewRegistry` is called with an **empty** `[]Implicit`. No category-to-hook-point derivation table exists anywhere in this codebase or in any spec table that could be cited, and `hookdispatch.Implicit`'s own doc comment refuses to invent one ("inventing one here would be a fabricated mapping wearing a kernel's authority"). Only explicit `hook{}` blocks from `agent.hcl` subscribe.

Consequence worth knowing: a context provider does **not** currently get `context-assemble` by category alone — though `context-assemble` never went through `hookdispatch` anyway (it stays on `ContextService.Contribute`). Whichever component eventually learns each loaded plugin's category-implied points builds those `Implicit` values and this call site passes them through; the parameter is already there.

## The two tracked deviations, and where the loud part lives

Both are wired in `newTurnDriver` (turnstack.go), deliberately at one call site so a review sees them together:

- **`autoallow`** — the block comment above `autoallow.New` is the unmissable acknowledgment the driver's own package doc demands, plus an explicit `WARN` naming the session id and `autoallow.DecidedBy`. `Config.AcknowledgeUnsafeAutoAllow` must be literally `true` or construction fails. Do not soften, relocate, or "clean up" any of it — read [`internal/plandecision/drivers/autoallow`](../plandecision/drivers/autoallow)'s `CLAUDE.md` first.
- **`unattended`** — auto-*refuses* every interactive call. The asymmetry with autoallow's auto-*approve* is deliberate and explained in that package's doc comment: an `ask` item has a defensible default (the call as proposed), an interactive call does not (its whole payload is a human's answer, and any synthetic one is a lie in the model's own history).

## Logging goes two places, on purpose

`startLogging` installs a `fanoutHandler` over a stderr text handler and — only when the operator enabled both telemetry and the logs signal — `telemetry.Provider.SlogHandler`. Choosing one would either lose the operator's console output or silently drop the OTel logs signal, and [`logging-telemetry.md`](../../.claude/rules/logging-telemetry.md) treats both as mandatory. A one-target fanout returns that target unwrapped, so the telemetry-off case pays nothing.

This is the one sanctioned `slog.SetDefault` in the tree ([`go-style.md`](../../.claude/rules/go-style.md)'s single global-state exception). It is what makes every package's own `slog.Default()` fallback land on the operator's configured level and destination.

## Tests

`kernel_integration_test.go` (`//go:build integration`) runs a whole session end to end against a real model-provider plugin subprocess built from `testdata/plugin` and reached through `dev_overrides`. It is a **third** fixture deliberately: `internal/pluginhost`'s and `internal/pluginruntime`'s both serve the tool category only, and a session cannot resolve a profile without a model provider.

The fixture binary is built into the repo's `bin/`, not `os.MkdirTemp` — the project `CLAUDE.md`'s "bin/ only, no exceptions" covers test fixtures too, even where a temp dir would be the obvious choice.
