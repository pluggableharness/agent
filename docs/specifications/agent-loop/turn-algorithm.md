# Turn algorithm

The kernel's own control-loop algorithm for one turn, plus the loop-level bounds that decide when a session's turns stop. See [`README.md`](README.md#scope-and-definitions) for what a "turn" and "session" mean precisely.

## The `RunTurn` algorithm

```go
RunTurn(session):
  1.  payload := HookDispatch("context-assemble", {history, budget})
  2.  request := HookDispatch("pre-model-call", {payload, tool_specs, params})
  3.  stream  := ModelProvider.StreamCompletion(request)
  4.  message := accumulate(stream)   // canonical content-block message
  5.  message := HookDispatch("post-model-response", message)
  6.  if message has no tool_use blocks:
        goto DoneCheck
  7.  for each tool_use block in message (declaration order):
        HookDispatch("pre-tool-call", tool_call)
  8.  data_source_calls, resource_calls, interactive_calls :=
        split_by_kind(tool_use_blocks)
  9.  results_ds := execute_concurrently(PolicyPrecheck(data_source_calls))
  9b. results_int := execute_sequentially(PolicyPrecheck(interactive_calls))
  10. plan := build_plan(resource_calls)
  11. plan := HookDispatch("plan-ready", plan)   // veto chain; policy engine runs here
  12. results_r := apply_approved_items(plan)     // per ConcurrencySpec
  13. for each (call, result) in results_ds ∪ results_int ∪ results_r:
        HookDispatch("post-tool-call", {call, result})
  14. HookDispatch("post-apply", plan)
  15. history := history ++ message ++ tool_result_blocks(results_ds, results_int, results_r)
  16. DoomLoopCheck(history)
  17. BoundsCheck(session)
  18. loop to step 1, unless a termination condition fired in 16/17 or DoneCheck
```

`session-start` fires once before the first `RunTurn`; `session-end` fires once after the loop exits (normal completion, a fired bound, or cancellation). Steps 1–2 and 5–7 are plain hook chains (see [`hook-dispatch.md`](hook-dispatch.md)); steps 8–13 are the resource/ data-source/interactive split and the plan/apply gate proper (see [`plan-apply-gate.md`](plan-apply-gate.md)). Step 9's data-source calls and step 9b's interactive calls both run a lightweight policy precheck before executing — every call the model makes passes through policy in some form, whether or not it has an apply step behind it; see [`plan-apply-gate.md#data-source-and-interactive-calls`](plan-apply-gate.md#data-source-and-interactive-calls) for exactly what that precheck does.

A kernel implementation MUST execute steps 1 through 18 in the order shown for every turn. It MAY pipeline non-dependent work (e.g. prefetching the next `context-assemble` pass) only where doing so is observably indistinguishable from strict sequential execution — the ordering guarantee, not the literal execution schedule, is what's required.

Every one of these 18 steps, and every hook dispatch nested inside them, is a mandatory instrumentation point: a conforming kernel MUST emit a span or equivalent trace event for the whole turn, for the model-call steps (3–4), for tool execution (steps 9/9b/12), and for the policy veto chain (step 11), so a turn's execution is fully reconstructible from its trace.

## Turn-level tool-call concurrency

Two resource/data-source calls within one turn can run in parallel, or a tool provider can decline that with a "not concurrency-safe" declaration — this is governed by [`tool/data-types.md#concurrencyspec`](../tool/data-types.md#concurrencyspec), not by `kind` directly. `kind` continues to drive only the plan/apply gate ([`plan-apply-gate.md`](plan-apply-gate.md)), not scheduling:

- For every `tool_use` block in a turn (both `data_source` and `resource`), the kernel computes a concurrency key: `(provider_name, tool_name, value(key_fields))` when the operation declares `safe: true`, or a provider-wide lock token when `safe: false` or undeclared — the conservative default.
- Calls with distinct keys MUST execute concurrently. Calls sharing an identical key, or belonging to the same `safe: false` operation, MUST execute sequentially.
- This reduces to the safe default when a provider declares nothing fancy: `data_source` operations SHOULD declare `safe: true` with no `key_fields` — effectively a distinct key per call, so concurrent by default. `resource` operations default to `safe: false` when undeclared — provider-wide sequential, as a conservative default. A tool provider wanting finer-grained parallelism (e.g. two file-write `resource` calls to different paths running in parallel) declares `safe: true, key_fields: ["path"]`.
- Step 9/12's split still governs *approval gating* (`data_source` executes freely, `resource` goes through the plan/apply gate); the concurrency key above governs *scheduling* within each group — one mechanism for both, not two separate rules.

`interactive` calls are the one exception: they MUST execute sequentially regardless of any declared `ConcurrencySpec`, and `ConcurrencySpec` MUST NOT even be declared for an `interactive` operation — see [`plan-apply-gate.md#data-source-and-interactive-calls`](plan-apply-gate.md#data-source-and-interactive-calls).

## Loop termination and bounds

### Independent bound dimensions

Harnesses that ship only one bound dimension consistently regret the omission of the others (a hard turn count alone, or a cost/token budget alone, or a hard iteration count alone, each miss a real failure mode the others catch). The kernel MUST track three independent bound dimensions per session, checked at step 17 of every turn:

```protobuf
LoopBounds {
  max_turns          int       // MUST be supported and independently configurable
  max_cost_usd        float64   // SHOULD be supported
  max_wall_clock_s     int       // SHOULD be supported
}
```

A kernel MAY additionally adopt a weighted-token cost model as a refinement layered on top of the actual dollar figure below, not a replacement for it: `weighted_tokens = output_tokens * sampling_token_weight + non_cached_input_tokens * prefill_token_weight`. This is a refinement of `max_cost_usd`, not a fourth independent dimension.

Each bound MUST be checked independently — hitting any one of the three MUST terminate the loop via the same graceful-degradation path (see [Limit-reached behavior](#limit-reached-behavior)), not three different paths.

### Cost accounting

`ModelSpec.pricing` ([`model/data-types.md#pricing`](../model/data-types.md#pricing)) is what makes per-turn cost computation possible: the kernel MUST compute and persist `cost_usd` per `usage` event at receipt time, per [`model/protocol.md#cost-computation`](../model/protocol.md#cost-computation). `max_cost_usd` tracking is exactly that: the kernel MUST accumulate a running sum of `cost_usd` across every `usage` event in a session and compare it against `max_cost_usd` at step 17.

**Cost rolls up the session tree** — the same reasoning [Depth limits](subagents.md#depth-limits) already establishes for `max_depth`. A session's `max_cost_usd` MUST account for its own spend *and every descendant `RunSession`'s spend*, not just its own direct model calls — otherwise a cost bound is trivially defeated by spawning sub-agents to do the expensive work:

```text
remaining_cost_budget_usd(root_session) = root_profile.max_cost_usd ?? +inf
remaining_cost_budget_usd(child) = min(remaining_cost_budget_usd(parent),
                                        child_profile.max_cost_usd ?? +inf)
```

Every `usage` event's computed `cost_usd` MUST be atomically subtracted from `remaining_cost_budget_usd` at **every** session on the path from where the spend occurred up to the root — the kernel's own in-memory session-tree state — the same live tracking depth-budget threading and cancellation propagation use ([`../state-backend.md#live-vs-post-hoc-tree-walking`](../state-backend.md#live-vs-post-hoc-tree-walking)), not a state-backend read — is what makes this walk possible. When any session's `remaining_cost_budget_usd` reaches `<= 0`, that session's bound has fired ([Limit-reached behavior](#limit-reached-behavior)), regardless of whether the spend that tripped it happened in that session directly or in a descendant. `RunSessionRequest` ([`subagents.md#data-types`](subagents.md#data-types)) carries a `remaining_cost_budget_usd` field alongside `remaining_depth`, computed identically at spawn time.

### Limit-reached behavior

Injecting a structured "final answer" turn — disabling tools and forcing text-only synthesis — produces materially better user-facing output than throwing a hard error. When a bound fires at step 17:

- The kernel MUST NOT raise an unrecoverable error as the default behavior.
- The kernel MUST run exactly one more turn with tool specs withheld from the request (mirroring the plan-mode schema-removal principle of [`plan-apply-gate.md#decision-semantics`](plan-apply-gate.md#decision-semantics), not a runtime block), and a synthetic instruction telling the model the limit was reached and to produce a final answer from what it has.
- The session MUST end after that turn with a status subtype identifying which bound fired (`error_max_turns`, `error_max_budget_usd`, `error_max_wall_clock`).
- A `max_turns`/`max_cost_usd` of "soft" (inject the limit message, pause, allow explicit user continuation) MAY be offered as a session-config option; if offered, the default MUST remain hard (session ends) so headless/non-interactive invocations (the future pipeline mode noted in [`architecture.md`](../architecture.md#cli-shape)) have deterministic termination.

### Done detection

The kernel MUST support implicit done detection: a model response containing no `tool_use` blocks ends the turn loop successfully. This is the dominant pattern across surveyed harnesses and requires no cooperation from tool providers, which matters for a microkernel where tool providers are third-party and heterogeneous.

An explicit terminal-tool pattern is more reliable in harnesses that use it, because it lets a tool provider carry a structured completion report. The kernel MUST support this as an opt-in: any tool provider MAY declare a resource with a `terminates_turn: bool` schema annotation; if the model calls such a tool, the kernel treats it as `DoneCheck` success immediately after that call's `post-tool-call` hook, independent of whether other `tool_use` blocks were present in the same message. Implicit no-tool-calls remains the MUST-support baseline; explicit terminal tools are an additive MAY, resolving the tension between LLM providers that don't reliably call a terminal tool and tool providers that want a structured completion signal by keeping implicit detection as the non-negotiable floor and layering explicit termination on top where a tool provider opts in.

### Doom-loop detection

Doom-loop detection — catching a model stuck repeating a functionally identical call — is implemented as a first-class subsystem, orthogonal to the turn counter, across several independently-designed harnesses. The kernel MUST implement this as a kernel-owned subsystem, never delegated to a tool or model provider:

```protobuf
DoomLoopDetector {
  window_size int  // MUST default >= threshold
  threshold   int  // MUST default in [3,5]; MUST be configurable
}

hash_of(call) = hash(tool_name, canonicalize(input_json))
```

At step 16 of every turn, the kernel MUST compare the most recent `threshold` resource/data-source calls' hashes; on `threshold` consecutive identical hashes it MUST treat this as a bound-fired condition and route through the [Limit-reached behavior](#limit-reached-behavior) path (inject a recovery/final-answer turn), not a raw exception. A kernel MAY additionally adopt a second tier — an LLM confidence check after a longer window — as a SHOULD-level enhancement for longer sessions where hash-identity alone under-detects (e.g. semantically repetitive but non-identical calls).
