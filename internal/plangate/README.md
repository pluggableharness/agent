# internal/plangate

The kernel's plan/apply gate — the mechanism that decides whether an LLM-issued tool call is allowed to run, and records what was decided. It implements [`docs/specifications/agent-loop/plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md).

One `Gate` is scoped to one session. That is not incidental: `PLAN_DECISION_SCOPE_SESSION` verdicts live in memory on the `Gate` itself, so a new `Gate` per session is the whole of this build's SESSION-scope expiry policy.

## The four entry points

| Method | What it does |
|---|---|
| `Build` | Turns a turn's provisional plan items into a `Plan`, calling each resource item's provider `Preview` RPC to populate the plan diff a frontend renders. |
| `Precheck` | Evaluates `data_source` and `interactive` calls against the same policy rule set, in the narrowed allow/deny outcome space those kinds get. |
| `Decide` | Runs per-item policy, the plan-ready veto chain, and ask resolution, then persists one plan event plus one terminal `plan_items` row per item. |
| `Result` | Assembles the turn's `ApplyResult` from the caller's apply outcomes plus the denied items, and persists it as one apply event. |

`DenialBlocks` is the fifth, smaller piece: it synthesizes the `tool_result` blocks a denial travels on. The spec is emphatic that denial surfaces as tool-result text and never on a separate out-of-band channel — the model has to see the denial in its own history to adapt on the next turn.

## Order of operations inside `Decide`

1. **Policy, per item.** Never once for the whole plan. Three resource calls against three providers get three independently evaluated decisions.
2. **The plan-ready hook chain, exactly once.** Any `HOOK_DECISION_DENY` denies the whole plan and sets `Decisions.VetoedBy`, overriding every per-item decision including allows. Chain ordering — the kernel-privileged policy veto pinned ahead of every plugin subscriber — is the dispatcher's guarantee; this package does not re-derive it.
3. **Ask resolution.** A remembered SESSION-scope verdict first (which suppresses the resolver round trip entirely), otherwise the plan-decision resolver, with any `corrected_input` re-validated against the operation's declared input schema.
4. **Persistence.** One plan event and every `plan_items` row in a single `AppendPlan` transaction, with every decision terminal.

Asks are resolved *before* anything is persisted, deliberately: `plan_items` has no representation for a decision that is still pending, so an ask resolved mid-turn would have nowhere to go if its row had already been written.

## `decided_by`

Every persisted row carries one of five forms:

```
policy:<rule-name>                 a policy rule decided outright
policy:default                     no rule matched; the kind default applied
policy:<rule>+resolver:<name>      an ask escalated to the plan-decision resolver
policy:<rule>+session:<name>       an ask satisfied by a remembered SESSION-scope verdict
hook-veto:<provider>               a plan-ready veto denied the whole plan
```

## Where it sits

```
internal/policy          pure rule evaluation ────┐
internal/plandecision    the ask-resolution seam ─┤
internal/schemavalidate  corrected_input checks ──┼──> internal/plangate ──> internal/statebackend
internal/circuitbreaker  denial-storm detection ──┤         │
internal/providercatalog Preview handles ─────────┘         │
                                                            v
                                            HookDispatcher / ApplyOutcome
                                            (interfaces declared HERE, satisfied by
                                             a future caller that owns both sides)
```

The gate composes pure-domain packages and adds the I/O and the ordering. It does **not** import `internal/hookdispatch` or `internal/tooldispatch` — see [`CLAUDE.md`](CLAUDE.md) for why that decoupling is load-bearing rather than a historical accident.
