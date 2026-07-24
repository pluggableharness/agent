# Tool provider — conformance

## Error taxonomy

Distinct from [`model/conformance.md#error-taxonomy`](../model/conformance.md#error-taxonomy)'s `ModelError` — a tool's failure modes are a different domain (no `rate_limited`/`context_length_exceeded`, which are model-vendor concepts) — but follows the same shape and the same non-negotiable principle: a plugin MUST classify every failure, MUST NOT collapse them into one generic error, for the same reason the model-provider protocol cites (undifferentiated errors are undebuggable after the fact).

```protobuf
ToolError {
  category    ToolErrorCategory  // MUST
  message     string             // MUST — human-readable
  retryable   bool               // MUST
  details     JSON               // MAY — provider-specific structured detail
}

ToolErrorCategory = enum {
  invalid_arguments      // input failed input_schema validation
  not_found              // target of the operation doesn't exist (path, URL, symbol, ...)
  permission_denied      // OS/policy denied the underlying operation
  execution_failed       // the operation ran but failed on its own terms (non-zero exit,
                          // compiler error, HTTP 4xx/5xx) — not a plugin bug
  timeout                // exceeded a plugin- or kernel-enforced deadline
  concurrency_conflict   // provider detected a conflicting concurrent call it could not
                          // serialize itself (see data-types.md#concurrencyspec) — signals
                          // the kernel to retry serialized
  cancelled              // stream was cancelled per README.md#transport--lifecycle — not
                          // "an error" in the failure sense; MUST be distinguished from
                          // other categories so the kernel doesn't surface it to the model
                          // as a tool failure when the whole turn is being aborted anyway
  process_crashed        // the plugin subprocess itself died mid-Invoke (transport error,
                          // not a graceful error event the plugin chose to emit) — MUST be
                          // kernel-synthesized, never something a plugin emits about itself
  unknown                // anything else; MUST include the raw underlying error in `details`
}
```

`process_crashed` exists because a tool subprocess dying mid-call (a segfault, an OOM kill, a panic that takes the process down) is a distinct, detectable failure mode from any graceful `error` event a plugin chooses to emit about its own operation — collapsing it into `unknown` would hide from the kernel (and from policy/circuit-breaker logic) that the *plugin itself* misbehaved, as opposed to the operation it was asked to perform. Because the plugin process is, by definition, no longer running to emit this event itself, the kernel MUST synthesize `process_crashed` from the transport-level failure (a broken gRPC connection, a `hashicorp/go-plugin` health-check failure) — a plugin author never constructs one directly.

Kernel's expected reaction per category:

| Category | Reaction |
|---|---|
| `invalid_arguments` | MUST NOT retry as-is; feed back to the model as a `tool_result` error so it can correct arguments |
| `not_found` | Surface to the model; no retry |
| `permission_denied` | Surface to the model; MUST NOT silently retry with escalated privilege |
| `execution_failed` | Ordinary `tool_result` content, not a protocol-level failure — this is the common case for e.g. a failing test run |
| `timeout` | Cancel per [`README.md`](README.md#transport--lifecycle); retryable at kernel's discretion |
| `concurrency_conflict` | Retry serialized against the same key (see [`data-types.md#concurrencyspec`](data-types.md#concurrencyspec)) |
| `cancelled` | Not surfaced as a model-visible failure unless the turn itself is being aborted |
| `process_crashed` | Surfaced to the model as an ordinary `tool_result` error (same observe-and-adapt principle as a denial, [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)); SHOULD trip the same circuit breaker as repeated denials if it recurs |
| `unknown` | Non-retryable by default; log `details` for debugging |

On the wire, `process_crashed` maps to `codes.Unavailable` — the same code used for a transient, retriable unavailability elsewhere in the system, since a crashed subprocess is exactly that from the kernel's point of view: the service became unavailable, not that the request itself was invalid.

### The `idempotent` / retry interaction

`ToolSchema.idempotent` (per [`protocol.md#getschema`](protocol.md#getschema)) is the gate on top of the category-reaction table above for exactly one row: a `retryable` `ToolError` returned for a `TOOL_KIND_RESOURCE` operation. The kernel MAY auto-retry such a failure — without surfacing it to the model as a failed call first — only when that operation's `ToolSchema.idempotent` is `true`; when it's `false` (or unset — proto3's zero value for `bool` is `false`, so an operation MUST explicitly declare `idempotent: true` to opt in, never rely on an implicit default), the kernel MUST treat the failure as terminal for this attempt and surface it, exactly as the category-reaction table already prescribes. `TOOL_KIND_DATA_SOURCE` operations are exempt from this gate entirely — they're implicitly safe to retry regardless of `idempotent`, since by definition they cannot mutate anything. `TOOL_KIND_INTERACTIVE` calls are never auto-retried (per [`protocol.md#kind-interactive`](protocol.md#kind-interactive), a human's answer isn't something a kernel can safely redo unprompted).

This interacts with, but is distinct from, `concurrency_conflict`'s existing "retry serialized against the same key" reaction: that retry is about serialization ordering, not about whether re-running the operation is safe at all, so it applies independent of `idempotent`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `GetSchema` / `Configure` / `Invoke` RPCs | MUST | the whole protocol surface |
| `Describe` RPC | MUST | [`protocol.md#describe`](protocol.md#describe); needed for `dev_overrides` plugin identity per [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry) |
| Streaming RPC shape for `Invoke` | MUST | see [`README.md`](README.md#transport--lifecycle) / [`protocol.md#invoke`](protocol.md#invoke) — applies even to non-streaming operations |
| `ToolCall.call_context` | MUST be set by the kernel, every `Invoke` call | [`protocol.md#invoke`](protocol.md#invoke); `working_directory` is what makes process-backed operations usable at all |
| `input_schema`/`output_schema` in the common JSON-Schema subset | MUST | [`model/data-types.md#tool-schema`](../model/data-types.md#tool-schema) |
| `kind` (resource / data_source / interactive) | MUST, per operation | drives the plan/apply gate; [`protocol.md#kind-interactive`](protocol.md#kind-interactive) |
| `risk` classification | MUST, per operation | see [`data-types.md#riskclass`](data-types.md#riskclass); `read_only` for `data_source` and `interactive` alike |
| `ConcurrencySpec.safe` | MUST, per operation except `interactive` | absent/unset MUST be treated as `false`; MUST NOT be declared for `interactive` |
| `ConcurrencySpec.key_fields` | MAY, per operation | only meaningful under `safe: true` |
| `default_timeout` | SHOULD, per operation | [`protocol.md#getschema`](protocol.md#getschema); absent means the kernel's global default applies |
| `idempotent` | MUST, per operation | [`protocol.md#getschema`](protocol.md#getschema); gates kernel auto-retry, see above |
| `supported_hook_points` | MAY | [`protocol.md#getschema`](protocol.md#getschema); empty means this provider subscribes no `hook{}` blocks |
| `exit_status` event | MUST for process-backed (exec-family) operations; MUST NOT otherwise | |
| `output_chunk` / `progress` / `partial_result` events | MAY | only for operations with `streaming: true` |
| Structured `ToolError` taxonomy, including `process_crashed` | MUST | |
| Strict `output_schema` enforcement | MUST | [`protocol.md#invoke`](protocol.md#invoke) |
| Best-effort partial-mutation report on cancellation | MUST, for `resource` operations | see [`protocol.md#invoke`](protocol.md#invoke) |
| `Render` | MAY | generic fallback exists; `RenderRequest.schema_version` per [`../frontend/render-tree.md#schema-versioning`](../frontend/render-tree.md#schema-versioning) |
| `Preview` | MAY | [`protocol.md#preview`](protocol.md#preview); kernel MUST fall back to raw `arguments` when absent; MUST NOT mutate anything when implemented |

## Open questions

- OS-level sandboxing (bubblewrap/Seatbelt/Landlock — increasingly common in production coding harnesses, not just research tools) isn't modeled by this protocol at all; it's presumably a `Configure`-time concern per provider (see [`protocol.md#configure`](protocol.md#configure)) or a kernel-level policy applied uniformly regardless of provider, but which is genuinely unresolved.
- Whether `key_fields` (see [`data-types.md#concurrencyspec`](data-types.md#concurrencyspec)) needs to support derived/composite keys beyond "the literal value of named input fields" — e.g. a filesystem provider wanting to serialize on a *resolved absolute path* rather than the raw (possibly relative, possibly symlinked) `path` argument as given.
