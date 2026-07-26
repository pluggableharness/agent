# internal/turn — agent notes

## Recorded exception: post-model-response fires AFTER modelcall.Complete

`pluggableharness.hook.v1.PostModelResponsePayload`'s own doc comment (`api/pluggableharness/hook/v1/events.proto`) places the point "immediately after a model turn's canonical message has been assembled, **before it is persisted** (`EVENT_KIND_MESSAGE`)". This build dispatches it immediately *after* `modelcall.Complete` returns, which means after persistence. This is deliberate, and it is inert — not a violation papered over. ([`hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md) itself states no ordering relative to persistence for this point; only the proto comment does.)

Why it is unavoidable in this shape: `modelcall.Complete` owns steps 3 and 4 together and persists the message plus its `cost_ledger` row **as part of computing its own result** (`internal/modelcall/complete.go`'s `persist`, called inside the retry loop before `Response` is built). It exposes no accumulate-then-persist split, and adding one would move cost computation away from the message it belongs to — `state-backend.md` requires `cost_ledger` be populated "at the same time as the message event that produced it", and `internal/modelcall`'s own notes record that single-transaction property as load-bearing.

Why it is inert:

- `PostModelResponsePayload`'s [mutable-field table](../../docs/specifications/agent-loop/hook-dispatch.md#per-point-transform-mutable-fields) lists **zero** transform-mutable fields — `message`, `model`, `usage`, and `cost_usd` are all immutable, because "the completion has already happened; there is nothing left to transform, only to observe."
- `post-model-response` is **not** veto-bearing. `internal/hookdispatch`'s own recorded resolution of that spec gap fixes the veto-bearing set at `{plan-ready, pre-tool-call}`, and `NewRegistry` rejects a veto subscription anywhere else with `ErrVetoNotPermitted`.

So nothing a subscriber can do at this point depends on whether persistence has happened: it cannot rewrite the payload and it cannot block anything. The only observable difference is that a subscriber sees a message that is already durable — which is, if anything, the more honest thing to show it.

**If this ever stops being inert** — if a future spec revision grants `post-model-response` a mutable field or veto authority — the fix is to split `modelcall.Complete` into accumulate and persist phases and dispatch between them, NOT to reorder the calls here while leaving persistence where it is.

## The three adapters, and why plangate must keep its own types

`internal/plangate` declares its own `HookDispatcher` interface and its own `ApplyOutcome` type, and its `CLAUDE.md` states plainly that these "MUST NOT become imports of `internal/hookdispatch` or `internal/tooldispatch`." That decoupling is what lets the gate be tested against a twenty-line fake instead of a real dispatcher chain and a real scheduler. This package is the one place that holds all three, so all three bridges live in `adapters.go`:

- **`GateHooks`** wraps this package's `HookDispatcher` to satisfy `plangate.HookDispatcher`. A session wiring both packages passes `turn.GateHooks{Dispatcher: hooks}` into `plangate.Config.Hooks`.
- **`hookOutcome`** is a single Go struct conversion, `plangate.HookOutcome(o)`, because the two types are field-for-field identical (`{Payload, Decision, DeniedBy}`, same order, same types). plangate's doc comment commits to keeping it that way. If this stops compiling the types have diverged — fix whichever drifted, don't hand-roll a field copy here.
- **`applyOutcome`** IS a field copy, because `tooldispatch.Outcome` carries two fields plangate has no place for: `ExitCode` (an exec-family detail) and `Sequence` (the persisted `tool_result` event's state-backend sequence — the gate persists its own apply event and never reads it).

**No import cycle results, in any direction.** `plangate` imports neither `hookdispatch` nor `tooldispatch` nor this package; `hookdispatch` and `tooldispatch` import neither each other nor this package. Only `internal/turn` imports all of them. `adapters_test.go` holds compile-time anchors (`var _ plangate.HookDispatcher = GateHooks{}` and friends) so a drift on either side fails to build rather than failing at a wiring site.

## The pre-model-call transform is request-scoped; the compactor rewrite is durable

Two things rewrite the message list on their way to the model, and they have deliberately different blast radii:

- **A compactor's `rewritten_history`** replaces the turn's conversation history wholesale, and the replacement is **durable** — it is what `Result.History` builds on and what the next turn carries. [`context/protocol.md#session-wide-conversation-compaction`](../../docs/specifications/context/protocol.md) makes that a MUST ("the kernel MUST replace the turn's conversation history with this before the next model call").
- **A `pre-model-call` transform subscriber's `messages`** rewrites only what this one request carries. `Result.History` is built from the pre-hook base history, not from the transformed list.

The spec does not resolve this explicitly, so here is the reasoning: `hook-dispatch.md` describes `messages` mutation at this point as "redaction, injecting an additional instruction, or similar content-level rewriting **of what's about to be sent to the model**" — an egress control on one request, not an edit to the record of what the user actually said. A redaction hook that permanently deleted the redacted content from history would also be idempotently re-applied every turn anyway, so making it durable buys nothing and loses the audit trail. `TestRunTurn_preModelCallTransformIsRequestScoped` locks this in.

The synthetic **final-answer instruction** is on the durable side (appended to the base history before the hook sees it), because it is kernel-authored and explains why the model produced a text-only answer — a transcript missing it would be incoherent.

## The plan phase is skipped when a turn made no resource calls

Steps 10-12 and 14 run only when at least one surviving call is `TOOL_KIND_RESOURCE`. The algorithm as written does not guard them, so this is a deliberate departure with a reason: `plangate.Decide` persists a plan event **and one `plan_items` row per item in a single `AppendPlan` transaction**, so running it over an empty plan writes an audit row per turn describing nothing that happened. A `plan-ready` chain over a plan with zero items likewise hands a veto subscriber nothing to veto, and `plangate.Result` would build an empty `ApplyResult` for `post-apply` to carry.

The conformance MUST is about **order**, not about firing every step unconditionally regardless of emptiness — and the ordering is unchanged: when resource calls exist, steps 10, 11, 12, and 14 run exactly where the algorithm puts them. `TestRunTurn_step9bRunsInteractiveSequentially` asserts the skip directly.

## terminates_turn, and the three qualifiers on it

`turn-algorithm.md#done-detection` says a terminal tool is a DoneCheck success "immediately after that call's `post-tool-call` hook, independent of whether other `tool_use` blocks were present in the same message." Three things that phrasing does not spell out, all decided here:

1. **A successful call only.** `providercatalog.ToolHandle.TerminatesTurn`'s own doc comment says "a **successful** call of this operation is an immediate DoneCheck once its post-tool-call hook has fired." A denied or failed terminal tool leaves the loop running, so the model can react to the denial. `TestRunTurn_terminatesTurnRequiresSuccess`.
2. **The remaining `post-tool-call` dispatches still run.** By step 13 every call has already executed (steps 9/9b/12), so "ending immediately" can only mean skipping the remaining *hooks* — which would also skip their `tool_result` blocks and leave `tool_use` blocks unanswered, which several vendor APIs reject outright. The flag is set and the loop continues. `TestRunTurn_terminatesTurnEndsTurnImmediately`.
3. **Steps 14 and 15 still complete.** DoneCheck ends the *loop* (step 18), not the current turn's own bookkeeping.

## Declaration order is the invariant everything else bends around

`pending` (in `runturn.go`) is one slice, built once at step 7 in `tool_use` block order and never reordered. Steps 8-12 hold *pointers into it* and write outcomes back through them; steps 13 and 15 walk the original slice. Every grouping (`splitByKind`) produces sub-slices of pointers, never copies.

If you refactor this, the property to preserve is: **`post-tool-call` dispatches and `tool_result` blocks both emerge in `tool_use` declaration order, not grouped by kind and not in completion order.** `TestRunTurn_historyPairsResultsInDeclarationOrder` deliberately interleaves resource/interactive/data_source/resource so any grouping-based implementation fails it.

## `TrippedProviders` has three writers, and the scheduler one is easy to lose

One per-session `*circuitbreaker.Breaker` is shared by the plan gate (denials) and the tool scheduler (plugin crashes), constructed in `internal/kernel`'s `turnstack.go`. Its trips reach a caller only through `Result.TrippedProviders`, which this package fills from **three** places:

1. `runPrecheckedCalls` — a `plangate.PrecheckResult.Tripped` on a denied data_source/interactive call.
2. `recordDenials` — a `plangate.DeniedItem.Tripped` on a denied resource item.
3. `run.record` — a **crash** trip, which arrives inside `tooldispatch.Outcome.Error.Details[tooldispatch.BreakerTrippedDetail]` rather than as a field on `Outcome` (that package's `CLAUDE.md` explains why it rides there).

The third one is the one that goes missing. It was absent entirely at first: `record` copied `Result`/`Error` and nothing read the `Details` field, so the scheduler debited the breaker, stamped the trip, and no consumer existed — a repeatedly-crashing tool provider tripped its breaker and the session never noticed, re-calling it every turn until `max_turns`/cost/wall-clock fired. That is precisely the wall `plan-apply-gate.md#circuit-breaker-on-repeated-denials` exists to stop the loop running into.

Two details to preserve: `record` is a **method** on `run` (it was a package-level func, which is why it had no way to reach `r.tripped`), and it records `p.handle.Provider` rather than the `Details`' own provider field, so all three writers agree on the agent.hcl local name. `TestRunTurn_schedulerCrashTripSurfacesAsTrippedProvider` guards it, with `TestRunTurn_schedulerSuccessLeavesNoTrippedProvider` as the negative control.

## Other things worth knowing

- **`ScopedTools` is keyed by the scoped `"<provider>.<tool>"` name, but `ToolCall.tool_name` is the bare schema name.** The map key is what `agentprofile.ResolveTools` produces and what the model sees in a `ToolUseBlock`; the wire `ToolCall` carries the provider-local operation name, because `tool.v1.ToolCall.tool_name` is documented as matching "a `ToolSchema.name` from **this provider's** `GetSchema` response". Don't collapse the two.
- **`CallHashes` hashes the SCOPED name, deliberately.** Two providers can advertise the same operation name; hashing the bare one would make two unrelated calls collide into a false doom loop. This is the one place the scoped name reaches a hash.
- **An out-of-scope `tool_use` block never reaches a hook.** There is no resolved handle, so there are no `kind`/`risk`/`description` snapshot fields to mint a `PlanItem` from and `PreToolCallPayload.plan_item` could not be populated. It resolves straight to an error `tool_result` in its own declaration slot. Same for a schema declaring `TOOL_KIND_UNSPECIFIED`: `plan-apply-gate.md`'s whole decision structure is kind-driven, so an unclassifiable call is denied rather than executed under a guessed kind.
- **A plan-gate denial reuses `plangate.DenialBlocks`' block verbatim**, slotted into the denied call's own declaration position. A pre-tool-call veto and a precheck denial build their blocks here instead, but in the identical shape and with wording that mirrors plangate's ("`<provider>.<op>` was denied (`<decided_by>`); this call was not executed"). Keep the vocabularies aligned — and keep the wording neutral: a fail-closed veto and a considered one arrive identically, and `internal/hookdispatch`'s notes are explicit that the **absence** of a `hook_error` proves nothing about which it was. Don't write text claiming a subscriber examined the call.
- **`Config` fields are interfaces, not the concrete collaborator types.** This is what makes the conformance test possible — five hand-written fakes appending to one shared ordered log. It also follows the "define the interface where it is consumed" rule `internal/plangate` and `internal/providercatalog` already apply. `hookdispatch.Outcome`, `plangate.Decisions`, `tooldispatch.Outcome`, and friends are used verbatim inside those interfaces: a second Go representation of a collaborator's result would be exactly the parallel type `go-layout.md` forbids in `internal/`.
- **`Driver` is immutable; `run` holds the mutable per-turn state.** That is what keeps concurrent `RunTurn` calls safe. Don't move `tripped` (or anything else per-turn) onto `Driver`.
- **Cancellation returns a bare `ctx.Err()` and leaves the turn span OK.** `RunTurn` checks `ctx.Err()` before assigning `spanErr`, the same pattern `internal/modelcall` uses — a canceled turn is normal control flow (`.claude/rules/grpc.md`), not a failed one. `TestRunTurn_cancellationPropagatesUnwrapped` asserts the error is not wrapped.
- **`ErrOutcomeCount` guards a scheduler contract violation rather than trusting it.** If a scheduler returns a different number of outcomes than calls, pairing them by index would attach a result to the wrong `tool_use` block — a silently wrong conversation. Failing the turn is the correct blast radius.
