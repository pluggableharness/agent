# internal/log — agent notes

- **No driver pattern, deliberately.** `go-layout.md`'s driver pattern is
  for genuinely pluggable backends; log output destination is already
  abstracted by Go's own `slog.Handler` interface, so a parallel internal
  driver layer would be redundant indirection. Don't "fix" this by adding
  `drivers/text/`, `drivers/json/`, etc.
- **Producer attribution now lives in `internal/producer`, not here.**
  `WithProducer`/`ProducerFromContext` used to be defined locally in
  `handler.go` as a temporary home; they've been extracted to
  `internal/producer` (renamed `producer.WithProducer`/`producer.FromContext`
  there — no stutter) now that a third consumer needs the identical
  "producer identity is server-derived, never client-supplied" mechanism
  (`kernel-callbacks.md` §4/§5). `internal/log` imports `internal/producer`
  like any other consumer — don't re-add a local copy of this pair here.
- **`Server` intentionally does not implement the full
  `KernelCallbackServiceServer` interface.** It only has a `Log` method
  with a matching signature. Don't embed
  `kernelv1.UnimplementedKernelCallbackServiceServer` here to make it look
  complete — that composition is `internal/kernelcallback`, which wires
  `RunSession`/`CountTokens`/`Emit`/`Log` together and delegates its own
  `Log` method straight through to `Server.Log` here.
- **`LevelTrace`/`LevelFatal` use range comparisons, not exact-value
  checks**, in `levelName` (`level.go`) — this generalizes correctly if a
  caller ever constructs a custom level between the standard ones, not just
  the two named constants.
- **`FATAL` is a severity label only.** `Server.Log` must never treat it as
  a signal to terminate anything — that's an explicit MUST NOT in
  `kernel-callbacks.md` §5. Crash detection stays on the ordinary
  process-crash path.
- **`Server.Log`'s own instrumentation is WARN-on-invalid-entry only — no
  entry-level `DEBUG` log.** `internal/CLAUDE.md`'s gRPC-handler rule
  normally wants `DEBUG` on entry too, but `Log` is also the sink that
  forwarded plugin log entries flow through via this same
  `slog.Handler` — an entry-level kernel-native log line on top of that
  would double log volume for every forwarded plugin log line. The two
  plugin-caused rejection paths (`entry == nil`; `RecordFromEntry`
  returning a malformed-entry error) each emit exactly one
  `WarnContext` call before returning the `codes.InvalidArgument`
  status, with `producer_category`/`producer_name`/`producer_version`
  attached when `ProducerFromContext` finds one on the call's context.
  This is a deliberate log-and-return: it's the gRPC-handler exception
  to `go-style.md`'s "error or log it, never both" — the status crosses
  the wire to the remote plugin caller, which never observes the WARN,
  so there's no in-process double-log.
- **The internal `Handler().Handle(ctx, record)` error path stays
  unlogged, on purpose.** Unlike the two rejection paths above, this is
  not a plugin-caused failure — it's this package's own configured
  `slog.Handler` failing. Logging it through the same `Handler` that
  just failed is self-defeating, and this package has no second logger
  to fall back to; the returned `codes.Internal` status is the only
  signal. Don't "fix" this into a WARN/ERROR call.
- **No span in `Server.Log`, for now.** `internal/CLAUDE.md` wants gRPC
  handlers span-wrapped, but that wiring belongs to the transport-level
  `ServerHandler()` gRPC stats-handler (`internal/telemetry/grpchooks.go`)
  once the kernel-callback server exists to install it — this package
  does not hand-roll its own `tracer.Start` call in the meantime.
