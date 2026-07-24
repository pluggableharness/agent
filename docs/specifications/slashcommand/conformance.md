# Slash-command provider — conformance

## Error taxonomy

This category defines no `SlashCommandError`/`SlashCommandErrorCategory` of its own — `SlashCommandEvent.error` is a `pluggableharness.tool.v1.ToolError` reused verbatim, per [`data-types.md#reused-toolv1-types`](data-types.md#reused-toolv1-types), so its failure taxonomy is [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy)'s `ToolErrorCategory`, unmodified: `invalid_arguments`, `not_found`, `permission_denied`, `execution_failed`, `timeout`, `concurrency_conflict`, `cancelled`, `process_crashed`, `unknown`. A plugin MUST classify every `Invoke` failure using this same enum, MUST NOT collapse them into one generic error, and MUST NOT invent a parallel category — a direct-invoke command's failure modes are the same domain as a tool call's (no vendor-specific concepts like `rate_limited` apply here any more than they do to a tool operation).

`process_crashed` carries the identical kernel-synthesized-only rule [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy) documents: a plugin never constructs one itself, only the kernel does, from a transport-level failure. On the wire it maps to `codes.Unavailable`, per `.claude/rules/grpc.md`'s canonical error-taxonomy-to-`codes` mapping table.

The kernel's expected reaction per category is the same table [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy) already specifies — this document does not duplicate it. One addition specific to this category: because `SlashCommandSpec` declares no `output_schema` (see [`data-types.md#slashcommandspec`](data-types.md#slashcommandspec)), the `unknown`-category "malformed payload" trigger [`tool/protocol.md#invoke`](../tool/protocol.md#invoke) describes for a `ToolResult` failing `output_schema` validation has no equivalent here — a `SlashCommandEvent.result.payload` is accepted without a schema to validate it against, so it can never itself be the cause of an `unknown`-category `ToolError`.

### The `idempotent` / retry interaction

Reused verbatim from [`tool/conformance.md#the-idempotent--retry-interaction`](../tool/conformance.md#the-idempotent--retry-interaction), with `SlashCommandSpec.idempotent` standing in for `ToolSchema.idempotent`: the kernel MAY auto-retry a retryable `ToolError` for a `TOOL_KIND_RESOURCE` command — without first surfacing it to the model as a failed call — only when that command's `idempotent` is `true`. `TOOL_KIND_DATA_SOURCE` commands are exempt from this gate entirely. `TOOL_KIND_INTERACTIVE` calls are never auto-retried.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `GetCapabilities` / `Configure` / `Invoke` RPCs | MUST | the whole protocol surface |
| `Describe` RPC | MUST | [`protocol.md#describe`](protocol.md#describe); needed for `dev_overrides` plugin identity per [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry) |
| Streaming RPC shape for `Invoke` | MUST | see [`README.md`](README.md#transport--lifecycle) / [`protocol.md#invoke`](protocol.md#invoke) — applies even to non-streaming commands |
| `SlashCommandCall.call_context` | MUST be set by the kernel, every `Invoke` call | [`protocol.md#invoke`](protocol.md#invoke) |
| `input_schema` in the common JSON-Schema subset | MUST | [`data-types.md#slashcommandspec`](data-types.md#slashcommandspec) |
| `kind` (resource / data_source / interactive) | MUST, per command | reused from `tool.v1.ToolKind`; drives the plan/apply gate identically to a tool operation |
| `risk` classification | MUST, per command | reused from `tool.v1.RiskClass`; `read_only` for `data_source` and `interactive` alike |
| `ConcurrencySpec.safe` | MUST, per command except `interactive` | absent/unset MUST be treated as `false`; MUST NOT be declared for `interactive` |
| `ConcurrencySpec.key_fields` | MAY, per command | only meaningful under `safe: true` |
| `default_timeout` | SHOULD, per command | absent means the kernel's global default applies |
| `idempotent` | MUST, per command | gates kernel auto-retry, see above |
| `supported_hook_points` | MAY | empty means this provider subscribes no `hook{}` blocks |
| `exit_status` event | MUST for process-backed commands; MUST NOT otherwise | |
| `output_chunk` / `progress` / `partial_result` events | MAY | only for commands with `streaming: true` |
| Structured `ToolError` taxonomy, including `process_crashed` | MUST | reused from [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy) |
| `output_schema` | Not applicable | `SlashCommandSpec` declares none — a direct-invoke command is never model-callable, see [`data-types.md#slashcommandspec`](data-types.md#slashcommandspec) |
| Best-effort partial-mutation report on cancellation | MUST, for `resource` commands | see [`protocol.md#invoke`](protocol.md#invoke) |
| `Render` | MAY | generic fallback exists; `RenderRequest.schema_version` per [`frontend/render-tree.md#schema-versioning`](../frontend/render-tree.md#schema-versioning) |
| `Preview` | MAY | [`protocol.md#preview`](protocol.md#preview); kernel MUST fall back to raw `arguments` when absent; MUST NOT mutate anything when implemented |

## Open questions

- **Combining `ToolService` and `SlashCommandService` in one plugin process.** A provider that wants a direct-invoke shortcut into one of its own tool operations — rather than duplicating that operation's logic inside a separate `slashcommand.v1`-only plugin — implements both `ToolService` and `SlashCommandService` on the same subprocess connection. `hashicorp/go-plugin` supports this natively (multiple gRPC services muxed over one broker connection), and it's not a novel pattern in this protocol: `HookSubscriberService` already establishes the identical precedent for any category's plugin optionally implementing a second service alongside its primary one, per [`agent-loop/hook-dispatch.md#wire-contract--pluggableharnesshookv1`](../agent-loop/hook-dispatch.md#wire-contract--pluggableharnesshookv1). This combination is expected to be common — most first-party direct-invoke commands will likely live inside an existing tool provider rather than a standalone `slashcommand.v1`-only plugin — but the exact `agent.hcl`/lock-file mechanics of "one provider block, two category services, potentially two independent `GetCapabilities`-equivalent calls, one shared `Configure`" are genuinely unresolved here and flagged as open rather than assumed settled.
- **Whether a name collision check across `SlashCommandSpec.name` and `PromptExpansionSpec.name` is required.** [`data-types.md#slashcommandspec-vs-promptexpansionspec`](data-types.md#slashcommandspec-vs-promptexpansionspec) states the two occupy independent namespaces at the protocol level, but from a user's point of view both surface as `/name` in the same frontend command palette — whether the config-load-time collision check each namespace already enforces on its own should be widened to cover both specs together (so `/deploy` can't simultaneously be a direct-invoke command from one provider and a prompt-expansion command from another) is left to a future revision of that check, not decided here.
