# Tool provider — protocol

The three RPCs a tool provider plugin exposes, plus `Describe` (MUST) and the optional `Render`/`Preview`. See [`README.md`](README.md#transport--lifecycle) for the transport-level framing (server-streaming, cancellation) that applies to `Invoke` specifically.

## `GetSchema`

Returns a list of `ToolSchema` values, one per operation the plugin exposes. Like [`model/protocol.md#getcapabilities`](../model/protocol.md#getcapabilities), this MUST be re-queryable cheaply and MUST NOT require a network call — a provider wrapping a hosted service (e.g. a web-search API) declares its schema statically; only `Invoke` talks to the network.

```protobuf
ToolSchema {
  name              string   // MUST — unique within this provider's namespace, e.g. "read_file"
  kind              enum { resource, data_source, interactive }  // MUST — drives the
                              // plan/apply gate. resource = mutating, gated behind
                              // approval. data_source = read-only, executes freely.
                              // interactive = blocks the turn for human input, mutates
                              // and reads nothing external — see below.
  risk              RiskClass  // MUST — see data-types.md#riskclass
  description       string   // MUST — shown to the model for tool selection and in plan diffs
  input_schema      JSONSchema  // MUST — common subset per model/data-types.md#tool-schema
  output_schema     JSONSchema  // MUST — same subset; describes the `result` payload shape
  streaming         bool     // MUST — true if Invoke may emit intermediate events (output_chunk,
                              // progress, partial_result) before the terminal event; false if
                              // Invoke always emits exactly one terminal event with no lead-up
  concurrency       ConcurrencySpec  // MUST, except for kind == interactive — see data-types.md#concurrencyspec
  default_timeout   Duration?  // SHOULD — the deadline the kernel applies to Invoke for this
                              // operation absent an agent.hcl override; omitted means the
                              // kernel's own global default applies instead
  idempotent        bool     // MUST — true iff re-running this operation with identical
                              // arguments cannot produce a different end state than running it
                              // once; see conformance.md#error-taxonomy for the retry interaction
}
```

`kind` and `risk` are deliberately separate axes. `kind` is the binary the plan/apply gate mechanically needs — [`configuration/policy-dsl.md`](../configuration/policy-dsl.md)'s policy examples match on it directly (`match = { kind = "data_source" }`). `risk` exists because `kind = resource` alone is too coarse for policy or UX to treat uniformly — the `bash`/`exec` operation alone spans everything from `ls` to `rm -rf $DIR`. A `resource` MUST declare one of `low`/`moderate`/`high`/`critical`; there is no `resource` with `read_only` risk. `risk` MUST be `read_only` for `kind == data_source` and `kind == interactive` alike — neither mutates nor reads anything external, so neither has a blast radius to classify. See [`data-types.md#riskclass`](data-types.md#riskclass) for the full enum, and [`reference-catalog.md`](reference-catalog.md) for how the reference tool set is classified in practice.

`default_timeout` and `idempotent` are both new, independent capability hints, not part of the `kind`/`risk` classification above. `default_timeout` lets a plugin author declare a sensible per-operation deadline (a `web_search` call and a `read_file` call warrant very different defaults) without every `agent.hcl` author having to override it by hand; the kernel's own configured global default (`configuration/settings-and-global.md`) is the fallback when it's absent. `idempotent` exists purely to gate auto-retry: the kernel MAY only auto-retry a retryable `ToolError` for a `TOOL_KIND_RESOURCE` operation when that operation's `idempotent` is `true` — a `TOOL_KIND_DATA_SOURCE` operation is implicitly safe to retry regardless of this field, since it cannot mutate anything by definition. See [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy) for the full retry interaction.

### `kind: interactive`

A genuine third `kind`, alongside `resource` and `data_source`, for calls that neither mutate state nor perform a pure read — they block the current turn on a human response (per [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md)'s `interactive_request`/`interactive_response` `ServerEvent`/`ClientEvent` pair) and produce no state mutation of their own — the human's answer becomes the tool's `result`. `ask_user` is the canonical example; see [`reference-catalog.md`](reference-catalog.md) for why it doesn't fit `resource` or `data_source`.

- `interactive` calls MUST NOT go through the resource plan/apply gate — there's nothing to approve, only a question to answer. See [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md#data-source-and-interactive-calls).
- `interactive` calls MUST still pass through a policy precheck before executing — the same non-interactive, `allow`/`deny`-only lane [`configuration/policy-dsl.md`](../configuration/policy-dsl.md) already defines for `data_source` calls, extended to cover this kind too. This exists specifically so an operator can `deny` interactive prompts outright in a non-interactive/headless invocation (a future pipeline mode per [`architecture.md`](../architecture.md#cli-shape)) where there is no human attached to answer one — without policy coverage, an `ask_user`-shaped call in a headless context would simply hang forever with no one able to respond. Note that policy's own `Match.Kind` field stays two-valued (`resource`/`data_source`) in v1 — an interactive call routes through the same non-interactive-style precheck path a `data_source` call uses, rather than policy gaining a third match kind of its own. See [`configuration/policy-dsl.md`](../configuration/policy-dsl.md#match-schema).
- `interactive` calls MUST execute **sequentially**, never concurrently with other `interactive` calls in the same turn, regardless of any declared `ConcurrencySpec` — asking a human two things at once in one frontend is inherently confusing. `ConcurrencySpec` MUST NOT be declared for an `interactive` operation; if present, the kernel MUST ignore it and enforce sequential execution unconditionally.

The overall `GetSchema` response (the wrapper around this list of `ToolSchema`s) MAY additionally include `slash_commands: []SlashCommandSpec`, per [`frontend/frontend-protocol.md`](../frontend/frontend-protocol.md) — each entry's `tool_name` MUST reference one of this same provider's own operations declared above, and `supported_hook_points: []pluggableharness.agent.common.v1.HookPoint`, naming which of the eight dispatchable hook points (`agent-loop/hook-dispatch.md`) this provider's `HookSubscriberService` subscribes to per its own `agent.hcl` `hook{}` blocks. Same capability-advertisement semantics as the other five plugin categories: it lets the kernel validate a `hook{}` declaration against what the plugin actually supports at config-load time, rather than discovering an unsupported subscription only when that hook point first fires.

## `Configure`

Same contract as [`model/protocol.md#configure`](../model/protocol.md#configure): config decoded from the provider's `agent.hcl` block via the schema-to-cty bridge; field contents are provider-specific.

- `Configure` MUST reject with a structured error on missing required fields (e.g. an `exec` provider requiring a working-directory jail root) rather than deferring failure to the first `Invoke`.
- A plugin MUST NOT echo a received secret (API keys for a hosted `web_search` provider, etc.) into an `Emit`'d event, `Render` output, log line, or error message.
- Tool providers commonly need a **capability boundary** distinct from secrets — a filesystem provider's allowed root path(s), an exec provider's sandbox policy, a web-fetch provider's domain allowlist. These are ordinary `Configure` fields, not a separate mechanism; every OS-level-isolated harness (Claude Code, Codex CLI, Cursor, Zed) enforces exactly this kind of boundary, so `Configure` MUST support it even though this protocol does not mandate a specific field name or enforcement mechanism (that's a provider/kernel concern, not a protocol one — see [`conformance.md#open-questions`](conformance.md#open-questions) on OS-level sandboxing specifically).

## `Invoke`

Request: a `ToolCall`. Response: a stream of `ToolEvent`s. See [`data-types.md`](data-types.md#toolcall--toolevent--toolresult) for the full message shapes and [`examples.md#a-full-invoke-event-sequence`](examples.md#a-full-invoke-event-sequence) for a worked sequence.

Semantics:

- **`ToolCall.call_context` MUST be set by the kernel on every `Invoke` call** — see [`data-types.md#toolcall--toolevent--toolresult`](data-types.md#toolcall--toolevent--toolresult) for the field's shape. Its `working_directory` is what a process-backed operation (the reference catalog's `exec`/`bash`, `read_file`, and similarly-shaped tools) MUST resolve any relative-path argument against; without it, those tools have no defined cwd to operate relative to and are unusable. Its `session_id`/`turn_id` are what the plugin echoes back on its own `KernelCallbackService.Emit`/`Log` calls (`kernel-callbacks.md`) for correlation, sparing every provider from having to thread those IDs through by hand.
- **`output_schema` conformance is enforced strictly, not advisory.** The kernel MUST validate a `result.payload` against the operation's declared `output_schema` before accepting it. A non-conforming payload MUST be rejected and re-surfaced to the plugin boundary as an `unknown`-category `ToolError` (see [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)) — not silently passed through to history, and not a warning-and-continue. Malformed data flowing into the state backend is a correctness bug, not a UX inconvenience to be lenient about.
- Exactly one of `result` or `error` MUST close the stream. `output_chunk`, `progress`, and `partial_result` MAY each appear zero or more times before it; `exit_status` MAY appear at most once, and only for tools whose underlying operation is a child process (the exec/shell family).
- `exit_status` is distinct from `result` because the two can genuinely be different moments: an exec tool's child process can exit while the tool itself is still doing post-processing (truncating output, computing a diff) before it can emit a conformant `result`. Providers for non-process-backed tools (file read, grep, web fetch) MUST NOT emit `exit_status`.
- A plugin whose operation is not naturally incremental (e.g. `file_read`) MUST still implement the streaming RPC shape, emitting a single terminal `result` with no lead-up events — the same non-streaming-backend accommodation [`model/protocol.md#streamcompletion`](../model/protocol.md#streamcompletion) makes for `StreamCompletion`. `ToolSchema.streaming = false` signals this as a UX hint.
- On cancellation (see [`README.md`](README.md#transport--lifecycle)), a plugin for a `resource` operation MUST make a best effort to report, via a final `output_chunk`/`partial_result` before the stream closes, what had actually happened before the cancel landed (e.g. "process received SIGTERM, partial output already streamed is valid") — the plan/apply audit log needs an honest record of partial mutation, not silence. A plugin MUST NOT synthesize a `result` claiming full success after a cancelled operation.
- `output_chunk` ordering within a single stream MUST be preserved (stdout/stderr interleaving is otherwise ambiguous); the kernel treats `stream` as a hint for display, not a demultiplexing key the plugin can reorder around.

## Render

Same optionality as [`model/protocol.md#render`](../model/protocol.md#render) — returning the `RenderTree` formally defined in [`frontend/render-tree.md`](../frontend/render-tree.md) — but tool-result rendering is where custom `Render` matters *more* than it does for model providers (per [`architecture.md`](../architecture.md#emit--render--paint-pipeline), "the tool-result side ... is where custom rendering matters more"). Reference examples: an `edit_file` result rendering as a unified diff rather than raw before/after text; an `exec` result's accumulated `output_chunk`s rendering as a scrollback pane; a `spawn_subagent` result rendering as a collapsible sub-session node ([`architecture.md`](../architecture.md#emit--render--paint-pipeline)'s `RenderTree` already reserves a node type for this). If not implemented, the kernel falls back to its generic default (pretty-printed JSON payload).

`RenderRequest.schema_version` names which version of the plugin's own emitted-payload schema `payload` was written under, per [`frontend/render-tree.md#schema-versioning`](../frontend/render-tree.md#schema-versioning) — the canonical definition of the versioning scheme every category's `Render` shares. It lets a long-lived plugin decode a `payload` that an older build of itself emitted, without the kernel needing to know anything about the plugin's internal payload format.

## Preview

`Preview` returns a dry-run, human-readable description of what `Invoke(call)` *would* do, without doing it. Request: a `PreviewRequest` wrapping the same `ToolCall` shape `Invoke` takes; MUST NOT actually be executed by the plugin. Response: a `PreviewResponse` wrapping a `RenderTree` (the same [`frontend/render-tree.md`](../frontend/render-tree.md) type `Render` returns), describing the call's effect — e.g. an `edit_file` call previews as the unified diff it would apply, a `bash` call previews as the command line it would run.

- MAY be implemented. A kernel MUST tolerate its absence (an unimplemented `Preview`, or a provider whose `GetSchema` never advertises support) and fall back to showing the call's raw `arguments` in the plan/apply gate's permission UI.
- MUST NOT mutate anything and MUST be side-effect-free — the same guarantee a `TOOL_KIND_DATA_SOURCE` operation makes, but here it applies unconditionally to `Preview` itself regardless of the underlying call's `ToolKind`. A plugin that cannot produce a preview without performing (part of) the operation MUST NOT implement `Preview` for that operation rather than violate this.
- Exists specifically to feed `pluggableharness.agent.plan.v1.PlanItem.preview` (a sibling protocol revision to this one) — the plan/apply gate renders that field to show a human what a pending `resource` call will actually do before they approve it. `PlanItem.preview` and `PreviewResponse.preview` are pinned to the exact same `pluggableharness.agent.render.v1.RenderTree` type by design, so the kernel can store a `Preview` call's output directly as a plan item's preview without any conversion.

## Describe

`Describe` reports this plugin build's own identity: request is empty (`DescribeRequest {}`), response is a `DescribeResponse` wrapping a single `pluggableharness.agent.common.v1.ProducerRef producer`. MUST be implemented — every one of the six category protocols gains this RPC in this same protocol revision.

This exists for the [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry) `dev_overrides` case: a plugin resolved via `dev_overrides` has no `provider "<name>" { ... }` lock-file entry for the kernel to read `{name, version, source, category, protocol_version}` from, because `dev_overrides` exists precisely to bypass registry/lock-file resolution. `Describe` lets the kernel obtain that same identity directly from the running process instead, at connection time.
