# internal/hookdispatch — agent notes

## Recorded spec-gap resolution: which points are veto-bearing

`hook-dispatch.md` uses the phrase "veto-bearing hook point" three times — its dispatch pseudocode's `decision` comment ("only meaningful at veto-bearing hook points, e.g. plan-ready"), [`#veto-mode-subscription-trust-model`](../../docs/specifications/agent-loop/hook-dispatch.md#veto-mode-subscription-trust-model)'s opening sentence, and by implication in [`#timeout-behavior`](../../docs/specifications/agent-loop/hook-dispatch.md#timeout-behavior) — and **never enumerates the set**. `points.go`'s `vetoBearingPoints` is this kernel's resolution of that gap, not a rule the spec states:

```
{HOOK_POINT_PLAN_READY, HOOK_POINT_PRE_TOOL_CALL}
```

The reasoning: these are the two points that immediately precede a blockable action — `plan-ready` is the terminal gate before a plan applies, `pre-tool-call` the terminal gate before one tool call executes. Every other point either fires after the action it describes has already happened (`post-model-response`, `post-tool-call`, `post-apply`, `session-end` — the mutable-field table calls three of them out as "the completion has already happened"/"applying has already happened"/"the outcome is already final") or gates nothing a deny could meaningfully stop (`session-start`, `pre-model-call`).

`NewRegistry` rejects a `veto`-mode subscription anywhere else with `ErrVetoNotPermitted`, and `Pin` panics at a non-veto-bearing point. **This is an interpretation.** If the spec later enumerates the set, change `vetoBearingPoints` and this section in the same commit — don't leave the code and this note disagreeing, and don't widen the map without a citable spec sentence.

## Parent cancellation is not a subscriber timeout — and the distinction is load-bearing

Two things look identical from inside a `DispatchHook` call: the per-subscriber `context.WithTimeout` firing, and the caller's parent ctx being canceled. Both surface as a canceled/deadline-exceeded error on the call. They mean opposite things:

- **The subscriber's own deadline fired.** That subscriber failed. At a veto point this MUST fail closed to `DENY` ([`#timeout-behavior`](../../docs/specifications/agent-loop/hook-dispatch.md#timeout-behavior): "a hanging policy subscriber at plan-ready MUST result in deny"). The whole point of fail-closed is that a hanging subscriber cannot widen what gets auto-applied.
- **The parent ctx was canceled.** The turn or session is being torn down. Nobody is waiting for a decision. Manufacturing a `DENY` here would persist a `hook_error` and a denial for a turn that is being abandoned anyway — a misleading, permanent record of a decision that was never really made, and one that would show up in a replay of a session that was simply interrupted.

`Dispatch` distinguishes them by checking the **parent** `ctx.Err()` — never the per-subscriber ctx — immediately after each call returns, before any mode-specific handling. A non-nil parent error abandons the chain and returns that error wrapped, with `Outcome.Decision` left at its zero value and nothing persisted. The same check runs at the top of each loop iteration and inside `runKernelVeto`.

Two consequences to preserve if you touch this:

- **Order matters.** The parent check comes *before* the veto fail-closed branch. Reversing them turns every canceled turn into a fabricated denial.
- **`Outcome.Decision` is meaningless when `Dispatch` returns an error.** A caller must check `err != nil` first; the zero value is `HOOK_DECISION_UNSPECIFIED`, deliberately not `ALLOW` or `DENY`, so a caller that forgets can't accidentally read a plausible-looking verdict.

`TestDispatchParentCancellationIsNotADeny`, `TestDispatchAlreadyCanceledParent`, and `TestDispatchKernelVetoParentCancellation` lock all of this in. They are not redundant with the timeout tests — they exercise the branch that distinguishes the two.

## hook_error's producer is the FAILING SUBSCRIBER, never the kernel

[`state-backend.md#the-kind-enum`](../../docs/specifications/state-backend.md#the-kind-enum) is explicit: `hook_error` is kernel-*synthesized* but carries the failing subscriber's identity in `producer_category`/`producer_name`/`producer_version`. `persistHookError` therefore uses `Subscriber.Producer` — **never** `statebackend.KernelProducer()`.

This is enforced on the other side too, so a mistake here fails loudly rather than silently: `internal/statebackend`'s `kernelProducerKinds` contains only `PLAN` and `APPLY`, and `encodeProducer` rejects the reserved kernel producer on any other kind. An attempt to write a `hook_error` under the kernel identity returns `ErrInvalidProducer` at append time.

**A `hook_error` is asymmetric evidence, and a consumer reasoning about the audit trail must treat it that way.** Its presence proves the denial at that point was fail-closed; its **absence proves nothing**, because persistence is deliberately best-effort in three ways. `New` accepts a nil `EventSink` — a caller with no live session (config validation, a dry run) has nowhere to append to, and nothing is persisted at all. A failed `AppendEvent` is logged at `ERROR` and dropped, because the dispatch outcome is already decided by the time it is persisted and an audit append must never change a gate's verdict. And a failing `KernelVeto` can never produce one (next paragraph). So "no `hook_error` for this point, therefore the DENY was a considered verdict" is an unsound inference — don't build a UI or a forensic story on it.

**The corollary, which is easy to get wrong:** a failing `KernelVeto` (the policy engine) has **no `ProducerRef` at all** — it is not a plugin, and it is structurally impossible to persist a `hook_error` for it. `runKernelVeto` therefore logs at `WARN` and increments the hook-error counter, and persists nothing. Don't "fix" this by reaching for `KernelProducer()`; statebackend will reject it, and the spec says the field identifies a subscriber, which policy is not one of in the plugin sense. If policy's own failures need to be persisted, that needs a separate event kind and a spec change, not a widened producer rule.

## Don't pin a policy veto next to a plan gate that already evaluates policy per item

`KernelVeto`'s doc comment says the policy engine is the only intended implementation, which follows [`architecture.md#policy--first-party-not-a-plugin-category`](../../docs/specifications/architecture.md#policy--first-party-not-a-plugin-category). Read that as "no *plugin* may hold this slot" — **not** as "policy is expected to be pinned here in every build."

Policy's real evaluation path is per-item and does not come through this package at all: `internal/policy.Evaluate(rules, call)` runs per call and returns the matched rule's name, which is what [`plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md) records in a plan item's `decided_by` ("subscriber/policy-rule name that produced the decision"). That matches [`#veto-mode-subscription-trust-model`](../../docs/specifications/agent-loop/hook-dispatch.md#veto-mode-subscription-trust-model), which describes policy as "producing per-item `PlanDecision`s" and notes a third-party veto subscriber "cannot express `PlanDecision`'s per-item PENDING/ALLOW/ASK/DENY granularity."

So a plan gate that already evaluates policy per item before dispatching `plan-ready` must **not** also `Pin` a policy veto into that chain. Doing so evaluates the same rules twice at different granularities, and the coarse one wins the audit trail: a plan-wide `hook-veto:<name>` row where a per-item `policy:<rule>` row is the correct record. `Pin` exists for a kernel veto that has no other path into the chain, not as a second front door for policy.

The ordering guarantee `Pin` provides is unaffected by leaving it unused — it exists so that *if* a kernel veto is pinned, it runs ahead of every plugin subscriber. An empty pin slot means the chain is plugin subscribers only, which is exactly right when policy has already decided per item upstream.

`internal/plangate` documents the same constraint from its side. If a future build genuinely needs both a per-item policy pass and a pinned kernel veto, `Outcome` needs an explicit origin field so a caller can tell "policy denied" from "a third-party hook veto denied" — do that rather than pattern-matching on `DeniedBy`, which carries a bare name with no marker of which kind of subscriber produced it.

## Other things worth knowing

- **`Position.FileIndex` exists because the ordering spec assumes one file and this project allows several.** `agent-profiles.md` says textual position is unambiguous "because `agent.hcl` is a single file", but `architecture.md`'s XDG layout permits "+ other `*.hcl` in project dir, merged". `NewRegistry` resolves the multi-file case by sorting filenames **lexicographically** — never by filesystem enumeration order, which would make chain order depend on directory iteration (`determinism.md`). It derives the indices itself from the `hcl.Range` filenames; a caller never assigns one.
- **The ordering is a total order, and that is what makes `SortStableFunc` safe to rely on.** Two subscriptions can never share a `(FileIndex, ByteStart)` pair in one chain, because a duplicate `(provider, point)` is rejected at construction and two distinct blocks in one file cannot start at the same byte.
- **`NewRegistry` takes implicit subscriptions as a parameter rather than deriving them.** No category-to-hook-point derivation table exists anywhere in this codebase or in any spec table that could be cited — `agent-profiles.md` describes implicit subscriptions in prose ("a context provider is automatically subscribed to `context-assemble`, policy is automatically the privileged `veto` subscriber at `plan-ready`") without a normative mapping. Inventing one here would be a fabricated table wearing the kernel's authority. Whichever component eventually learns each loaded plugin's category-implied points builds `[]Implicit` and hands it over. When that component lands, this note should point at it.
- **`context-assemble` is not dispatchable here, and its rejection is deliberately distinct from an unknown label.** [`#hook-points`](../../docs/specifications/agent-loop/hook-dispatch.md#hook-points) keeps it on `ContextService.Contribute`. `pointFromText` gives it its own error message so an operator who writes `hook "context-assemble" {}` learns why rather than being told the point doesn't exist.
- **The point and mode string vocabularies come from `internal/telemetry`'s constants, not fresh literals.** `points.go`'s maps reference `telemetry.HookPointPreModelCall` and friends so the kernel has exactly one spelling of each name across config parsing, dispatch, and span attributes.
- **Two error categories are resolved here rather than in `internal/hookpayload`.** `errorCategory` maps a fired deadline to `HOOK_ERROR_CATEGORY_TIMEOUT` and `codes.Unavailable` to `HOOK_ERROR_CATEGORY_PROCESS_CRASHED` (`grpc.md`'s `process_crashed` mapping), then defers everything else to `hookpayload.Category`. This split is intentional: `hookpayload` is pure domain and never sees a gRPC status or a context deadline, so it cannot classify either.
- **`ConcurrentObserve` defaults off, and its failure persistence stays ordered.** The spec's parallelism MAY is a latency optimization; sequential dispatch is the deterministic default. With it on, a concurrent run's calls interleave but `runObserveRun` persists their `hook_error` events by declaration index afterward, so event `sequence` doesn't depend on which call finished first.
- **`Instruments().HookErrors` carries only the hook point and subscriber mode.** Never a producer name or session id — `internal/telemetry`'s cardinality rule. Producer attribution lives on the span (`StartHookSubscriber`) and on the persisted event.
