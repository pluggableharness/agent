# Plan/apply gate

The kernel's mechanism for approving mutating tool calls before they execute — the harness's analog of Terraform's `plan`/`apply` split, applied to LLM-issued tool calls rather than an author-written resource graph.

## Plan construction and policy evaluation

Resource calls identified at step 8 of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) are collected into a `Plan` and evaluated individually against policy — a policy object per gated call, not a mode flag, and the near-universal three-tier decision model (`allow`/`ask`/`deny`) present in essentially every surveyed open-source harness. Both a `tool.v1` provider (a `ToolCall`) and a `slashcommand.v1` provider (a `SlashCommandCall`) feed the same `Plan`; `PlanItem.producer_category` distinguishes which category produced a given item:

```protobuf
PlanItem {
  id, call_id, provider, operation_name
  input               // parsed JSON args (kernel's canonical ToolCall representation, tool/data-types.md)
  decision            enum { pending, allow, ask, deny }
  decided_by          string   // subscriber/policy-rule name that produced the decision

  // Snapshot fields — see "Snapshot rationale" below.
  kind                tool.v1.ToolKind
  risk                tool.v1.RiskClass
  description         string
  preview             render.v1.RenderTree?   // optional; see "Preview flow" below

  producer_category   common.v1.Category   // CATEGORY_TOOL or CATEGORY_SLASHCOMMAND — which
                                            // producer built this item
}

Plan {
  turn_id
  items []PlanItem
}
```

Policy evaluation happens as the `plan-ready` hook's `veto` chain ([`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category) — "policy is the kernel-privileged veto-mode subscriber at the plan-ready hook, always run, always respected"). The kernel MUST evaluate policy rules per `PlanItem`, not once for the whole plan — a plan with three resource calls against three different providers can and MUST receive three independently evaluated decisions. Presentation MAY batch multiple `ask`-decision items from the same plan into a single combined approval UI interaction (matching the "shown as a diff for approval" framing, and Terraform's own plan-diff precedent), but the decision unit underneath MUST remain per-item so a human can approve some resource calls in a plan and reject others without rejecting the whole turn — this also enables a corrected-input redirect (the model supplies corrected arguments rather than a binary accept/reject) as a frontend feature without a kernel data-model change; see [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `plan_decision.corrected_input`.

### Snapshot rationale

`kind`, `risk`, `description`, and `preview` are captured from the originating operation's `ToolSchema` or `SlashCommandSpec` (per `producer_category`) — and, for `preview`, from a live `Preview` call — see below — at **plan-construction time**, not looked up live whenever a plan is later displayed or audited. This matters because neither schema type is immutable across a provider's lifetime: an operator can edit `agent.hcl`, a provider can ship a new version reclassifying an operation's risk, or a description can be reworded — none of which may retroactively alter what a *historical* plan's audit record says happened. `state-backend.md`'s `plan_items` table persists a `plan-ready`-time snapshot precisely so "what risk was this call classified at when it ran" stays answerable after the classification itself has since changed. A frontend rendering a live, in-progress plan and a CLI displaying a plan from six months ago both read the same snapshot fields — neither re-resolves against the provider's current capability advertisement (`GetSchema` for a `tool.v1` provider, `GetCapabilities` for a `slashcommand.v1` provider).

### Preview flow

`preview` is populated by the kernel calling the originating provider's `Preview` RPC — [`tool/protocol.md#preview`](../tool/protocol.md#preview) for a `CATEGORY_TOOL` item, [`slashcommand/protocol.md#preview`](../slashcommand/protocol.md#preview) for a `CATEGORY_SLASHCOMMAND` item — at plan-construction time, for `TOOL_KIND_RESOURCE` items whose provider implements it — a dry-run description of the call's effect (e.g. a diff for a file write, a request summary for an HTTP call), returned as a `render.v1.RenderTree` and stored on the `PlanItem` verbatim, the same type either category's `Preview` RPC response carries. `data_source` and `interactive` items MUST NOT have `preview` populated — `Preview` is a resource-item concept, mirroring how only resource items reach the plan/apply gate's `allow`/`ask`/`deny` decision at all (see [Data source and interactive calls](#data-source-and-interactive-calls) below).

A provider that does not implement `Preview` leaves `preview` absent on every `PlanItem` it produces — this is an ordinary, unexceptional absence, not an error condition; a frontend MUST fall back to rendering the raw `input` field (the call's parsed arguments) in that case, exactly as it would for a plan built before `Preview` existed. The kernel MUST NOT block plan construction on a slow or failing `Preview` call beyond its own ordinary per-RPC deadline (`.claude/rules/grpc.md`'s "Context and deadlines") — a `Preview` timeout or error degrades to an absent `preview` for that item, never to an aborted plan.

## Decision semantics

- `allow` — the kernel proceeds to apply this item without further interaction.
- `ask` — the kernel MUST emit a `permission-request` state event (per [`frontend/README.md`](../frontend/README.md)) and block that item's apply until the frontend returns a client decision. Other `allow`-decision items in the same plan MAY proceed without waiting, unless the tool provider's declared concurrency-safety ([`turn-algorithm.md#turn-level-tool-call-concurrency`](turn-algorithm.md#turn-level-tool-call-concurrency)) says otherwise.
- `deny` — the kernel MUST NOT execute that item. Denial surfaces as tool-result text, not a separate out-of-band channel — a pattern universal across every open-source harness surveyed. The kernel MUST synthesize a `tool_result` content block carrying the denial reason for that call, so the model observes the denial in its own history and can adapt on the next turn rather than silently having the call vanish.

Plan mode (a session or turn restricted to `data_source`-kind calls only) MUST be implemented by removing resource tool specs from the request sent to the model at step 2 of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) (`pre-model-call`), not by intercepting calls at runtime after the model has already attempted one — removing a tool from the schema entirely is the cleanest implementation available: the model literally cannot attempt the call, so there is no denial to feed back and no wasted turn.

## Circuit breaker on repeated denials

An automated denial engine can otherwise fall into a denial-storm feedback loop, where the model keeps re-attempting a functionally identical mutating call after each `deny`. The kernel SHOULD implement a circuit breaker against this: N consecutive `deny` decisions (or M denials within a sliding window) within one session SHOULD trip the same graceful- degradation path as a bound ([`turn-algorithm.md#limit-reached-behavior`](turn-algorithm.md#limit-reached-behavior)) rather than allowing the model to loop indefinitely against a wall of denials. This is a SHOULD, not a MUST — only a single surveyed harness demonstrates this pattern strongly, rather than showing full convergence across independent implementations.

## Data source and interactive calls

`data_source` calls are not policy-exempt, and neither are `interactive` calls. Before step 9/9b of [`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) executes them, each `data_source` and `interactive` call MUST be checked against policy — the same rule set the section above evaluates for resources — but with a narrower outcome space, since neither kind has an apply step to gate: `ask` is not a meaningful decision for either. This `kind`-based precheck applies identically regardless of `PlanItem.producer_category` — whether the originating `kind` came from a `tool.v1` `ToolSchema` or a `slashcommand.v1` `SlashCommandSpec`, `producer_category` only affects which provider's RPC the kernel calls, never which policy path a `data_source`/`interactive` item takes.

- **`allow`** (including the case of no matching rule, the default for reads): the call proceeds unchanged — `data_source` calls to `execute_concurrently`, `interactive` calls to `execute_sequentially`.
- **`deny`**: the call MUST NOT execute. The kernel MUST synthesize a `tool_result` denial block for it, identical in spirit to the resource-denial handling above, so the model observes the denial in its own history and can adapt on the next turn.
- A policy rule that resolves to `ask` against a `data_source` or `interactive` call is downgraded to `deny` by the policy engine itself — by the time this precheck runs, the decision space is already just `allow`/`deny`.

This means `data_source` and `interactive` calls still execute *by default*, but can now be explicitly blocked by policy — an operator can write a rule denying a specific read operation, or denying `kind = interactive` outright to make a profile safe to run headless (a future non-interactive pipeline invocation has no human to answer an `ask_user`-shaped call; see [`architecture.md`](../architecture.md#cli-shape)). See [`configuration/policy-dsl.md`](../configuration/policy-dsl.md) for the full rule-matching DSL and evaluation semantics.

What differs between `data_source` and `interactive` is scheduling, not policy: `interactive_calls` are policy-prechecked exactly like `data_source_calls`, but executed **sequentially** (`execute_sequentially`, never `execute_concurrently`) — asking a human two things at once in one frontend is inherently confusing, and this holds regardless of any declared `ConcurrencySpec`, which MUST NOT even be declared for an `interactive` operation. Execution itself (once `allow`ed) surfaces as [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `interactive_request`/`interactive_response` `ServerEvent`/`ClientEvent` pair, rendered in the same visual treatment already established for `ask`-decision prompts.

The precheck's evaluation result MUST distinguish an outright `ask`-turned-`deny` downgrade from a plain `deny` decision — a caller needs to be able to log that a winning `ask` decision was flipped to `deny` for a `data_source`/`interactive` call, rather than have that transition happen silently.

The `interactive`-kind precheck reuses the `data_source` precheck's defaulting and downgrade rules verbatim rather than the policy DSL gaining a third match kind of its own: [`configuration/policy-dsl.md`](../configuration/policy-dsl.md)'s match schema stays two-valued (`resource`/`data_source`), and an `interactive` call routes through the same non-interactive precheck path a `data_source` call uses.

## PlanDecisionScope semantics

[`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `ClientEvent.PlanDecision` carries a `scope` field (`PlanDecisionScope`: `ONCE`/`SESSION`/`ALWAYS`) alongside `decision` and `corrected_input`. `scope` governs how durably the resolved decision applies beyond the one `PlanItem` it names — it is evaluated by the plan/apply gate at the same point `decision` and `corrected_input` are, immediately on receipt of a `plan_decision` client event, not deferred to any later stage:

- **`ONCE`** (the default a frontend SHOULD send absent explicit operator intent): applies to the named `PlanItem` only. No durable record beyond the ordinary `plan_items` audit row this decision produces regardless of scope.
- **`SESSION`**: the kernel MUST remember this verdict for the rest of the current session, in memory — not written to `agent.hcl` or any persisted policy store — and apply it automatically to any future plan item in the same session matching the same `(provider, operation_name)` pair, without re-emitting a `permission_request`/blocking on a fresh `plan_decision`. A `SESSION`-scoped `deny` suppresses future `ask`/`allow` items the same way; a `SESSION`-scoped `allow` (with or without `corrected_input`) auto-applies the decision (re-validating `corrected_input` against the current call's own arguments each time, per [`frontend/frontend-protocol.md#plan_decisioncorrected_input`](../frontend/frontend-protocol.md#plan_decisioncorrected_input) — a `SESSION` scope remembers the *verdict*, not a frozen copy of the corrected arguments). This rule lapses at session end; it does not survive a `ResumeSession` re-open ([`frontend/frontend-protocol.md#resume-and-re-open-semantics`](../frontend/frontend-protocol.md#resume-and-re-open-semantics)) into a fresh session of rules.
- **`ALWAYS`**: the kernel MUST persist this verdict as policy — surviving beyond the current session, applying to future sessions under the same profile — via the same policy-rule mechanism [`configuration/policy-dsl.md`](../configuration/policy-dsl.md) already governs for operator-authored rules. This requires kernel-side policy persistence: a kernel build that cannot durably write a new policy rule (e.g. no writable policy store configured) MUST reject an `ALWAYS`-scoped `plan_decision` with a distinct error rather than silently downgrading it to `SESSION` or `ONCE` — a frontend and its operator need to know an "always allow this" request didn't actually stick, not discover it the next time the same prompt reappears. An `ALWAYS`-scoped decision, once persisted, takes effect starting with the *next* plan-ready evaluation it would match — it does not retroactively alter the plan item that triggered it, which has already been decided via the ordinary `decision`/`corrected_input` fields.

`SESSION` and `ALWAYS` are both strictly broader than what the underlying per-item decision unit ([Plan construction and policy evaluation](#plan-construction-and-policy-evaluation) above) requires — they are a frontend/operator convenience layered on top of, not a replacement for, per-item evaluation: policy still runs and still produces an independent decision for every item in every future plan, it's simply that a `SESSION`/`ALWAYS` rule can now be one of the things that decision is based on.
