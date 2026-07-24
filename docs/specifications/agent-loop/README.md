# Agent loop

Covers the kernel's own required turn-by-turn control flow — not a plugin protocol. Nothing in this directory is optional plugin surface; every conforming kernel implementation MUST implement this directory's MUST-level behavior regardless of which providers happen to be loaded. The tone throughout is "here is what the kernel does," not "here is what a plugin author implements" — contrast with [`model/`](../model/README.md), [`tool/`](../tool/README.md), [`context/`](../context/README.md), [`memory/`](../memory/README.md), and [`frontend/`](../frontend/README.md), each of which a third-party plugin author actually builds against.

This design reflects patterns observed across roughly 16 agentic coding systems (Claude Code, Codex CLI, Gemini CLI, Aider, Cline, Kilo Code, opencode, Continue, Goose, OpenHands, SWE-agent, Zed, Plandex, Open Interpreter, Cursor, Windsurf/Cascade, Amp). Where a strong convergent pattern holds across independent implementations, this directory adopts it as a MUST. Where there's real divergence with no clear winner, a document here makes an explicit judgment call and says so, or carries the question into [`conformance.md`](conformance.md#open-questions). See [`architecture.md`](../architecture.md) for the surrounding system (provider categories, Emit→Render→Paint, state backend, plan/apply terminology) — this directory only covers the kernel loop algorithm in detail.

## Scope and definitions

- **Turn** — one iteration of context-assemble → model call → tool resolution → apply → hook dispatch, corresponding to exactly one `StreamCompletion` call and its resultant tool executions. See [`turn-algorithm.md`](turn-algorithm.md).
- **Session** — one `RunSession` invocation, spanning `session-start` to `session-end`, comprising one or more turns. Sessions form a tree via `parent_session_id` ([`state-backend.md`](../state-backend.md)); the root session has no parent. See [`subagents.md`](subagents.md).
- **Hook points**, in the order they occur across the loop, per [`architecture.md`](../architecture.md#hook-dispatch-semantics): `session-start`, `context-assemble`, `pre-model-call`, `post-model-response`, `pre-tool-call`, `plan-ready`, `post-tool-call`, `post-apply`, `session-end`. `context-assemble` through `post-apply` repeat every turn; `session-start` and `session-end` fire exactly once per session. See [`hook-dispatch.md`](hook-dispatch.md).

## Reading order

- [`turn-algorithm.md`](turn-algorithm.md) — the numbered `RunTurn` algorithm, turn-level tool-call concurrency, loop termination and bounds (independent bound dimensions, cost accounting, limit-reached behavior, done detection, doom-loop detection).
- [`hook-dispatch.md`](hook-dispatch.md) — dispatch order and payload flow, subscriber error handling, timeout behavior, parallelism within one hook point, and open questions around `veto`-mode registration.
- [`plan-apply-gate.md`](plan-apply-gate.md) — plan construction and policy evaluation, decision semantics, the circuit breaker on repeated denials, and the `data_source`/`interactive` policy precheck.
- [`subagents.md`](subagents.md) — `RunSession`'s data types, context isolation, concurrency limits, session-hierarchy bookkeeping, structural depth limits, tool scoping at spawn, cancellation propagation, and the (deliberate) absence of inter-session communication.
- [`error-recovery.md`](error-recovery.md) — model-provider error handling and tool-provider (plugin) crash handling mid-turn.
- [`conformance.md`](conformance.md) — the MUST/SHOULD/MAY summary matrix and genuinely open questions.
