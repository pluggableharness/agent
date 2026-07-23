# Plan/apply gate

The kernel's mechanism for approving mutating tool calls before they execute — the harness's analog of Terraform's `plan`/`apply` split, applied to LLM-issued tool calls rather than an author-written resource graph.

## Plan construction and policy evaluation

Resource calls identified at step 8 of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) are collected into a `Plan` and evaluated individually against policy — a policy object per tool call, not a mode flag, and the near-universal three-tier decision model (`allow`/`ask`/`deny`) present in essentially every surveyed open-source harness:

```protobuf
PlanItem {
  id, tool_call_id, provider, tool_name
  input       // parsed JSON args (kernel's canonical ToolCall representation, tool/data-types.md)
  decision    enum { pending, allow, ask, deny }
  decided_by  string   // subscriber/policy-rule name that produced the decision
}

Plan {
  turn_id
  items []PlanItem
}
```

Policy evaluation happens as the `plan-ready` hook's `veto` chain ([`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category) — "policy is the kernel-privileged veto-mode subscriber at the plan-ready hook, always run, always respected"). The kernel MUST evaluate policy rules per `PlanItem`, not once for the whole plan — a plan with three resource calls against three different tool providers can and MUST receive three independently evaluated decisions. Presentation MAY batch multiple `ask`-decision items from the same plan into a single combined approval UI interaction (matching the "shown as a diff for approval" framing, and Terraform's own plan-diff precedent), but the decision unit underneath MUST remain per-item so a human can approve some resource calls in a plan and reject others without rejecting the whole turn — this also enables a corrected-input redirect (the model supplies corrected arguments rather than a binary accept/reject) as a frontend feature without a kernel data-model change; see [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `plan_decision.corrected_input`.

## Decision semantics

- `allow` — the kernel proceeds to apply this item without further interaction.
- `ask` — the kernel MUST emit a `permission-request` state event (per [`frontend/README.md`](../frontend/README.md)) and block that item's apply until the frontend returns a client decision. Other `allow`-decision items in the same plan MAY proceed without waiting, unless the tool provider's declared concurrency-safety ([`turn-algorithm.md#turn-level-tool-call-concurrency`](turn-algorithm.md#turn-level-tool-call-concurrency)) says otherwise.
- `deny` — the kernel MUST NOT execute that item. Denial surfaces as tool-result text, not a separate out-of-band channel — a pattern universal across every open-source harness surveyed. The kernel MUST synthesize a `tool_result` content block carrying the denial reason for that call, so the model observes the denial in its own history and can adapt on the next turn rather than silently having the call vanish.

Plan mode (a session or turn restricted to `data_source`-kind calls only) MUST be implemented by removing resource tool specs from the request sent to the model at step 2 of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) (`pre-model-call`), not by intercepting calls at runtime after the model has already attempted one — removing a tool from the schema entirely is the cleanest implementation available: the model literally cannot attempt the call, so there is no denial to feed back and no wasted turn.

## Circuit breaker on repeated denials

An automated denial engine can otherwise fall into a denial-storm feedback loop, where the model keeps re-attempting a functionally identical mutating call after each `deny`. The kernel SHOULD implement a circuit breaker against this: N consecutive `deny` decisions (or M denials within a sliding window) within one session SHOULD trip the same graceful- degradation path as a bound ([`turn-algorithm.md#limit-reached-behavior`](turn-algorithm.md#limit-reached-behavior)) rather than allowing the model to loop indefinitely against a wall of denials. This is a SHOULD, not a MUST — only a single surveyed harness demonstrates this pattern strongly, rather than showing full convergence across independent implementations.

## Data source and interactive calls

`data_source` calls are not policy-exempt, and neither are `interactive` calls. Before step 9/9b of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) executes them, each `data_source` and `interactive` call MUST be checked against policy — the same rule set the section above evaluates for resources — but with a narrower outcome space, since neither kind has an apply step to gate: `ask` is not a meaningful decision for either.

- **`allow`** (including the case of no matching rule, the default for reads): the call proceeds unchanged — `data_source` calls to `execute_concurrently`, `interactive` calls to `execute_sequentially`.
- **`deny`**: the call MUST NOT execute. The kernel MUST synthesize a `tool_result` denial block for it, identical in spirit to the resource-denial handling above, so the model observes the denial in its own history and can adapt on the next turn.
- A policy rule that resolves to `ask` against a `data_source` or `interactive` call is downgraded to `deny` by the policy engine itself — by the time this precheck runs, the decision space is already just `allow`/`deny`.

This means `data_source` and `interactive` calls still execute *by default*, but can now be explicitly blocked by policy — an operator can write a rule denying a specific read operation, or denying `kind = interactive` outright to make a profile safe to run headless (a future non-interactive pipeline invocation has no human to answer an `ask_user`-shaped call; see [`architecture.md`](../architecture.md#cli-shape)). See [`configuration/policy-dsl.md`](../configuration/policy-dsl.md) for the full rule-matching DSL and evaluation semantics.

What differs between `data_source` and `interactive` is scheduling, not policy: `interactive_calls` are policy-prechecked exactly like `data_source_calls`, but executed **sequentially** (`execute_sequentially`, never `execute_concurrently`) — asking a human two things at once in one frontend is inherently confusing, and this holds regardless of any declared `ConcurrencySpec`, which MUST NOT even be declared for an `interactive` operation. Execution itself (once `allow`ed) surfaces as [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `interactive_request`/`interactive_response` `ServerEvent`/`ClientEvent` pair, rendered in the same visual treatment already established for `ask`-decision prompts.

The precheck's evaluation result MUST distinguish an outright `ask`-turned-`deny` downgrade from a plain `deny` decision — a caller needs to be able to log that a winning `ask` decision was flipped to `deny` for a `data_source`/`interactive` call, rather than have that transition happen silently.

The `interactive`-kind precheck reuses the `data_source` precheck's defaulting and downgrade rules verbatim rather than the policy DSL gaining a third match kind of its own: [`configuration/policy-dsl.md`](../configuration/policy-dsl.md)'s match schema stays two-valued (`resource`/`data_source`), and an `interactive` call routes through the same non-interactive precheck path a `data_source` call uses.
