# internal/session — agent notes

## The circuit breaker does NOT reach this package as a `*circuitbreaker.Breaker`

`Config` deliberately has no `*circuitbreaker.Breaker` field, and adding one would be wrong in this shape. The breaker instance is consumed by `internal/plangate` (`plangate.Config.Breaker`, denials) and `internal/tooldispatch` (`tooldispatch.Config.Breaker`, crashes) — **both of which sit below the `TurnDriver` seam**. A `Runner` receives an already-constructed turn driver and cannot reach into the gate or the scheduler it was built over, so it cannot inject anything into them.

What that means for the composition root (`internal/kernel`): construct the per-session `*circuitbreaker.Breaker` there, wire the same instance into `plangate.Config.Breaker` and `tooldispatch.Config.Breaker`, build the `*turn.Driver` over those, and hand that driver to `session.New`. A `Runner` is therefore constructed once **per session**, not once per process — which is fine, since `New` is cheap and holds nothing that needs sharing.

What reaches this package is the *trip signal*, not the breaker: `plangate.PrecheckResult.Tripped` and `tooldispatch.Outcome.Error.Details`' `breaker_tripped` field both bubble up into `turn.Result.TrippedProviders`, which `loop` reads. Don't "fix" the missing field by adding a `Breaker` config knob nothing can use.

## Doom-loop and circuit-breaker trips map to `SESSION_STATUS_COMPLETED`

`session.v1.SessionStatus` has exactly seven values and **no** dedicated subtype for either mechanism — verified against `pkg/session/proto/v1/types.pb.go`, not assumed. `turn-algorithm.md#limit-reached-behavior` names only the three `error_max_*` subtypes, each of which corresponds to a declared bound.

So both trips end the session as `COMPLETED`, with `Result.FinalAnswerReason` carrying `"doom_loop"` or `"circuit_breaker"`. The reasoning, in order of what was rejected:

- **Not one of the three `error_max_*` values** — each names a specific declared bound that did not fire. Persisting `error_max_turns` for a doom loop would make `session_meta` lie about which limit the operator hit.
- **Not `FAILED`** — `state-backend.md#session_meta` groups `failed` with the `error_max_*` set as terminal and replay-only, and the session genuinely did produce a usable final answer through the same graceful-degradation turn a real bound produces.
- **`COMPLETED` plus a reason** is honest about the outcome (a final answer exists) while keeping *why the loop stopped early* recoverable from `Result.FinalAnswerReason` and from the synthetic final-answer instruction `internal/turn` appends to the durable history.

If a future protocol revision adds `SESSION_STATUS_ERROR_DOOM_LOOP` / `..._CIRCUIT_BREAKER`, the change is one `switch` in `limitReached`'s callers — the reason strings already distinguish the cases.

## `Done` is checked BEFORE the bounds and the two detectors

`turn-algorithm.md` step 18 ("loop to step 1, unless a termination condition fired in 16/17 or DoneCheck") states no precedence among them. This package checks `result.Done` first, deliberately: any other order spends a whole extra model call synthesizing a "final answer" for a model that already produced one, and then persists `error_max_turns` for a session whose model genuinely finished on its last permitted turn. `TestRunDoneWinsOverAFiredBound` locks this in.

## `KernelDefaultMaxDepth` and why the depth budget is currently inert

`Config.KernelDefaultMaxDepth <= 0` resolves to `math.MaxInt32` — the *same* "effectively unbounded" sentinel `internal/kernelcallback`'s `GetSession` already reports as `RemainingDepth` (its `rootSessionRemainingDepth`), reused rather than picking a second, disagreeing number for one idea. In production, `internal/kernel` passes `settings.max_depth` through (`config.Settings.MaxDepth`, a `*int` that this package's `<= 0` rule resolves the nil case of).

The resolved figure is computed via `agentprofile.RootRemainingDepth`, recorded on `resolution.remainingDepth`, and logged at session start — but it currently **excludes nothing**. `agent-profiles.md#depth-budget` requires excluding every spawn-capable tool once remaining depth reaches zero; no loaded tool advertises spawn capability in this build (`kernelcallback`'s `RunSession` is still `codes.Unimplemented`, and nothing in `providercatalog.ToolHandle`/`toolv1.ToolSchema` marks an operation as spawn-capable). Wiring the exclusion is part of landing sub-agents, not something to fake here against a marker that doesn't exist.

## The implicit default profile's chosen values

`BuiltinDefaultProfile()` applies when no `agent_profile "default"` block exists at all ([`agent-profiles.md#the-implicit-root-profile`](../../docs/specifications/configuration/agent-profiles.md#the-implicit-root-profile)'s "kernel-builtin defaults apply for every field"):

| Field | Value | Why |
|---|---|---|
| `MaxTurns` | 200 | `agent-profiles.md`'s own `agent_profile "default"` example, verbatim |
| `MaxCostUSD` | 5.00 | same |
| `MaxWallClockS` | 3600 | same |
| `Tools` | empty | §8.3's strict default is a posture, not an artifact of a declared block — a kernel with no profile configured gets a text-only session, never the full loaded capability set |
| `SlashCommands` | empty | same strict-default posture |
| `MaxDepth` | nil | so `RootRemainingDepth` falls through to `Config.KernelDefaultMaxDepth` rather than a second hard-coded ceiling |
| `Model` | empty | falls through to the sole-loaded-model rule below |

It is a **function, not a package-level var**: `AgentProfile` carries slices, and a shared mutable global would let one caller's append leak into every later session.

**The empty `Model` block's fallback is deliberately narrow.** `resolveModel` picks the sole loaded model when the catalog holds exactly one, and returns `ErrNoDefaultModel` for zero or for two-or-more. Exactly one is the only unambiguous case; choosing among several would be an arbitrary decision made on an operator's behalf, and this project's stated posture is that ambiguity is an error.

## Grants are released on every exit path, including a panic

`Run` takes one `sessionscope.Grant` per entry in `resolution.keys` and registers `teardown` as a `defer` immediately after. That defer unregisters the live session, closes it, and runs every release func — so a panic unwinding through `Run` still cannot leak a grant, which would otherwise leave a plugin authorized to call back naming a session that no longer exists.

Two things about the grant set itself:

- **Grants are taken after resolution, not before.** A resolution failure (unknown profile, unresolvable model, malformed tool scoping) therefore leaves zero grants *and* no session file — `TestRunResolutionFailureLeavesNoGrantsAndNoSession`.
- **A hooks-only plugin gets no grant, and this is a known gap.** `providercatalog.Catalog.Hook` resolves by local name and the interface exposes no listing, so hook subscribers cannot be enumerated from this side. A plugin that serves hooks *and* a category service is already covered by its category grant (a hook subscription rides the same connection); one that serves hooks and nothing else is not. Fixing it means widening `providercatalog.Catalog`, which belongs in that package's own change, not a workaround here.

## Ordering inside finalize: status first, then session-end

`finalize` persists the terminal status via `statebackend.Session.SetStatus` **before** dispatching `session-end`. A `session-end` subscriber still holds its callback grant (grants are released in `teardown`, after) and may read this session back through `GetSession`; showing it `running` while its own end hook fires would be a lie `session_meta` has no reason to tell. `state-backend.md` states no ordering here, so this is a deliberate choice, not a spec requirement.

`finalize` also runs under `context.WithoutCancel(ctx)`. A canceled session still MUST reach `session-end` with `status = cancelled` durably recorded to its own `session_meta` row ([`subagents.md#cancellation-propagation`](../../docs/specifications/agent-loop/subagents.md#cancellation-propagation)), which is impossible on a context that is already `Done`. `TestRunCancellationMidLoop` asserts the persisted status, not just the returned one.

## A hook dispatch error is logged and swallowed

`session-start` and `session-end` are neither veto-bearing (`internal/hookdispatch` fixes the veto-bearing set at `{plan-ready, pre-tool-call}`) nor transform-mutable, so a `Dispatch` error costs no verdict and no payload edit. Failing an otherwise healthy session because a subscriber chain misbehaved would trade a working session for nothing. `TestRunSurvivesHookDispatchFailure`.

## `Debit` is this package's job, and it is called exactly once per turn

`internal/modelcall` persists the `cost_ledger` row at usage-event time but holds no `bounds.Tracker`; `internal/turn` holds none either. The session driver is the only thing on the path that can decrement the live budget, so `absorb` calls `st.budget.Debit(result.CostUSD)` — `turn.Result.CostUSD` is **one turn's** completion cost, not a running total.

The tracker itself comes from `sessionstate.Live.Budget()`, never a second `bounds.NewTracker` of this package's own — two trackers would each see half the spend. `internal/sessionstate`'s own `AppendMessage` deliberately does *not* debit, precisely so this stays the single debit site; don't add one there to "make it symmetric."

## The initial user message is not persisted

`userMessage` mints a kernel-assigned id for the prompt (determinism.md requires one) and puts it in the turn's history, but nothing writes it to the `events` table. Replaying a session therefore reconstructs every assistant message, tool call, and plan — but not the prompt that caused them.

**This is blocked on a spec decision, not on an implementation detail here.** It is tracked in [`state-backend.md`](../../docs/specifications/state-backend.md#open-questions)'s open questions; do not work around it locally. Two independent things block it:

- **No legal producer.** `events.producer_category` is the seven plugin categories, and `state-backend.md` is explicit that every `kind` except `hook_error` is written by the producing plugin's own callback connection. A user's turn has no `ProducerRef` at all, and `statebackend`'s reserved `kernel` producer is restricted to `plan`/`apply` (`kernelProducerKinds`) — an append under it for any other kind returns `ErrInvalidProducer`.
- **`message` is the wrong shape.** A `message` event's payload carries `Usage`/`cost_usd`, extracted into `cost_ledger` at write time. A user prompt has neither, and writing a zero-cost ledger row to satisfy the pairing would pollute what `SUM(cost_usd)` means.

Resolving it means either widening the reserved kernel producer to cover a user-authored `message` (with the usage/cost fields optional in that case), or giving the user turn its own `kind` and payload. Both are wire-visible. Don't reach for a local hack in the meantime — a fabricated producer or a zero-cost row would put a lie in the audit log, which is worse than the gap.

## `effectiveCeilingPercent` is this package's policy, by design

`internal/turn` refuses to invent a `ModelTarget` (`turn.ErrNoModelTarget`) precisely because resolving an effective ceiling is the session driver's decision. 80% of the declared context window is this build's documented, conservative reservation for expected output plus tool schemas, computed with integer arithmetic so it is bit-identical everywhere (determinism.md). Changing it changes what every context provider budgets against — do it here, in one place, not per caller.

## Model routing happens once per session, not once per turn

`agent-profiles.md#model-routing` describes capability-aware routing as a per-turn check, but the only requirement that varies in this build is tool-use, which is fixed for a session by its resolved tool scope. Re-routing every turn would let adjacent turns be served by different models for no reason a caller asked for. If a future turn genuinely carries different requirements (vision on one turn, thinking on another), move `resolveModel` into the loop — the function already takes the requirement as a parameter.
