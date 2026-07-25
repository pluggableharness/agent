# internal/session

The kernel's session driver: the outer loop wrapped around [`internal/turn`](../turn), implementing steps 16-18 of [`turn-algorithm.md#the-runturn-algorithm`](../../docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm) plus everything that happens once per session rather than once per turn.

One `Runner.Run` call is one whole session — one `RunSession` invocation in the specification's vocabulary ([`agent-loop/README.md#scope-and-definitions`](../../docs/specifications/agent-loop/README.md#scope-and-definitions)).

## What Run does

1. **Resolves the agent profile** ([`configuration/agent-profiles.md`](../../docs/specifications/configuration/agent-profiles.md)) — an empty `Spec.Profile` means `"default"`, and an absent `agent_profile "default"` block falls back to `BuiltinDefaultProfile`.
2. **Expands tool scoping** via `agentprofile.ResolveTools` against the loaded providers' advertised operations, then resolves each surviving entry to a live `providercatalog.ToolHandle`.
3. **Routes the model chain** via `agentprofile.SelectModel`, once per session, and builds the `ModelTarget` every context provider budgets against.
4. **Creates the session** in the state backend with `status = running`, wraps it in a `sessionstate.Live`, and registers it in the process-wide live-session table so kernel callbacks can resolve it.
5. **Takes one session-lifetime callback grant per resolved plugin** in `sessionscope.Registry`, released on every exit path.
6. **Dispatches `session-start`**, runs the turn loop, **dispatches `session-end`**, and persists the terminal status.

## The turn loop and its five exits

| Exit | Status | Extra turn? |
|---|---|---|
| `turn.Result.Done` | `COMPLETED` | no |
| `max_turns` / `max_cost_usd` / `max_wall_clock_s` fired ([`#independent-bound-dimensions`](../../docs/specifications/agent-loop/turn-algorithm.md#independent-bound-dimensions)) | `ERROR_MAX_TURNS` / `ERROR_MAX_BUDGET_USD` / `ERROR_MAX_WALL_CLOCK` | yes — one final-answer turn |
| doom loop tripped ([`#doom-loop-detection`](../../docs/specifications/agent-loop/turn-algorithm.md#doom-loop-detection)) | `COMPLETED`, `Result.FinalAnswerReason == "doom_loop"` | yes |
| circuit breaker tripped ([`plan-apply-gate.md#circuit-breaker-on-repeated-denials`](../../docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials)) | `COMPLETED`, `Result.FinalAnswerReason == "circuit_breaker"` | yes |
| caller context canceled | `CANCELLED` | no |
| turn driver failed | `FAILED` | no |

The three graceful exits all route through one path — exactly one more turn with `FinalAnswer` set and a reason naming what fired, per [`#limit-reached-behavior`](../../docs/specifications/agent-loop/turn-algorithm.md#limit-reached-behavior). No soft limit mode is offered, so the spec's "if offered, the default MUST remain hard" holds vacuously.

## Collaborators

`Config` names them all. Two are narrow interfaces declared here rather than concrete types, so the whole loop is testable against hand-written fakes:

- `TurnDriver` — one method, `RunTurn`. `*turn.Driver` satisfies it structurally with zero adapter code.
- `HookDispatcher` — one method, `Dispatch`. `*hookdispatch.Dispatcher` satisfies it as written.

Everything else is concrete and shared process-wide: `*statebackend.Store`, `*sessionstate.Table`, `*sessionscope.Registry`, `*eventbus.Bus`, `providercatalog.Catalog`.

`internal/kernel` (the composition root) is what wires them together; this package is the last one below it.
