# Agent profiles

`agent_profile "<name>" { ... }` blocks are named, scoped capability sets — model routing, tool scope, policy-adjacent bounds, and depth budget — that a session (root or sub-agent spawn) selects rather than inheriting the parent session's full, unscoped capabilities. See [`glossary.md`](../glossary.md).

```hcl
agent_profile "default" {
  model {
    primary  { provider = "anthropic", id = "claude-opus-4-8" }
    fallback { provider = "anthropic", id = "claude-sonnet-5" }
    fallback { provider = "anthropic", id = "claude-haiku-4-5" }
  }

  max_turns        = 200
  max_cost_usd     = 5.00
  max_wall_clock_s = 3600
}

agent_profile "code-reviewer" {
  model {
    primary  { provider = "anthropic", id = "claude-sonnet-5" }
    fallback { provider = "anthropic", id = "claude-haiku-4-5" }
  }

  tools = [
    "filesystem.read_file",
    "search.*",
  ]

  slash_commands = ["compact"]

  max_depth                = 1
  max_concurrent_subagents = 4
  max_turns                = 40
}
```

## The implicit root profile

The root/main interactive session is not architecturally distinct from a sub-agent session — both are `RunSession` invocations (see [`../kernel-callbacks.md`](../kernel-callbacks.md)). The kernel uses the profile named **`default`** for the root session unless the CLI is told otherwise. If no `agent_profile "default"` block exists, kernel-builtin defaults apply for every field below.

## Model routing

`model` is a structured block, not an inline shorthand string — one `primary` and zero or more `fallback` sub-blocks, each an explicit `{provider, id}` pair resolved against `required_providers`/`provider` blocks (see [`blocks-reference.md`](blocks-reference.md)). Fallback candidates MUST additionally satisfy the model provider's capability-aware routing rule (see [`../provider/README.md`](../provider/README.md)): a candidate is only eligible for a given turn if it satisfies that turn's actual requirements (context length needed, tool-use, vision, thinking), checked mechanically, not just listed in declaration order.

Remember that `primary`/`fallback` are HCL blocks with two attributes each (`provider`, `id`) — they MUST be written multi-line; see [`blocks-reference.md#hcl-single-line-blocks-take-only-one-argument`](blocks-reference.md#hcl-single-line-blocks-take-only-one-argument).

Model selection walks `primary` then `fallback` entries in declared order and returns the first candidate whose capabilities satisfy the turn's actual requirements (tool-use, vision, thinking, minimum context window). Declaration order is a preference, not the sole criterion: a candidate ineligible for a turn's actual requirements is skipped even if it comes first. A model reference that isn't available this session (its provider or model wasn't loaded) is treated as ineligible and skipped, not as an error — only exhausting the whole chain with no eligible candidate is a failure.

## Tool scoping

`tools` is a flat list of `"<provider>.<tool_name>"` strings, with `"<provider>.*"` as a wildcard meaning every operation that provider's `GetSchema` exposes. A profile that omits `tools` entirely inherits **no** tools — an intentionally strict default: an `agent_profile` that forgets to list tools gets a session that can only produce text, not one that silently inherits the full parent capability set.

Recursion (a session spawning further sub-agents) needs no separate boolean flag: a profile simply cannot spawn children unless a spawn-capable tool (e.g. `agent.spawn_subagent`) is present in its own `tools` list. "Recursion disabled by default" falls out of the strict-default rule above, without inventing a redundant field.

**`slash_commands`** closes a gap the strict tool-scoping default doesn't cover: `direct_invoke` slash commands are naturally scoped by `tools` (a command naming an out-of-scope tool simply isn't registered), but `prompt_expansion` commands have no backing tool to scope against. `slash_commands` is a flat allow-list of command names (`["compact", "clear"]`); a profile omitting it entirely inherits **no** `prompt_expansion` commands — the same strict-default posture as `tools`, for the same reason. This has no effect on `direct_invoke` commands, which remain scoped by `tools` alone. See [`../frontend/README.md`](../frontend/README.md).

A profile's `tools` scoping is resolved against the set of providers actually loaded this session — each loaded provider's advertised tool names — into the concrete allowed set.

An unknown tool name is handled differently depending on whether its provider is loaded at all:

- A `"<provider>.<tool_name>"` entry naming a **loaded** provider but a tool name that provider doesn't actually advertise MUST be a config-validation-time error. This is deliberately strict: the information needed to catch a typo (the provider's real schema) is right there, and a misspelled tool name silently resolving to "granted nothing" would be a much harder bug to notice than a load-time error naming the bad entry.
- A wildcard or concrete entry naming a provider **not loaded at all this session** is a silent no-op, not an error — the provider may simply be absent this session, and there's no advertised-tool list to check the name against in the first place.

Separately, a malformed scoping string — missing the `.` separator, or an empty provider/tool half (`"filesystem"` or `"filesystem."`) — MUST also be a config-validation-time error, rather than being silently parsed as a provider or tool with an empty name.

## Depth budget

`max_depth` is not a static per-profile ceiling checked in isolation — it's an inherited budget that only ever shrinks going down the session tree, consistent with the "MUST NOT be widened" rule [`../agent-loop/subagents.md#depth-limits`](../agent-loop/subagents.md#depth-limits) applies to scoped providers generally. See that document for the full depth-budget rule and its rationale; this section only covers the `agent_profile{}` field surface.

A profile's own `max_depth` still acts as a hard ceiling it never exceeds on its own, but an ancestor's tighter budget always wins going down — a permissive profile spawned deep in an already-deep tree cannot use its own generous `max_depth` to smuggle past an ancestor's tighter one. When a session's remaining depth reaches zero or below, the kernel MUST exclude every spawn-capable tool from that session's resolved tool registry.

Computing remaining depth differs between a root session and a child session, since each has a different fallback for "unset": a root session's remaining depth falls back to the kernel's own configured default when the profile's `max_depth` is unset. A child session's remaining depth falls back to effectively unbounded when its own profile's `max_depth` is unset, and is otherwise the smaller of the parent's remaining depth minus one and its own ceiling. See [`../agent-loop/subagents.md#depth-limits`](../agent-loop/subagents.md#depth-limits) for the full depth-budget rule this computation implements.

## Loop bounds

`max_turns`, `max_cost_usd`, `max_wall_clock_s` are ordinary `agent_profile` fields, matching [`../agent-loop/turn-algorithm.md`](../agent-loop/turn-algorithm.md)'s `LoopBounds` data type exactly — no separate top-level `session { ... }` block exists; loop bounds are just profile fields like `max_depth`/`max_concurrent_subagents` already are, consistent with the "root session is just a profile" framing above. `max_cost_usd` is a float (the example's `5.00` carries a fractional dollar amount); `max_turns` and `max_wall_clock_s` are integers.

## Explicit hook subscriptions

```hcl
hook "post-tool-call" {
  provider = "audit-logger"
  mode     = "observe"
}
```

Most hook subscriptions are **implicit by provider category** — a context provider is automatically subscribed to `context-assemble`, policy is automatically the privileged `veto` subscriber at `plan-ready`, etc. — with zero `agent.hcl` boilerplate for the common case. An explicit `hook "<point>" { provider = "<name>", mode = "observe" | "transform" | "veto" }` block exists only for a plugin subscribing to a hook point its category doesn't imply by default (e.g. a generic audit-logger provider wanting `observe`-mode visibility at `post-tool-call`, which isn't any provider category's default hook). See [`../agent-loop/hook-dispatch.md`](../agent-loop/hook-dispatch.md).

**Ordering across implicit and explicit subscriptions**, at a given hook point, is resolved by **textual declaration position in `agent.hcl`** — an implicit subscription's position is wherever its `provider "<name>" { ... }` block appears in the file; an explicit subscription's position is wherever its `hook { ... }` block appears. Because `agent.hcl` is a single file (see [`README.md#file-location--loading`](README.md#file-location--loading)), "textual position" is unambiguous without needing a separate explicit-order field — this is what satisfies hook dispatch's "declaration order, not runtime registration order" requirement.
