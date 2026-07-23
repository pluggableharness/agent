# internal/telemetry/drivers/fake — agent notes

- **Spans only appear in `Spans.GetSpans()` after a flush.**
  `sdktrace.WithBatcher` means spans queue in a batch processor; call
  `Provider.ForceFlush(ctx)` before asserting, not just `EndSpan`/`span.End()`.
  A test that reads `Spans.GetSpans()` and gets an empty slice almost
  always means the flush step was skipped, not that the span was never
  created.
- **`Metrics` is a `ManualReader` — call `Collect` directly**, don't wait
  for a periodic export; there is no periodic export configured for this
  backend.
- **This package has no configuration.** `New()` takes no arguments on
  purpose — don't add a `cfg` parameter "for consistency" with the other
  drivers; there's nothing for a test double to configure.
