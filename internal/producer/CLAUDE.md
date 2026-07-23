# internal/producer — agent notes

- **`FromContext`, not `ProducerFromContext`.** The original home
  (`internal/log/handler.go`) named it `ProducerFromContext` because it
  sat in a package that wasn't itself called `producer`. Now that it lives
  in package `producer`, the `producer.ProducerFromContext` spelling would
  be package-name stutter (`.claude/rules/go-style.md`) — it was
  deliberately renamed to `producer.FromContext` during the extraction.
  Don't "restore" the old name for consistency with call sites elsewhere;
  update the call sites instead.
- **This package is intentionally minimal and has no growth plan.** Two
  functions, one unexported context-key type, zero dependents beyond
  passing a `*commonv1.ProducerRef` through a `context.Context`. Resist
  adding validation, defaulting, or a second context-carried type here —
  if a future need doesn't fit "carry a producer ref across a context
  boundary," it belongs in a different package.
- **No `log/slog` or `internal/telemetry` import, ever.** This package is
  trivially I/O-free (two map-like `context.Value` calls), so it doesn't
  even reach the pure-domain exception in `internal/CLAUDE.md` — it simply
  has nothing to instrument.
