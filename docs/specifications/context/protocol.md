# Context provider — protocol

The three RPCs a context provider plugin MUST expose, plus the one it MAY. See [`README.md`](README.md#transport--lifecycle) for the transport-level framing (unary, not streamed) that applies to `Contribute` specifically.

## `GetCapabilities`

Returns a `ContextCapabilities` value describing this provider's static properties:

```protobuf
ContextCapabilities {
  default_token_budget int    // MUST — the cap this provider requests if
                                // agent.hcl does not override it, see
                                // data-types.md#ordering--chaining
  stability  enum { static, dynamic }  // MUST
  compactor  bool              // MUST, default false
}
```

`stability` and `compactor` MUST be re-queryable cheaply and MUST NOT depend on a live read of the content source — a provider reading CLAUDE.md declares `stability` as a fixed property of the plugin, not a per-call judgment.

`compactor` is a normal capability flag, not a special case: a context provider MAY declare itself the one responsible for compacting or summarizing content — including, but not limited to, other providers' already-assembled sections and the session's conversation history — when the context budget is under pressure. Declaring `compactor: true` extends what this provider is allowed to touch on `Contribute` (see [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining)); a provider that doesn't declare it stays confined to its own section(s).

`ContextCapabilities` MAY additionally include `slash_commands: []SlashCommandSpec` and MUST include the provider's `ConfigSchema`, so the kernel knows what fields `Configure` expects before ever calling it — the same shape every provider category's `GetCapabilities` follows, see [`provider/protocol.md#getcapabilities`](../provider/protocol.md#getcapabilities).

## `Configure`

Accepts a config object decoded from the provider's `agent.hcl` block via the schema-to-cty bridge (see [`configuration/blocks-reference.md`](../configuration/blocks-reference.md)). Field contents are provider-specific — which file(s)/globs to read, max-hop `@import` depth, whether to strip HTML comments, etc. This protocol doesn't mandate a shape beyond:

- The kernel places no opinion on convention-file *format*. A CLAUDE.md reader and an AGENTS.md reader MAY be configured and loaded simultaneously in the same `agent.hcl` — see [`README.md`](README.md).
- `Configure` MUST reject with a structured error if a declared source path/glob cannot be resolved to anything on disk, rather than deferring to a silent-empty `Contribute` at first call.

## `Contribute` (the `context-assemble` RPC)

```text
Contribute(ContextRequest) -> ContextContribution
```

The kernel invokes `Contribute` at least once per turn, before each model call (see [`README.md#firing-cadence--jit-loading`](README.md#firing-cadence--jit-loading)). `Contribute` MUST return the full accumulated `[]ContextSection` chain — this provider's own section appended to `ContextRequest.prior_sections` — never a delta. This mirrors [`architecture.md`](../architecture.md#hook-dispatch-semantics)'s `transform` hook-mode contract, where "each subscriber... returns a modified version, the next subscriber sees the transformed payload." See [`data-types.md`](data-types.md#contextrequest) for the full request/response schema and [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining) for what a provider MAY and MUST NOT touch in the chain it receives.

### Session-wide conversation compaction

A provider declaring `compactor: true` MAY see and rewrite the session's conversation history, not just other providers' sections. `ContextRequest` carries a `conversation_history` field populated **only** for a compactor provider — a non-compactor provider MUST NOT receive it, symmetric with the "own section only unless compactor" rule above. A compactor's `Contribute` response MAY include `rewritten_history` alongside its ordinary section contribution; when present, the kernel MUST replace the turn's conversation history with this value before the next model call (see [`agent-loop/turn-algorithm.md`](../agent-loop/turn-algorithm.md)). This reuses the same `context-assemble` hook payload rather than inventing a second mechanism — the "producer, not a generic byte-slicer, owns the reduction" principle that governs section-content budgeting ([`data-types.md#ordering--chaining`](data-types.md#ordering--chaining)) extends to the conversation itself.

A non-compactor provider whose `Contribute` response mutates a section it doesn't own is a protocol violation: the kernel MUST discard that provider's entire response for the turn (drop its own section, restore the chain as it was before the call) and MUST log the violation, rather than accepting the mutation or failing the whole session. See [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)'s `scope_violation` category.

## `Render`

Context providers MAY implement `Render` per the general Emit→Render→Paint pipeline ([`architecture.md`](../architecture.md#emit--render--paint-pipeline)), returning the `RenderTree` formally defined in [`frontend/render-tree.md`](../frontend/render-tree.md) — e.g. to render an injected CLAUDE.md section collapsed by default in a transcript view, distinct from the live conversation. If not implemented, the kernel falls back to its generic default rendering.
