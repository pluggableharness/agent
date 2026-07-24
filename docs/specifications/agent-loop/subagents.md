# Sub-agent invocation via `RunSession`

[`architecture.md`](../architecture.md#sub-agent-support) defines `RunSession(profile, prompt, scoped_providers) -> result` as a kernel primitive callable from any plugin, not privileged kernel code — "spawn a sub-agent" is an ordinary tool provider whose `Invoke` calls back into it (see [`tool/reference-catalog.md`](../tool/reference-catalog.md) for the reference `spawn_subagent` tool). The following mechanics apply to every such call, regardless of which tool provider issues it.

## Data types

```protobuf
RunSessionRequest {
  profile             string           // named sub-agent profile from agent.hcl
  prompt              string
  parent_session_id   string           // MUST be set to the calling session's id
  remaining_depth      int             // MUST — an inherited, only-shrinking budget,
                                        // never widened by the child (see Depth limits)
  remaining_cost_budget_usd  float64   // MUST — same inherited, only-shrinking shape as
                                        // remaining_depth, computed per
                                        // turn-algorithm.md#cost-accounting
  scoped_providers     []ProviderRef    // resolved from the profile; MAY be narrowed
                                        // further per-call, MUST NOT be widened
}

RunSessionResult {
  session_id
  final_message       // canonical content-block message — the ONLY thing that
                       // crosses the session boundary back to the parent turn
  status               enum { completed, error_max_turns, error_max_budget_usd,
                               error_max_wall_clock, cancelled, failed }
}
```

See [`kernel-callbacks.md`](../kernel-callbacks.md) for `RunSession` as a kernel-callback primitive in general (it's reserved for a future non-interactive pipeline mode too, not just sub-agent spawns).

## Context isolation (default: fresh)

Fresh context per subagent, with only the prompt string crossing the boundary, is the dominant pattern across the surveyed harnesses — a strong, nearly-universal convergence. A lone surveyed outlier forks the parent's full conversation history into the child thread instead.

The kernel MUST default to fresh context: a child session's initial history contains only the `prompt` string (plus whatever its `context-assemble` hook chain independently contributes per its own profile) — the parent's message history, tool state, and in-flight context MUST NOT be implicitly visible to the child. A profile MAY opt into history forking as an explicit, named alternative (e.g. a `fork_parent_history = true` profile setting), but this MUST be opt-in, not the default, given how strongly comparable systems converge against it. Only `final_message` — the child's last message — crosses back into the parent's `tool_result`; intermediate turns are never visible to the parent's model context, though they remain queryable in the state backend via each session's own `session_meta.parent_session_id` row ([Session-hierarchy bookkeeping](#session-hierarchy-bookkeeping)) for replay and audit.

## Concurrency limits

The kernel MUST enforce a configurable cap on concurrently in-flight child sessions per parent (`max_concurrent_subagents`, `agent.hcl`, profile- or session-scoped). When a model turn contains multiple parallel `tool_use` blocks each invoking a spawn-capable tool, the kernel executes the resulting `RunSession` calls concurrently up to that cap, queuing any beyond it — an explicit, configurable ceiling appropriate for a kernel that must not assume any particular runtime's default concurrency behavior.

A parent turn's apply step ([`turn-algorithm.md`](turn-algorithm.md#the-runturn-algorithm) step 12, `apply_approved_items`) for a batch of parallel spawn calls MUST NOT proceed past that turn until every `RunSession` call issued in that turn has returned — this is a join, not a fire-and-forget: the tool results feeding back into the parent's history (step 15) need every child's `final_message` present.

## Session-hierarchy bookkeeping

Parent linkage lives in `session_meta.parent_session_id` ([`../state-backend.md#session_meta`](../state-backend.md#session_meta)), not on every event. There is no separate queryable parent→children index: finding a session's children, or reconstructing a tree, means scanning `session_meta` across the sessions directory ([`../state-backend.md#cross-session-queries`](../state-backend.md#cross-session-queries)) — a first-class, graph-queryable session-spawn structure is the kind of thing that "enables restart, audit, and cost attribution" once a harness has more than a handful of sub-agent spawns to reason about, and this scan-based approach is deliberately what fills that role here. At minimum `session_meta` MUST record, per spawn: parent session id, child session id, the profile name used, and the resulting depth (see [Depth limits](#depth-limits)).

## Depth limits

Convention-only depth limits get violated in practice — an unbounded subagent-recursion bug is a real, documented failure mode when a depth limit is a documented convention rather than an enforced mechanism. Structural enforcement (a compile-time constant, or excluding spawn-capable tools from a child's tool registry entirely) is the only pattern that reliably holds.

The kernel MUST enforce depth structurally, via an inherited, only-shrinking budget rather than a static per-profile value checked in isolation — a purely per-profile check would let a permissive profile spawned deep in an already-deep tree bypass an ancestor's tighter limit. This requires two distinct resolution functions, not one: a root session's remaining depth falls back to a kernel default, while a child's falls back to effectively unbounded absent an explicit override on its own profile:

```text
remaining_depth(root_session) = root_profile.max_depth ?? kernel_default_max_depth
remaining_depth(child) = min(remaining_depth(parent) - 1, child_profile.max_depth ?? +inf)
```

`RunSessionRequest` (see [Data types](#data-types)) carries `remaining_depth`, computed by the kernel at spawn time — a profile's own `max_depth` still acts as a hard ceiling it never exceeds on its own, but an ancestor's tighter budget always wins going down. When resolving `scoped_providers` for a child whose `remaining_depth` would be `<= 0`, the kernel MUST exclude any spawn-capable tool from that child's tool registry entirely — the same "remove from the schema, don't intercept at runtime" principle already established for plan mode ([`plan-apply-gate.md#decision-semantics`](plan-apply-gate.md#decision-semantics)). The model at the depth ceiling literally cannot see a spawn tool to call it; there is no runtime check to bypass.

## Tool scoping at spawn

Scoping child tools to the task's privilege level at spawn time is a convergent pattern across the surveyed harnesses (stripping write/browser/ MCP/spawn tools from read-only subagents, denying task-management tools, excluding agent-spawn tools by construction). `scoped_providers` in a `RunSessionRequest` MUST be resolved from the named profile's declared tool set, not inherited from the parent's full capability set. Recursion (a child spawning its own children) MUST default to disabled per profile unless the profile explicitly opts in — default-deny-with-opt-in, matching the dominant pattern across comparable systems, rather than default-allow-with-a-depth-counter-as-the-only-brake. See [`configuration/agent-profiles.md`](../configuration/agent-profiles.md).

## Cancellation propagation

If a session is cancelled (the model-provider stream cancellation described in [`model/README.md`](../model/README.md#transport--lifecycle), or an explicit user interrupt), the kernel MUST cascade-cancel every in-flight child `RunSession` reachable from that session, recursively through any grandchildren. This is a live mechanism, not a state-backend lookup: the kernel propagates cancellation through its own in-memory tracking of currently-running child sessions (the same in-memory session state depth-budget threading and cost-rollup use, [`../state-backend.md#live-vs-post-hoc-tree-walking`](../state-backend.md#live-vs-post-hoc-tree-walking)) — the state backend plays no role in deciding *which* sessions to cancel. Cancellation MUST NOT leave orphaned child sessions running after their parent has been torn down. Each cancelled child session MUST reach `session-end` with `status = cancelled`, durably recorded to its own `session_meta` row, and its `final_message` (if any partial content exists) MUST still be recorded to the state backend for audit, even though it never reaches a waiting parent (the parent is itself being cancelled).

## Inter-session communication

No surveyed harness supports peer-to-peer mid-task communication between sibling subagents; where coordination is needed, it goes through a layer above the subagent mechanism entirely, kept distinct from the parent-child spawn primitive itself. `RunSession` MUST NOT provide a sibling-to-sibling communication channel. Any future multi-agent coordination primitive (task boards, mailboxes) is out of scope for this document and should be a distinct mechanism layered above `RunSession`, not folded into it.
