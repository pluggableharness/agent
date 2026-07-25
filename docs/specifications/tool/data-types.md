# Tool provider — data types

## `ToolSchema`

See [`protocol.md#getschema`](protocol.md#getschema) for the full shape; this section covers the two classification fields, `RiskClass` and `ConcurrencySpec`, plus the `terminates_turn` declaration, in detail.

## `RiskClass`

```protobuf
RiskClass = enum {
  read_only   // data_source and interactive alike; inherently unable to mutate
              // anything the plugin controls
  low         // resource with narrow, easily-reversible blast radius (e.g. write
              // to a scratch path)
  moderate    // resource with real but bounded blast radius (e.g. edit a tracked
              // source file)
  high        // resource with broad or hard-to-predict blast radius (e.g. arbitrary
              // shell exec)
  critical    // resource capable of irreversible or wide-blast-radius action (e.g.
              // `rm -rf`, force-push, spawning a sub-agent with further unattended
              // write access)
}
```

`risk` is orthogonal to `kind`: `kind` determines whether the plan/apply gate applies at all, `risk` determines how significant the gated (or inherently ungated) action is. A `resource` MUST declare one of `low`/`moderate`/`high`/`critical` — never `read_only`. See [`reference-catalog.md`](reference-catalog.md) for how the reference tool set assigns `risk` in practice, and [`configuration/policy-dsl.md`](../configuration/policy-dsl.md#match-schema) for how `agent.hcl` policy matches on a tool's declared `RiskClass` alongside `kind`/`provider`/`tool_name`.

On the wire, `RiskClass` is declared as an enum with an explicit `RISK_CLASS_UNSPECIFIED = 0` zero value — a caller that forgets to set the field gets a named "unspecified" state, never a silently-valid-looking value. The same is true of `ToolKind` (`TOOL_KIND_UNSPECIFIED = 0`) and `ToolErrorCategory` (`TOOL_ERROR_CATEGORY_UNSPECIFIED = 0`) below.

## `ToolCall` / `ToolEvent` / `ToolResult`

```protobuf
ToolCall {
  id             string   // MUST — kernel-assigned, echoed in every emitted event for correlation
  tool_name      string   // MUST — matches a ToolSchema.name from this provider's GetSchema
  arguments      JSON     // MUST — already-parsed JSON conforming to input_schema; per
                          // model/data-types.md#tool-schema, the kernel's internal ToolCall
                          // representation always stores parsed arguments regardless of which
                          // model-provider adapter produced them
  call_context   CallContext  // MUST be set by the kernel — pluggableharness.common.v1.CallContext.
                          // Carries session_id/turn_id, echoed by the plugin on its own
                          // KernelCallbackService.Emit/Log calls for attribution, and
                          // working_directory — the cwd a process-backed operation (exec/bash,
                          // read_file, ...) MUST resolve a relative-path argument against; without
                          // it those tools have no defined cwd and are unusable. See
                          // protocol.md#invoke.
}

ToolEvent = oneof {
  output_chunk    { stream: enum { stdout, stderr }, data: bytes }
  progress        { message: string, fraction_complete: float? }
  partial_result  { payload: JSON }   // incremental structured output, e.g. search hits as found
  exit_status     { exit_code: int, signal: string? }  // exec-family only; see protocol.md#invoke
  result          ToolResult          // terminal, success
  error           ToolError           // terminal, failure — see conformance.md#error-taxonomy
}

ToolResult {
  payload   JSON   // MUST conform to output_schema
}
```

`output_chunk`, `progress`, and `partial_result` MAY each appear zero or more times before the stream's terminal event; `exit_status` MAY appear at most once. Exactly one of `result`/`error` MUST close the stream. See [`protocol.md#invoke`](protocol.md#invoke) for the full ordering and cancellation semantics.

On the wire, `ToolCall`/`ToolEvent` are wrapped in thin per-RPC envelope messages (`InvokeRequest { call = 1; }`, `InvokeResponse { event = 1; }`), so the rich structure lives on `ToolCall`/`ToolEvent` themselves, independent of the RPC signature — see [`examples.md`](examples.md#the-wire-protocol) for the full message definitions.

## `ConcurrencySpec`

```protobuf
ConcurrencySpec {
  safe        bool     // MUST, except for kind == interactive. false = the kernel MUST NOT
                        // run any other Invoke call against this provider process concurrently
                        // with this one — a coarse, provider-wide lock. true = concurrent Invoke
                        // calls against this provider are generally safe.
  key_fields  []string // MAY, only meaningful when safe == true. Names of input_schema
                        // fields whose value(s) form a serialization key. The kernel
                        // computes key = (provider_name, tool_name, value(key_fields))
                        // and MUST serialize calls sharing an identical key, while still
                        // freely parallelizing calls with distinct keys.
}
```

This resolves a question the surrounding architecture narrative left open: can two resource/data-source calls in one turn run in parallel safely, or does a provider need to declare "not concurrency-safe" (the canonical example: two writes to the same file path)? The two-level declaration above is the answer — a coarse provider-wide lock (`safe: false`) or a finer-grained per-key lock (`safe: true, key_fields: [...]`).

Example: a filesystem provider declares `safe: true, key_fields: ["path"]` on its `edit`/`write` operations. Two concurrent edits to `a.go` and `b.go` run in parallel; two concurrent edits to `a.go` serialize. This resolves the same-path-write problem without forcing a provider-wide lock for unrelated paths.

A provider that does not populate `ConcurrencySpec` at all (e.g. an older plugin version predating this field) MUST be treated by the kernel as `safe: false` — the conservative default. A plugin MUST NOT declare `safe: true` without `key_fields` for any operation where distinct calls could plausibly target the same underlying resource; omitting `key_fields` under `safe: true` asserts "no two calls to this operation can ever conflict," which is a strong claim (true for e.g. `web_search`, false for e.g. `write_file`).

`data_source` operations SHOULD declare `safe: true` with no `key_fields` in the common case (reads generally don't conflict), but this is a per-operation choice, not implied by `kind` — a `data_source` that reads from a provider-internal cache with a bounded writer could still need a key.

`ConcurrencySpec` MUST NOT be declared for a `kind == interactive` operation; if present, the kernel MUST ignore it and enforce sequential execution unconditionally — see [`protocol.md#kind-interactive`](protocol.md#kind-interactive). Whether `key_fields` needs to support derived/composite keys beyond "the literal value of named input fields" (e.g. a filesystem provider wanting to serialize on a resolved absolute path rather than the raw, possibly relative or symlinked, `path` argument) is a genuinely open question — see [`conformance.md#open-questions`](conformance.md#open-questions).

## terminates_turn

```protobuf
ToolSchema {
  ...
  terminates_turn  bool  // MAY. true = the kernel MUST treat this call as an immediate,
                          // successful DoneCheck once the call's post-tool-call hook has
                          // fired. Resource-only. Absent/false = this operation does not
                          // terminate the turn.
}
```

`terminates_turn` is a tool provider's opt-in to the explicit terminal-tool done-detection path described in [`agent-loop/turn-algorithm.md#done-detection`](../agent-loop/turn-algorithm.md#done-detection). When the model calls an operation declaring `terminates_turn: true`, the kernel MUST treat that call as `DoneCheck` success immediately after the call's `post-tool-call` hook fires — independent of whether other `tool_use` blocks were present in the same assistant message, and without waiting for a subsequent no-tool-calls message. The remaining `tool_use` blocks in that message are not a reason to keep looping; the terminal tool's own `result` is the turn's completion report.

`terminates_turn` is orthogonal to `kind`/`risk`/`idempotent` — it says nothing about mutation, blast radius, or retry safety. It MAY be `true` only on a `kind == resource` operation: a turn-terminating call is a deliberate, model-driven state transition, so it goes through the plan/apply gate like any other resource, and a `data_source` or `interactive` operation declaring it is an invalid `ToolSchema` the kernel MUST reject at `GetSchema` time rather than silently honor.

Absent or `false` is the default, exactly as with `idempotent`: proto3's zero value for `bool` is `false`, so an operation MUST explicitly declare `terminates_turn: true` to opt in and MUST NOT rely on an implicit default. Implicit no-tool-calls done detection remains the MUST-support baseline the kernel applies regardless of whether any provider declares this field at all — `terminates_turn` layers on top of it, never replaces it.
