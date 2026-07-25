# internal/turn

The kernel's `RunTurn` driver: steps 1 through 15 of the numbered algorithm in [`docs/specifications/agent-loop/turn-algorithm.md`](../../docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm).

## What this package is

Pure orchestration. It owns no algorithm of its own — every step delegates to a collaborator that already exists as its own tested package. What this package contributes is three things the collaborators cannot contribute individually:

1. **The documented order.** `conformance.md`'s first MUST is "turn algorithm executes steps in the documented order." `execute` in `runturn.go` *is* that order, and `conformance_test.go` asserts it against a shared call log every fake appends to.
2. **Declaration-order bookkeeping.** Tool calls are split by kind, prechecked or planned along different paths, and executed concurrently — but every `tool_result` block must pair with the `tool_use` block it answers, in the order the model emitted them. The `pending` slice is what preserves that ordering across all of it.
3. **The adapters.** `internal/plangate` deliberately declares its own `HookDispatcher` and `ApplyOutcome` rather than importing `internal/hookdispatch` and `internal/tooldispatch`. This package owns all three, so the bridges live here.

## The steps and who executes them

| Step | Spec | Collaborator |
|---|---|---|
| 1 — context-assemble | [`context/protocol.md`](../../docs/specifications/context/protocol.md#contribute-the-context-assemble-rpc) | `internal/contextassembly` (persists its own `context_contribution` events) |
| 2 — pre-model-call, request build | [`hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md) | `internal/hookdispatch`, then `internal/modelrequest` |
| 3-4 — StreamCompletion, accumulate | [`model/protocol.md`](../../docs/specifications/model/protocol.md) | `internal/modelcall` (persists the message + cost ledger row) |
| 5 — post-model-response | [`hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md) | `internal/hookdispatch` |
| 6 — DoneCheck | [`turn-algorithm.md`](../../docs/specifications/agent-loop/turn-algorithm.md#done-detection) | this package (no `tool_use` blocks ⇒ done) |
| 7 — pre-tool-call | [`hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md) | `internal/hookdispatch`, over a provisional `PlanItem` minted here |
| 8 — split_by_kind | [`plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md) | this package (pure Go) |
| 9 / 9b — data_source / interactive | [`plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls) | `internal/plangate` precheck, then `internal/tooldispatch` |
| 10-12 — build, decide, apply | [`plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md) | `internal/plangate`, then `internal/tooldispatch` |
| 13 — post-tool-call | [`hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md) | `internal/hookdispatch` |
| 14 — post-apply | [`plan-apply-gate.md`](../../docs/specifications/agent-loop/plan-apply-gate.md) | `internal/plangate`, then `internal/hookdispatch` |
| 15 — history append | [`turn-algorithm.md`](../../docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm) | this package (pure Go) |

Steps 16 (doom-loop), 17 (bounds), and 18 (the loop) are **not** here, and neither are `session-start`/`session-end`. They belong to the session driver that calls `RunTurn` repeatedly. `Result` hands that driver everything it needs to run them: `CallHashes` for the doom-loop window, `CostUSD`/`Usage` for the budget, `TrippedProviders` for the circuit-breaker path, and `Done`/`DoneReason` for the loop's own exit.

## Wiring

```go
hooks := hookdispatch.New(registry, session, telem, logger, hookdispatch.Options{})

gate := plangate.New(plangate.Config{
    // GateHooks bridges this dispatcher to the HookDispatcher plangate
    // declares for itself — see adapters.go.
    Hooks: turn.GateHooks{Dispatcher: hooks},
    // ... resolver, events, rules, breaker, tools
})

driver, err := turn.New(turn.Config{
    Hooks:     hooks,
    Context:   contextassembly.New(contextassembly.Config{ /* ... */ }),
    Model:     modelcall.New(modelcall.Config{ /* ... */ }),
    Gate:      gate,
    Tools:     tooldispatch.New(tooldispatch.Config{ /* ... */ }),
    Catalog:   catalog,
    Telemetry: telem,
    Logger:    logger,
})
```

Every collaborator field is an interface declared in this package, narrowed to the methods the turn actually calls. The concrete types above satisfy them as written — `adapters_test.go` holds compile-time anchors proving it.

## Restricted turns

Two request flags withhold tool specs at step 2, which is the only place either is implemented — never a runtime interception of a call the model already attempted:

- **`PlanMode`** removes every `TOOL_KIND_RESOURCE` operation from the request, per [`plan-apply-gate.md#decision-semantics`](../../docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics). The model literally cannot attempt a mutation, so there is no denial to feed back and no wasted turn.
- **`FinalAnswer`** withholds *every* spec and appends a synthetic instruction naming `FinalAnswerReason`, per [`turn-algorithm.md#limit-reached-behavior`](../../docs/specifications/agent-loop/turn-algorithm.md#limit-reached-behavior). The session driver sets it for the one extra turn a fired bound triggers.

## Testing

`conformance_test.go` is the important one: a realistic two-turn scenario through hand-written fakes, asserting the exact recorded call sequence. `runturn_test.go` covers the branches that scenario does not reach — the veto path, both restricted-turn modes, the terminal-tool path, denial handling at each gate, and cancellation. `adapters_test.go` covers the bridges and the compile-time interface anchors.
