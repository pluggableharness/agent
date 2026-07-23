# Subagent Spawning

## What it is

Subagent spawning lets a model, mid-turn, launch a nested agent session — its own control loop, its own (usually fresh) context window, its own tool set — to handle a delegated task, and receive back a single distilled result rather than a raw transcript. It turns a coding harness from "one model driving one conversation" into a tree of cooperating sessions: a parent can offload broad codebase exploration, a specialized review pass, or an entire independent multi-step task to a child, without that child's intermediate turns polluting the parent's own context budget.

The capability spans a spectrum of granularity: a single tool call ("spawn a session with this prompt, give me back its final answer") at the simple end; a small lifecycle API with named verbs for spawning, sending follow-up input, waiting, interrupting, resuming, and closing a session at the richer end; and, one tier up, an orchestration layer that coordinates many subagents from a script rather than one at a time.

Architecturally, spawning is an ordinary tool the model calls, not privileged kernel machinery — the harness's executor intercepts the call, spins up a child loop, and feeds the result back as a normal tool result.

## Design considerations

**Isolation and final-result-only return are close to universal.** The child gets a fresh context by default — only the prompt (plus whatever the child's own configuration contributes) crosses the boundary — and only the child's final message ever reaches the parent; intermediate turns are discarded or invisible to the caller. Forking the parent's full conversation history into the child is a real but uncommon alternative.

**Tool-scoping at spawn time is the primary safety lever.** A spawned session isn't a single bounded action — it's a nested control loop that can itself invoke arbitrary further tools without additional per-call confirmation, depending on how tightly its own tool set is scoped. Narrowing what a child can do below what the parent could do (stripping write/shell/network tools from a read-only exploration subagent, denying it the ability to spawn further children) is the mechanism that actually bounds this risk — a documented depth-limit *convention* that isn't structurally enforced is not a safety mechanism; it's a bug waiting to happen.

**Recursion needs a structural, not conventional, guard.** Blocking a child from spawning its own children by excluding the spawn tool from its registry entirely (so the model literally cannot see or call it) is a stronger guarantee than a runtime depth counter the model's own tool calls could route around.

**No peer-to-peer mid-task communication.** The dominant design has no channel for in-flight sibling subagents to message each other directly; where cross-session coordination is needed, it belongs in a distinct mechanism layered above the spawn primitive, not folded into it.

## Permission, sandbox & risk classification

Subagent spawning is classified `kind = resource`, `risk = critical` — a spawned session can itself invoke further resources unattended, so its blast radius is the union of whatever its scoped capability profile allows. See [`reference-catalog.md`](../../specifications/tool/reference-catalog.md) for the full classification, and [`subagents.md`](../../specifications/agent-loop/subagents.md) for the underlying spawn mechanics (context isolation, depth limits, cancellation propagation, and the deliberate exclusion of sibling-to-sibling messaging).

Sandboxing is inherited, not spawn-specific: a child typically runs inside whatever sandbox configuration the parent session already has, so the spawn operation's own safety story is almost entirely about tool-scoping rather than a container boundary around the spawn call itself.
