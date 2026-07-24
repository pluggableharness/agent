# Agent loop — conformance

## Required vs. optional — summary matrix

| Behavior | Level | Where |
|---|---|---|
| Turn algorithm executes steps in the documented order | MUST | [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) |
| `max_turns` bound, configurable | MUST | [`turn-algorithm.md`](turn-algorithm.md#independent-bound-dimensions) |
| `max_cost_usd` bound, configurable | SHOULD | [`turn-algorithm.md`](turn-algorithm.md#independent-bound-dimensions) |
| `max_wall_clock_s` bound, configurable | SHOULD | [`turn-algorithm.md`](turn-algorithm.md#independent-bound-dimensions) |
| Weighted-token cost model (accounting refinement, not a fourth dimension) | MAY | [`turn-algorithm.md`](turn-algorithm.md#independent-bound-dimensions) |
| `cost_usd` computed via provider-declared pricing, accumulated per session | MUST | [`turn-algorithm.md`](turn-algorithm.md#cost-accounting) |
| Cost rolls up the session tree (inherited, only-shrinking budget) | MUST | [`turn-algorithm.md`](turn-algorithm.md#cost-accounting) |
| Graceful "final answer" turn on any bound firing, not a hard error | MUST | [`turn-algorithm.md`](turn-algorithm.md#limit-reached-behavior) |
| Soft-limit mode with user continuation | MAY | [`turn-algorithm.md`](turn-algorithm.md#limit-reached-behavior) |
| Implicit no-tool-calls done detection | MUST | [`turn-algorithm.md`](turn-algorithm.md#done-detection) |
| Opt-in explicit terminal-tool done detection | MAY (per tool provider) | [`turn-algorithm.md`](turn-algorithm.md#done-detection) |
| Kernel-owned doom-loop hash detector, threshold 3–5 | MUST | [`turn-algorithm.md`](turn-algorithm.md#doom-loop-detection) |
| Secondary LLM-oracle doom-loop check on long sessions | SHOULD | [`turn-algorithm.md`](turn-algorithm.md#doom-loop-detection) |
| `data_source` calls execute concurrently within a turn (default, no declaration needed) | MUST | [`turn-algorithm.md`](turn-algorithm.md#turn-level-tool-call-concurrency) |
| `resource` calls execute sequentially by default (undeclared `ConcurrencySpec`) | MUST | [`turn-algorithm.md`](turn-algorithm.md#turn-level-tool-call-concurrency) |
| Concurrency governed by `ConcurrencySpec`, not `kind` directly | MUST | [`turn-algorithm.md`](turn-algorithm.md#turn-level-tool-call-concurrency) |
| Ordered, declaration-order hook dispatch across all subscriber modes | MUST | [`hook-dispatch.md`](hook-dispatch.md#dispatch-order-and-payload-flow) |
| `observe` errors/timeouts never alter payload or abort chain | MUST | [`hook-dispatch.md`](hook-dispatch.md#subscriber-error-handling) |
| `transform` errors/timeouts abort chain + surface `hook_error` | MUST | [`hook-dispatch.md`](hook-dispatch.md#subscriber-error-handling) |
| `veto` errors/timeouts fail-closed to `deny` | MUST | [`hook-dispatch.md`](hook-dispatch.md#timeout-behavior) |
| Per-subscriber timeout enforcement | MUST | [`hook-dispatch.md`](hook-dispatch.md#timeout-behavior) |
| Sequential dispatch for `transform`/`veto` within one hook point | MUST | [`hook-dispatch.md`](hook-dispatch.md#parallelism-within-one-hook-point) |
| Concurrent dispatch among consecutive `observe` subscribers | MAY | [`hook-dispatch.md`](hook-dispatch.md#parallelism-within-one-hook-point) |
| Per-`PlanItem` (not per-plan) policy evaluation | MUST | [`plan-apply-gate.md`](plan-apply-gate.md#plan-construction-and-policy-evaluation) |
| Batched UI presentation of multiple `ask` items | MAY | [`plan-apply-gate.md`](plan-apply-gate.md#plan-construction-and-policy-evaluation) |
| `deny` synthesizes a `tool_result` denial block back to the model | MUST | [`plan-apply-gate.md`](plan-apply-gate.md#decision-semantics) |
| Plan mode via tool-schema removal, not runtime interception | MUST | [`plan-apply-gate.md`](plan-apply-gate.md#decision-semantics) |
| Circuit breaker on repeated `deny` decisions | SHOULD | [`plan-apply-gate.md`](plan-apply-gate.md#circuit-breaker-on-repeated-denials) |
| `data_source`/`interactive` policy precheck (allow/deny only, ask downgrades to deny) | MUST | [`plan-apply-gate.md`](plan-apply-gate.md#data-source-and-interactive-calls) |
| `interactive` calls execute sequentially, never concurrently with each other | MUST | [`plan-apply-gate.md`](plan-apply-gate.md#data-source-and-interactive-calls) |
| Fresh context per child `RunSession` (no history fork) | MUST (default) | [`subagents.md`](subagents.md#context-isolation-default-fresh) |
| Opt-in parent-history forking per profile | MAY | [`subagents.md`](subagents.md#context-isolation-default-fresh) |
| Configurable `max_concurrent_subagents` cap | MUST | [`subagents.md`](subagents.md#concurrency-limits) |
| Parent turn joins on all spawned children before proceeding | MUST | [`subagents.md`](subagents.md#concurrency-limits) |
| Child lookup via a `session_meta.parent_session_id` scan (no separate index) | MUST | [`subagents.md`](subagents.md#session-hierarchy-bookkeeping) |
| Structural (schema-level) depth-cap enforcement | MUST | [`subagents.md`](subagents.md#depth-limits) |
| Default-deny recursion (child spawning further children) | MUST (default) | [`subagents.md`](subagents.md#tool-scoping-at-spawn) |
| Cascading cancellation to in-flight children | MUST | [`subagents.md`](subagents.md#cancellation-propagation) |
| Sibling-to-sibling communication channel | MUST NOT | [`subagents.md`](subagents.md#inter-session-communication) |
| Exponential backoff + jitter, `retry_after` honored | MUST | [`error-recovery.md`](error-recovery.md#model-provider-errors) |
| Separate per-attempt vs. per-session retry caps | MUST | [`error-recovery.md`](error-recovery.md#model-provider-errors) |
| No retry on `context_length_exceeded`, `auth_error`, `invalid_request` | MUST | [`error-recovery.md`](error-recovery.md#model-provider-errors) |
| Tool-provider crash surfaced as tool-result error, not session fault | SHOULD | [`error-recovery.md`](error-recovery.md#tool-provider-plugin-crashes) |

## Open questions

- **Veto-hook timeout fail-closed default** ([`hook-dispatch.md`](hook-dispatch.md#timeout-behavior)). Chosen over a fail-open-with-explicit-instructions alternative because policy sits at the terminal mutation gate. Worth revisiting if fail-closed proves too disruptive to interactive UX in practice — there is no clear consensus among comparable systems here, only one adjacent precedent this design deliberately diverges from.
- **Whether third-party plugins may register `veto`-mode hooks at all**, or whether `veto` is policy-exclusive ([`hook-dispatch.md`](hook-dispatch.md#open-questions)). Carried forward from [`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category) — this is a plugin trust-model question that cross-harness comparison doesn't settle, and this document's hook-dispatch mechanics apply equally either way once that's decided.
- **Tool-provider crash handling** ([`error-recovery.md`](error-recovery.md#tool-provider-plugin-crashes)) is reasoned by analogy to the denial-feedback pattern, not a pattern directly established for crashes specifically. Should be revisited if tool-result formatting and feedback conventions evolve to address crash handling directly.
- **Full context-compaction algorithm** (trigger metric, mechanism, tail protection) is deliberately out of scope across this whole directory — "what's worth remembering" and context injection belong to memory/context providers ([`memory/README.md`](../memory/README.md), [`context/README.md`](../context/README.md)), not the kernel loop. This directory's only compaction-adjacent obligation is the reaction to a `context_length_exceeded` error ([`error-recovery.md#model-provider-errors`](error-recovery.md#model-provider-errors)).

Plan-editing granularity at the frontend is **not** an open question here: it's resolved by [`frontend/README.md`](../frontend/README.md)'s `ClientEvent.plan_decision.corrected_input` field, a corrected-input redirect re-validated against the tool's declared input schema before being honored — see [`plan-apply-gate.md#plan-construction-and-policy-evaluation`](plan-apply-gate.md#plan-construction-and-policy-evaluation).
