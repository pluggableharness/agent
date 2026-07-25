# internal/plandecision/drivers/autoallow

> [!WARNING]
> This resolver approves every `ask`-decision plan item **without ever asking a human**. It is a deliberate, tracked, operator-approved deviation from a spec MUST, for the current build stage only. It is not a fallback, not a convenience, and not something to select in anything resembling production.

## Why it exists

[`plan-apply-gate.md#decision-semantics`](../../../../docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics) requires an `ask` decision to emit a `permission-request` state event and block that item's apply until a frontend returns a client decision. No frontend attach path exists anywhere in this codebase yet, so no build at this stage can satisfy that MUST. Rather than leaving `ask` items unhandled — or, worse, quietly treating them as `allow` somewhere inside the plan/apply gate where nobody would see it — the deviation is concentrated here, in one named, acknowledged, loudly self-identifying driver.

The real implementation is a future `drivers/frontend`: it emits the `permission-request` `ServerEvent` and blocks on the matching `ClientEvent.plan_decision` ([`frontend/frontend-protocol.md`](../../../../docs/specifications/frontend/frontend-protocol.md)).

## What it does

`Resolve` returns, for every item, unconditionally:

| Field | Value | Reason |
|---|---|---|
| `Decision` | `PLAN_DECISION_ALLOW` | It never denies — inventing risk heuristics here would be this driver quietly making policy that `internal/policy` already made. |
| `Scope` | `PLAN_DECISION_SCOPE_ONCE` | `SESSION`/`ALWAYS` would create durable state the real frontend resolver would later have to discover and reconcile. Auto-allow leaves zero durable trace. |
| `CorrectedInput` | `nil` | A correction is an operator's substitute arguments. There is no operator here. |
| `DecidedBy` | `UNSAFE-AUTO-ALLOW(no-frontend-attached)` | Verbatim on every item, so an audited session's `plan_items.decided_by` column shows unambiguously, per item, that no human approved it. |

Plus: one `WARN` per resolution (with `session_id`, `plan_item_id`, `provider`, `operation_name`, `risk`), one `WARN` at construction naming the deviation and its replacement, a `plan.decision.resolve` span, and a `pluggableharness.policy.decisions` increment.

An already-cancelled `ctx` returns the cancellation error instead of approving anything.

## How to construct it

```go
r, err := autoallow.New(autoallow.Config{
    AcknowledgeUnsafeAutoAllow: true, // required; New refuses without it
    Logger:                     logger,
    Telemetry:                  prov,
})
```

There is deliberately no usable zero value. `New` returns `ErrNotAcknowledged` unless `AcknowledgeUnsafeAutoAllow` is explicitly true, so the opt-in is visible in code, at the call site, in every diff and review. Selecting the driver through the selector (`drivers.New("auto-allow-unsafe", …)`) is *not* itself the acknowledgement — the Config field is still required.

## Before you change anything here

Read [`CLAUDE.md`](CLAUDE.md). It restates each of the six behavioral requirements with the reasoning behind it, so a future editor tempted to simplify this resolver knows exactly what they would be breaking and why it was built this way on purpose.
