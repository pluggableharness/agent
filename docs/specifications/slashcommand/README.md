# Slash-command provider protocol

Covers the **slash-command provider** category — a plugin that declares and directly executes one or more slash commands in its own right (`/deploy`, `/release-notes`, ...), rather than merely expanding a static prompt template. Sibling to [`tool/`](../tool/README.md) (tool provider) in structure and gating mechanics: a direct-invoke slash command is a tool-shaped operation — it declares an `input_schema`, a `kind`/`risk` classification, and a `ConcurrencySpec`, and it flows through the identical plan/apply gate a tool call does. This category exists so a plugin can declare and execute a command without also being a tool provider and aliasing into one of its own tool operations.

This category depends directly on [`tool/`](../tool/README.md): `SlashCommandSpec.kind`/`risk`/`concurrency` are `pluggableharness.tool.v1.ToolKind`/`RiskClass`/`ConcurrencySpec` reused directly, not redeclared, and `SlashCommandEvent.result`/`error`/`OutputChunk.stream` reuse `tool.v1.ToolResult`/`ToolError`/`OutputStream` the same way — see [`data-types.md#reused-toolv1-types`](data-types.md#reused-toolv1-types) for the full list. `SlashCommandSpec.input_schema` uses the same common JSON-Schema subset [`model/data-types.md#tool-schema`](../model/data-types.md#tool-schema) defines for tool authors, one wire type (`pluggableharness.schema.v1.Schema`) shared across every category that declares one.

**Not to be confused with a prompt-expansion slash command** — a purely static `/name` → template-string expansion that never executes anything and costs an ordinary model turn once expanded. That variant is `pluggableharness.common.v1.PromptExpansionSpec`, declared directly in any of the other six categories' own capability response (e.g. [`tool/protocol.md#getschema`](../tool/protocol.md#getschema)'s `slash_commands` field), and has zero dependency on this category's vocabulary. See [`data-types.md#slashcommandspec-vs-promptexpansionspec`](data-types.md#slashcommandspec-vs-promptexpansionspec) for the full distinction.

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). Standard handshake (magic cookie, protocol version negotiation) applies uniformly across all seven provider categories, per [`architecture.md`](../architecture.md#the-seven-provider-categories), and isn't repeated per category.

A slash-command provider plugin exposes four RPCs: `GetCapabilities`, `Configure`, `Invoke`, `Describe`. It MAY additionally implement `Render` (see [`protocol.md#render`](protocol.md#render)) and `Preview` (see [`protocol.md#preview`](protocol.md#preview)).

**`Invoke` is server-streaming**, identical in shape and cancellation semantics to [`tool/protocol.md#invoke`](../tool/protocol.md#invoke) — a direct-invoke command like `/deploy` may need to stream live output exactly as an `exec` tool call does, and the two RPCs share the identical streaming-plus-cancellation contract for the identical reason: none of the underlying primitives (process exec, HTTP call, file I/O) need mid-call client input on the same call. **Cancellation follows the tool-provider pattern exactly**: the kernel cancels/closes the gRPC stream; it is not a distinct RPC or a sentinel event the plugin must invent. Plugin authors MUST treat stream cancellation as a normal, expected event — kill the child process, release file handles/sockets, discard buffers — never as an error condition.

## Category structure

- [`protocol.md`](protocol.md) — the RPCs: `GetCapabilities`, `Configure`, `Invoke`, `Render`, `Preview`, `Describe`.
- [`data-types.md`](data-types.md) — `SlashCommandSpec`, `SlashCommandCall`, `SlashCommandEvent`, and the `pluggableharness.tool.v1` types this category reuses rather than redeclares.
- [`examples.md`](examples.md) — a worked `agent.hcl` provider block, the real proto wire definitions, and a full `Invoke` event sequence including cancellation.
- [`conformance.md`](conformance.md) — the error taxonomy (reused from [`tool/`](../tool/README.md)) and the MUST/SHOULD/MAY summary matrix, plus genuinely open questions.
