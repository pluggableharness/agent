# Slash-command provider — protocol

The three RPCs a slash-command provider plugin exposes, plus `Describe` (MUST) and the optional `Render`/`Preview`. See [`README.md`](README.md#transport--lifecycle) for the transport-level framing (server-streaming, cancellation) that applies to `Invoke` specifically.

## `GetCapabilities`

Returns a list of `SlashCommandSpec` values, one per direct-invoke command the plugin exposes. Like [`tool/protocol.md#getschema`](../tool/protocol.md#getschema), this MUST be re-queryable cheaply and MUST NOT require a network call — only `Invoke` talks to the network. See [`data-types.md#slashcommandspec`](data-types.md#slashcommandspec) for the full message shape.

`kind`, `risk`, and `concurrency` are the same `pluggableharness.tool.v1` types and carry the identical MUST-level semantics [`tool/protocol.md#getschema`](../tool/protocol.md#getschema) already specifies — `kind` drives the plan/apply gate exactly as it does for a tool operation, `risk` MUST be one of `low`/`moderate`/`high`/`critical` for `TOOL_KIND_RESOURCE` and MUST be `read_only` for `TOOL_KIND_DATA_SOURCE`/`TOOL_KIND_INTERACTIVE`, and `TOOL_KIND_INTERACTIVE` commands MUST NOT declare a `ConcurrencySpec` and MUST execute sequentially — see [`tool/protocol.md#kind-interactive`](../tool/protocol.md#kind-interactive). This category does not restate that reasoning; it applies verbatim to a `SlashCommandSpec` the same way it applies to a `ToolSchema`.

`default_timeout` and `idempotent` carry the same meaning [`tool/protocol.md#getschema`](../tool/protocol.md#getschema) defines for the identically-named `ToolSchema` fields: `default_timeout` is the deadline the kernel applies to `Invoke` absent an `agent.hcl` override, and `idempotent` gates whether the kernel MAY auto-retry a retryable `ToolError` for a `TOOL_KIND_RESOURCE` command without first surfacing the failure — see [`tool/conformance.md#the-idempotent--retry-interaction`](../tool/conformance.md#the-idempotent--retry-interaction).

Unlike `ToolSchema`, `SlashCommandSpec` declares no `output_schema`: a direct-invoke command is never presented to the model as a callable tool and dispatches without a model turn, so there is no LLM-facing structured-output contract to validate its result against.

The `GetCapabilitiesResponse` wrapper around this list also carries `config_schema` (this provider's `agent.hcl` config schema, per [`configuration/blocks-reference.md#the-schema-to-cty-bridge`](../configuration/blocks-reference.md#the-schema-to-cty-bridge)) and `supported_hook_points: []pluggableharness.common.v1.HookPoint`, naming which of the eight dispatchable hook points ([`agent-loop/hook-dispatch.md`](../agent-loop/hook-dispatch.md)) this provider's `HookSubscriberService` subscribes to per its own `agent.hcl` `hook{}` blocks — same capability-advertisement semantics as every other plugin category: it lets the kernel validate a `hook{}` declaration against what the plugin actually supports at config-load time, rather than discovering an unsupported subscription only when that hook point first fires.

## `Configure`

Same contract as [`tool/protocol.md#configure`](../tool/protocol.md#configure): config decoded from the provider's `agent.hcl` block via the schema-to-`cty` bridge; field contents are provider-specific.

- `Configure` MUST reject with a structured error on missing required fields rather than deferring failure to the first `Invoke`.
- A plugin MUST NOT echo a received secret into an `Emit`'d event, `Render` output, log line, or error message.
- A slash-command provider needing a capability boundary (a filesystem root, a sandbox policy, a domain allowlist) declares it as an ordinary `Configure` field, exactly as [`tool/protocol.md#configure`](../tool/protocol.md#configure) describes for a tool provider — this protocol does not mandate a specific field name or enforcement mechanism.

## `Invoke`

Request: a `SlashCommandCall`. Response: a stream of `SlashCommandEvent`s. See [`data-types.md#slashcommandcall--slashcommandevent`](data-types.md#slashcommandcall--slashcommandevent) for the full message shapes and [`examples.md#a-full-invoke-event-sequence`](examples.md#a-full-invoke-event-sequence) for a worked sequence.

`Invoke` dispatches through the plan/apply gate exactly like [`tool/protocol.md#invoke`](../tool/protocol.md#invoke) — this section does not restate that RPC's semantics (streaming shape, cancellation, `output_chunk` ordering, `exit_status` rules, best-effort partial-mutation reporting on cancellation) since they apply here verbatim, with `SlashCommandCall`/`SlashCommandEvent` standing in for `ToolCall`/`ToolEvent`. The one difference: `tool/protocol.md#invoke`'s strict `output_schema` enforcement rule has no analogue here, since `SlashCommandSpec` declares no `output_schema` (see [`GetCapabilities`](#getcapabilities) above) — the kernel accepts a `SlashCommandEvent.result.payload` without a schema to validate it against.

A `SlashCommandCall` produces a `pluggableharness.plan.v1.PlanItem` with `producer_category == CATEGORY_SLASHCOMMAND`, exactly parallel to how a `ToolCall` produces one with `CATEGORY_TOOL` — see [`agent-loop/plan-apply-gate.md#plan-construction-and-policy-evaluation`](../agent-loop/plan-apply-gate.md#plan-construction-and-policy-evaluation). Both `producer_category` values flow through one shared `Plan`/`PlanItem` type and one policy evaluation path; a `TOOL_KIND_RESOURCE` command is gated exactly as a `TOOL_KIND_RESOURCE` tool call is, and a `TOOL_KIND_DATA_SOURCE`/`TOOL_KIND_INTERACTIVE` command follows the same non-interactive policy precheck lane a `data_source`/`interactive` tool call uses, per [`tool/protocol.md#kind-interactive`](../tool/protocol.md#kind-interactive).

`SlashCommandCall.call_context` MUST be set by the kernel on every `Invoke` call, identically to `ToolCall.call_context` — see [`data-types.md#slashcommandcall--slashcommandevent`](data-types.md#slashcommandcall--slashcommandevent).

## Render

Same optionality and reasoning as [`tool/protocol.md#render`](../tool/protocol.md#render) — returning the `RenderTree` formally defined in [`frontend/render-tree.md`](../frontend/render-tree.md). If not implemented, the kernel falls back to its generic default (pretty-printed JSON payload). `RenderRequest.schema_version` names which version of the plugin's own emitted-payload schema `payload` was written under, per [`frontend/render-tree.md#schema-versioning`](../frontend/render-tree.md#schema-versioning).

## Preview

`Preview` returns a dry-run, human-readable description of what `Invoke(call)` *would* do, without doing it — same contract as [`tool/protocol.md#preview`](../tool/protocol.md#preview). Request: a `PreviewRequest` wrapping the same `SlashCommandCall` shape `Invoke` takes; MUST NOT actually be executed by the plugin. Response: a `PreviewResponse` wrapping a `RenderTree` (the same [`frontend/render-tree.md`](../frontend/render-tree.md) type `Render` returns).

- MAY be implemented. A kernel MUST tolerate its absence and fall back to showing the call's raw `arguments` in the plan/apply gate's permission UI.
- MUST NOT mutate anything and MUST be side-effect-free, unconditionally regardless of the call's declared `kind` — the same guarantee [`tool/protocol.md#preview`](../tool/protocol.md#preview) makes. A plugin that cannot produce a preview without performing (part of) the operation MUST NOT implement `Preview` for that command.
- Exists specifically to feed `pluggableharness.plan.v1.PlanItem.preview` for a `producer_category == CATEGORY_SLASHCOMMAND` item, the same dry-run-feeds-the-plan-item mechanism [`tool/protocol.md#preview`](../tool/protocol.md#preview) describes for `CATEGORY_TOOL` — see [`agent-loop/plan-apply-gate.md#preview-flow`](../agent-loop/plan-apply-gate.md#preview-flow). `PlanItem.preview` and `PreviewResponse.preview` are pinned to the exact same `pluggableharness.render.v1.RenderTree` type by design, so the kernel can store a `Preview` call's output directly as a plan item's preview without any conversion, regardless of which of the two categories produced it.

## Describe

`Describe` reports this plugin build's own identity: request is empty (`DescribeRequest {}`), response is a `DescribeResponse` wrapping a single `pluggableharness.common.v1.ProducerRef producer`. MUST be implemented — every one of the seven category protocols carries this RPC.

This exists for the [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry) `dev_overrides` case: a plugin resolved via `dev_overrides` has no `provider "<name>" { ... }` lock-file entry for the kernel to read `{name, version, source, category, protocol_version}` from. `Describe` lets the kernel obtain that same identity directly from the running process instead, at connection time.
