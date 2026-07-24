# internal/pluginruntime

The kernel-side launcher for PluggableHarness Agent's six out-of-process plugin
categories (provider, tool, context, memory, frontend, widget), each a
`hashicorp/go-plugin` subprocess speaking gRPC.

## What this package does

`Launch(ctx, cfg)` runs the whole launch sequence for one plugin
subprocess implementing one category:

1. A no-op-today pre-flight protocol-version check.
2. Building the one-entry go-plugin `PluginSet` for the launch's category.
3. Spawning the subprocess (`exec.CommandContext`) under a minimal,
   explicit environment allowlist — never the kernel's full `os.Environ()`.
4. Constructing the `*plugin.Client`, with gRPC dial options that wire in
   OpenTelemetry propagation and crash-classifying interceptors.
5. Dialing and completing the handshake.
6. The authoritative post-handshake protocol-version gate.
7. Dispensing the category's raw generated service client
   (`modelv1.ModelServiceClient`, `toolv1.ToolServiceClient`, ...).
8. Returning a `*Plugin` wrapping the dispensed client and the plugin's
   producer identity.

Every launched plugin is simultaneously wired with a real, servable
`KernelCallbackService` — the plugin-to-kernel reverse channel described
in `specifications/kernel-callbacks.md` — served over a fixed, well-known
`go-plugin` broker ID (`pkg/common.CallbackBrokerID`), so a plugin can call
back into the kernel (today: `Log`; `RunSession`/`CountTokens`/`Emit` are
tracked `Unimplemented` stubs, see `internal/kernelcallback`) from the
moment it starts.

`(*Plugin).Close(ctx)` performs a graceful shutdown: it gives
`go-plugin`'s own `Kill()` a bounded drain window to finish, then escalates
to a hard subprocess-tree teardown only if that window is exceeded.

## What this package deliberately does not do

- **Resolve a registry source address to a binary path.** `Config.BinaryPath`
  is already-resolved; a future registry-aware launcher does that
  resolution and calls `Launch` with the result — the same shape
  `DevOverrides` already uses today.
- **Construct the `kernelcallback.Server` it serves.** The caller builds
  one per launched plugin (with that plugin's producer identity baked in)
  and passes it in via `Config.Callback`.
- **Implement any plugin-side code**, beyond the minimal integration-test
  fixture under `testdata/plugin/` — this package is the kernel half only.
- **Change any `.proto`.** The callback broker ID is a fixed, out-of-band
  constant (`pkg/common.CallbackBrokerID`) precisely so nothing needed to
  be added at the wire-protocol layer.

## Where this fits

- `pkg/common` — the shared handshake config, protocol version constant,
  callback broker ID, and category→plugin-map-key helper this package (and
  a future plugin-side SDK) both compile against.
- `internal/kernelcallback` — the composed `KernelCallbackServiceServer`
  this package serves on the callback broker.
- `internal/telemetry` — the gRPC stats handlers wired onto both halves of
  the plugin boundary, and the `plugin.launch` span this package's
  `Launch` runs inside.
- `internal/log` — the six-level log vocabulary `hcloglogger.go`'s shim
  translates `go-plugin`'s own diagnostics into.

See `CLAUDE.md` for implementation-level conventions, design decisions,
and gotchas; `specifications/plugin-runtime.md` and
`specifications/kernel-callbacks.md` are the governing specs.
