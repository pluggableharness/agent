# internal/kernelcallback

The composed `kernelv1.KernelCallbackServiceServer` — the twelve-method
plugin-to-kernel callback service (`RunSession`, `CountTokens`, `Emit`,
`Log`, `ExportSpans`, `RecordMetrics`, `GetTelemetryConfig`, `GetConfig`,
`Publish`, `Subscribe`, `ReadEvents`, `GetSession`) described in
`specifications/kernel-callbacks.md`. Every plugin subprocess, regardless
of category, is handed a client connection to this service at handshake
time; `Server` here is the kernel-side implementation that connection
talks to.

## What this package does

- **`Log`** (`server.go`) delegates to `internal/log.Server`, which
  implements the full RPC (batch entry validation, level translation,
  session/producer attribution). `internal/log` is unchanged by this
  package.
- **`ExportSpans`/`RecordMetrics`/`GetTelemetryConfig`** (`telemetry.go`)
  relay a plugin's spans to the operator's collector via
  `internal/telemetryrelay`, record a plugin's metric observations against
  kernel-owned dynamic instruments via `internal/telemetry.RecordDynamicMetric`,
  and report the operator's configured tracing/metrics/logs signal state —
  see `specifications/observability.md` for why spans relay transparently
  while metrics deliberately don't.
- **`GetConfig`** (`config.go`) returns the calling plugin's own
  already-decoded `agent.hcl` configuration, fixed on the `Server` at
  construction.
- **`Publish`/`Subscribe`** (`eventbus.go`) bridge to `internal/eventbus`:
  `Publish` constructs a server-derived topic and republishes onto it;
  `Subscribe` is a server-streaming RPC layering a per-stream backpressure
  bound on top of `internal/eventbus`'s own unbounded, never-drop
  contract — see `specifications/event-bus.md#backpressure` for why that
  bound exists at this layer and not in `internal/eventbus` itself.
- Stubs `RunSession`, `CountTokens`, `Emit`, `ReadEvents`, and `GetSession`,
  each returning `codes.Unimplemented`. `RunSession`/`CountTokens` await
  packages that don't exist yet (`agent-loop.md` §7, `kernel-callbacks.md`
  §2/§3). `Emit`/`ReadEvents`/`GetSession` are different: their data paths
  already exist (`internal/statebackend`), but nothing in this codebase
  yet tracks which session(s) a given plugin instance is authorized to
  touch — implementing the data read without that check would be silently
  insecure, not just incomplete, so they stay honest stubs. Embedding
  `kernelv1.UnimplementedKernelCallbackServiceServer` would already give
  `codes.Unimplemented` for free, but this package defines its own stub
  methods with package-specific error messages.

## Every dependency is per-instance, not per-call

`kernel-callbacks.md` requires producer attribution to be server-derived:
a property of which plugin's broker connection a call arrived on,
established once at handshake, never a field the calling plugin supplies
on the request. This package expresses that by binding one `Server`
instance to exactly one plugin's dependencies at construction time
(`NewServer(Config)`) — not just `Producer`, but also `Telemetry`,
`TelemetryRelay`, `Bus`, and `ResolvedConfig`. Every RPC that instance
serves uses those same fixed values. There is no shared server instance
juggling multiple plugins' identities and no interceptor threading
identity onto the context from outside; the identity and every other
dependency live in the `Server` value itself.

## How this fits in

A follow-up task wires `Server` onto the plugin-runtime's callback broker
(the `hashicorp/go-plugin` bidirectional connection handed to each launched
plugin subprocess) as part of real plugin launch — today only
`internal/pluginruntime`'s integration test fixture constructs one, as a
stand-in for that future caller.
