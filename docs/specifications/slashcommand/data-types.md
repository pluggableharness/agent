# Slash-command provider — data types

## `SlashCommandSpec`

Declares one directly-invocable command this provider exposes — a tool-shaped operation invoked via this same provider's own `SlashCommandService.Invoke`, never by naming another provider's tool operation.

```protobuf
SlashCommandSpec {
  name              string   // MUST — the command's name, without the leading "/". MUST be unique
                              // across every direct-invoke command declared by every provider in
                              // the session — a name collision at config-load time is a hard error.
  description       string   // MUST — shown in the frontend's hotkey_hints region and wherever
                              // else the frontend surfaces available commands
  input_schema      JSONSchema  // MUST — common subset per model/data-types.md#tool-schema;
                              // describes the shape of SlashCommandCall.arguments
  kind              tool.v1.ToolKind        // MUST — reused verbatim, see "Reused tool.v1 types" below
  risk              tool.v1.RiskClass       // MUST — reused verbatim
  concurrency       tool.v1.ConcurrencySpec // MUST, except for kind == interactive — reused verbatim
  streaming         bool     // MUST — true if Invoke may emit intermediate events (output_chunk,
                              // progress, partial_result) before the terminal event; false if
                              // Invoke always emits exactly one terminal event with no lead-up
  default_timeout   Duration?  // SHOULD — the deadline the kernel applies to Invoke for this
                              // command absent an agent.hcl override; omitted means the kernel's
                              // own global default applies instead
  idempotent        bool     // MUST — true iff re-running this command with identical arguments
                              // cannot produce a different end state than running it once; see
                              // tool/conformance.md#the-idempotent--retry-interaction
  // No output_schema: unlike a tool operation, a direct-invoke command is never presented to the
  // model as a callable tool — it dispatches without a model turn — so there is no LLM-facing
  // structured-output contract to validate its result against.
}
```

`kind`/`risk`/`concurrency` carry the identical MUST-level rules [`tool/protocol.md#getschema`](../tool/protocol.md#getschema) and [`tool/data-types.md`](../tool/data-types.md) define for `ToolSchema`'s same-named fields — see [`protocol.md#getcapabilities`](protocol.md#getcapabilities) for the full cross-reference. This is not incidental convergence: a direct-invoke command flows through the identical plan/apply gate a tool call does ([`pluggableharness.plan.v1.PlanItem`](../agent-loop/plan-apply-gate.md#plan-construction-and-policy-evaluation)), so it needs the identical classification vocabulary, not a parallel copy of it.

## `SlashCommandCall` / `SlashCommandEvent`

```protobuf
SlashCommandCall {
  id             string   // MUST — kernel-assigned, echoed in every emitted event for correlation
  name           string   // MUST — matches a SlashCommandSpec.name from this provider's
                          // GetCapabilities response
  arguments      JSON     // MUST — already-parsed JSON conforming to that SlashCommandSpec's
                          // input_schema
  call_context   CallContext  // MUST be set by the kernel — pluggableharness.common.v1.CallContext.
                          // Carries session_id/turn_id, echoed by the plugin on its own
                          // KernelCallbackService.Emit/Log calls for attribution, and
                          // working_directory — the session's cwd at call time.
}

SlashCommandEvent = oneof {
  output_chunk    { stream: tool.v1.OutputStream, data: bytes }
  progress        { message: string, fraction_complete: float? }
  partial_result  { payload: JSON }   // incremental structured output, e.g. progress lines as emitted
  exit_status     { exit_code: int, signal: string? }  // process-backed commands only
  result          tool.v1.ToolResult          // terminal, success — reused verbatim
  error           tool.v1.ToolError           // terminal, failure — reused verbatim, see
                                               // conformance.md#error-taxonomy
}
```

`SlashCommandEvent` is structurally identical to [`tool/data-types.md#toolcall--toolevent--toolresult`](../tool/data-types.md#toolcall--toolevent--toolresult)'s `ToolEvent`, and the same streaming contract applies verbatim: `output_chunk`, `progress`, and `partial_result` MAY each appear zero or more times before the stream's terminal event; `exit_status` MAY appear at most once, and only for a command whose implementation is process-backed; exactly one of `result`/`error` MUST close the stream; `output_chunk` ordering within one stream MUST be preserved. See [`protocol.md#invoke`](protocol.md#invoke) for the full ordering and cancellation semantics — this document does not restate them.

On the wire, `SlashCommandCall`/`SlashCommandEvent` are wrapped in thin per-RPC envelope messages (`InvokeRequest { call = 1; }`, `InvokeResponse { event = 1; }`), the same pattern [`tool/data-types.md`](../tool/data-types.md#toolcall--toolevent--toolresult) uses — see [`examples.md`](examples.md#the-wire-protocol) for the full message definitions.

## Reused `tool.v1` types

This category declares no parallel copy of any of the following — each is the literal `pluggableharness.tool.v1` message or enum, imported and reused as-is:

| Type | Reused as | Defined in |
|---|---|---|
| `ToolKind` | `SlashCommandSpec.kind` | [`tool/protocol.md#getschema`](../tool/protocol.md#getschema) |
| `RiskClass` | `SlashCommandSpec.risk` | [`tool/data-types.md#riskclass`](../tool/data-types.md#riskclass) |
| `ConcurrencySpec` | `SlashCommandSpec.concurrency` | [`tool/data-types.md#concurrencyspec`](../tool/data-types.md#concurrencyspec) |
| `ToolResult` | `SlashCommandEvent.result` | [`tool/data-types.md#toolcall--toolevent--toolresult`](../tool/data-types.md#toolcall--toolevent--toolresult) |
| `ToolError` | `SlashCommandEvent.error` | [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy) |
| `OutputStream` | `SlashCommandEvent.OutputChunk.stream` | [`tool/examples.md#the-wire-protocol`](../tool/examples.md#the-wire-protocol) |

A plugin MUST NOT redeclare any of these types under `pluggableharness.slashcommand.v1`; a kernel-side or plugin-side consumer decodes them exactly as it would decode the identically-named `tool.v1` message elsewhere in the system. This is a deliberate consequence of `kind`/`risk`/`concurrency`/results/errors meaning the same thing regardless of which of the two categories produced the call — see [`README.md`](README.md) for why this category exists as a sibling to `tool/` rather than folding into it.

## `SlashCommandSpec` vs. `PromptExpansionSpec`

Two deliberately distinct "slash command" concepts exist in this protocol:

| | `SlashCommandSpec` (this category) | `pluggableharness.common.v1.PromptExpansionSpec` |
|---|---|---|
| Executes anything | Yes — dispatches through this provider's own `Invoke`, and through the plan/apply gate for `TOOL_KIND_RESOURCE` commands | No — purely a static template expansion |
| Declared by | A `slashcommand.v1` provider, in `GetCapabilitiesResponse.commands` | Any of the other six categories, directly in their own capability response (e.g. [`tool/protocol.md#getschema`](../tool/protocol.md#getschema)'s `slash_commands` field) |
| Has `input_schema`/`kind`/`risk`/`concurrency` | Yes | No — only `name`, `description`, and a `template` string |
| Costs a model turn | Not inherently — a `TOOL_KIND_DATA_SOURCE` or `TOOL_KIND_RESOURCE` command executes directly; nothing about invoking it requires a model turn | Always — the kernel expands `template` with the user's arguments and submits the result as an ordinary `user_message`, which the model then turns on |
| Producer's own name-uniqueness namespace | Its own — a name collision among `SlashCommandSpec`s across every provider in the session is a hard error, independent of the `PromptExpansionSpec` namespace | Its own, independent of the `SlashCommandSpec` namespace above |

A tool provider (or any other category's provider) wanting a direct-invoke shortcut into one of its own operations implements `SlashCommandService` alongside its own category's service in the same process — `hashicorp/go-plugin` muxes multiple gRPC services per subprocess connection, the same mechanism [`agent-loop/hook-dispatch.md#wire-contract--pluggableharnesshookv1`](../agent-loop/hook-dispatch.md#wire-contract--pluggableharnesshookv1) already relies on for `HookSubscriberService`. See [`conformance.md#open-questions`](conformance.md#open-questions) for the current, expected-common, unresolved shape of that combination.
