# internal/plandecision

The seam through which the kernel turns one `ask`-decision plan item into a terminal `allow`/`deny` verdict.

[`docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics`](../../docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics) defines the contract: an `ask` decision means the kernel **MUST** emit a `permission-request` state event and block that item's apply until a frontend returns a client decision. This package holds the interface that obligation is served through, so the plan/apply gate itself carries no frontend knowledge.

## What this package does

- `plandecision.go` — the `Resolver` interface (one method: `Resolve(ctx, Request) (Decision, error)`), the `Request`/`Decision` types, and the sentinels a caller matches with `errors.Is`.
- `Request` carries the session/turn identifiers, the `plan.v1.PlanItem` awaiting a verdict, and the originating operation's `schema.v1.Schema` — the last so a resolver-returned `CorrectedInput` can be re-validated before it is honored.
- `Decision` carries the terminal `plan.v1.PlanDecision`, the `frontend.v1.PlanDecisionScope` (`ONCE`/`SESSION`/`ALWAYS`), an optional `CorrectedInput`, and `DecidedBy` for the `plan_items` audit row a future caller persists.
- `ValidateDecision` is the one call a caller makes on a verdict handed back by a `Resolver`: it rejects a non-terminal decision (`ErrNonTerminalDecision`) and re-validates a `CorrectedInput` against the declared input schema, per [`frontend/frontend-protocol.md#plan_decisioncorrected_input`](../../docs/specifications/frontend/frontend-protocol.md#plan_decisioncorrected_input)'s "MUST re-validate ... never silently coerced and never silently downgraded to a plain deny".
- `ErrPolicyPersistenceUnavailable` is the distinct, surfaced error a resolver returns rather than silently downgrading an `ALWAYS`-scoped verdict it cannot persist ([`plan-apply-gate.md#plandecisionscope-semantics`](../../docs/specifications/agent-loop/plan-apply-gate.md#plandecisionscope-semantics)).

## Drivers

| Name | Package | Status |
|---|---|---|
| `frontend` | — | **The real implementation.** Emits the `permission-request` `ServerEvent`, blocks on the matching `ClientEvent.plan_decision`. Not built yet — no frontend attach path exists in this codebase. The name is reserved in the selector, deliberately unstubbed. |
| `auto-allow-unsafe` | [`drivers/autoallow`](drivers/autoallow/) | A deliberate, tracked, operator-approved deviation from the MUST above, for the current build stage only. Auto-approves every item without asking a human. Read its `CLAUDE.md` before touching it. |
| *(unregistered)* | [`drivers/fake`](drivers/fake/) | Scripted test double for exercising a plan-gate consumer against every `Decision` shape. Not selectable by name — tests construct it directly. |

The selector ([`drivers/drivers.go`](drivers/drivers.go)) has **no default name**: an empty or unrecognized name is a construction-time error, so nothing can fall back to auto-allow by omission.

## What this package does NOT do

- It does not emit state events, hold frontend streams, or know what a frontend is — that is the future `drivers/frontend`'s job.
- It does not apply a verdict. Persisting the `plan_items` audit row, honoring `SESSION`/`ALWAYS` scope, and synthesizing a `tool_result` denial block are all the plan/apply gate's job.
- It does not log or trace. No `log/slog`, no `internal/telemetry` import — pure domain logic per [`.claude/rules/logging-telemetry.md`](../../.claude/rules/logging-telemetry.md)'s pure-domain exemption. The drivers instrument; the seam does not.

## How it fits in

The plan/apply gate calls a `Resolver` once per `PLAN_DECISION_ASK` item, concurrently with applying that plan's `allow` items where the tool's declared concurrency safety permits. A `Resolver` that hangs stalls the whole turn, which is why every implementation must honor `ctx` cancellation promptly.
