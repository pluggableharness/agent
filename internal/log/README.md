# internal/log

The kernel side of the plugin-to-kernel `Log` callback
(`specifications/kernel-callbacks.md` §5). A plugin calls `Log` over the
`KernelCallbackService` so its own log output reaches the kernel's
centralized `log/slog` logging instead of vanishing into an unread
subprocess stderr — `plugin-runtime.md` has no stderr-capture fallback, so
this RPC is the only intended channel.

## What this package does

- Extends `log/slog`'s four built-in levels with `LevelTrace` (below
  `Debug`) and `LevelFatal` (above `Error`), matching the wire protocol's
  six-level `LogLevel` enum (`level.go`).
- Converts a wire `LogEntry` into a `slog.Record`, validating the three
  MUST fields (level, message, time) and rejecting anything missing them
  rather than filling in defaults (`translate.go`).
- Implements the `Log` RPC method itself (`handler.go`) — not the full
  `KernelCallbackServiceServer` interface. `RunSession`, `CountTokens`, and
  `Emit` belong to other packages that don't exist yet; a future composed
  server will delegate its `Log` method straight to `Server.Log` here.

## How it fits in

This is the first hand-written Go package in the repo — it set the house
style (`doc.go` conventions, error-sentinel naming, context-key pattern)
that every later `internal/` package follows.
