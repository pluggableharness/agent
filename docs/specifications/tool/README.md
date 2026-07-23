# Tool provider protocol

Covers the **tool provider** category — file I/O, shell execution, search, web access, task tracking, sub-agent spawning, and similar operations (Claude Code's "tools," Terraform's closest analog would be a provider's resources and data sources combined). Sibling to [`provider/`](../provider/README.md) (model provider) in this system's Terraform-derived vocabulary; the other plugin categories ([`context/`](../context/README.md), [`memory/`](../memory/README.md), [`frontend/`](../frontend/README.md)) follow the same shape this and `provider/` establish.

Tool sets converge strongly across agentic coding harnesses on a common core of operations. Where that convergence exists, this category treats it as evidence for what belongs in the reference catalog ([`reference-catalog.md`](reference-catalog.md)). Where genuine divergence exists (edit mechanisms, risk/approval models, concurrency handling), this category calls that out explicitly rather than picking one approach and presenting it as settled.

This category depends directly on [`provider/`](../provider/README.md): the common JSON-Schema subset tool authors write `input_schema`/`output_schema` within is the same one [`provider/data-types.md#tool-schema`](../provider/data-types.md#tool-schema) defines for model tool-calling declarations (one wire type, `pluggableharness.agent.schema.v1.Schema`, shared by both categories), and `Invoke`'s server-streaming-plus-cancellation shape reuses [`provider/README.md`](../provider/README.md#transport--lifecycle)'s `StreamCompletion` pattern verbatim. See [`architecture.md`](../architecture.md) for the surrounding system (plan/apply gate, hook dispatch, Emit→Render→Paint, state backend) — this directory only covers the tool-provider RPC surface, data types, and reference catalog in detail.

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). Standard handshake (magic cookie, protocol version negotiation) applies uniformly across all six provider categories and isn't repeated per category.

A tool provider plugin exposes three RPCs: `GetSchema`, `Configure`, `Invoke`. It MAY additionally implement `Render` (see [`protocol.md#render`](protocol.md#render)).

**`Invoke` is server-streaming**, the same shape [`provider/README.md`](../provider/README.md#transport--lifecycle) specifies for `StreamCompletion` and for the identical reason: a tool like `exec` needs to stream live stdout/stderr rather than blocking until completion, and none of the underlying primitives (process exec, HTTP fetch, file I/O) need mid-call client input on the same call. **Cancellation follows the model-provider pattern exactly**: the kernel cancels/closes the gRPC stream; it is not a distinct RPC or a sentinel event the plugin must invent. Plugin authors MUST treat stream cancellation as a normal, expected event — kill the child process, release file handles/sockets, discard buffers — never as an error condition. A tool provider and a model provider are both "long-running, streaming, cancellable" from the kernel's point of view, and giving them different cancellation mechanics would be an unforced inconsistency.

## Category structure

- [`protocol.md`](protocol.md) — the three/four RPCs: `GetSchema` (including the `kind: interactive` sub-classification), `Configure`, `Invoke`, `Render`.
- [`data-types.md`](data-types.md) — `ToolSchema`, `RiskClass`, the `ToolCall`/`ToolEvent`/`ToolResult` shapes, and `ConcurrencySpec`.
- [`reference-catalog.md`](reference-catalog.md) — the first-party reference tool set this protocol defines, and the genuinely ambiguous classification calls (`bash`, `web_fetch`) worth calling out by name.
- [`examples.md`](examples.md) — a worked `agent.hcl` provider block, the real proto wire definitions, and a full `Invoke` event sequence.
- [`conformance.md`](conformance.md) — the error taxonomy and the MUST/SHOULD/MAY summary matrix, plus genuinely open questions.
