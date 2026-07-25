# internal/retrypolicy — agent notes

- **Pure domain, no instrumentation.** This package is I/O-free and deterministic; it MUST NOT import `log/slog` or `internal/telemetry`. Logging and tracing are the caller's responsibility — they log/span around the result of `Classify()` or `Delay()`, never inside the package. This is enforced by `logging-telemetry.md`'s pure-domain exemption.

- **Jitter is caller-supplied for purity.** `Delay()` takes jitter as a function argument (caller provides a float64 in [0, 1)) rather than sourcing it internally, so the function stays deterministic and testable. Production code supplies randomness via `math/rand.Float64()` or equivalent; tests pin jitter to 0.0 and 0.999 for assertion against exact expected values.

- **Backoff formula is exact — no approximation.** The exponential backoff formula `delay = base_delay * (backoff_factor ^ (attempt - 1)) * (0.5 + 0.5 * jitter)` is implemented with floating-point arithmetic and then converted to a `time.Duration`. Tests verify the output for fixed jitter values (0.0, 0.999) at attempts 1-5 against the exact formula — not a tolerance-based comparison (which would hide precision bugs). This matters because cost and budget computations depend on accurate retry delay estimates.

- **`retry_after_seconds` MUST override the backoff entirely** — when the provider supplies an explicit `retry_after_seconds` value, it takes precedence without any application of backoff or jitter. This is encoded in `Delay(s, attempt, retryAfter, jitter)`: if `retryAfter` is non-nil, it is returned verbatim.

- **Attempt is 1-indexed.** The first retry is `attempt=1` (not 0), so the formula becomes `base_delay * (backoff_factor ^ 0)` for the first retry, `base_delay * (backoff_factor ^ 1)` for the second, etc.

- **Negative attempt is treated as 1.** If a caller passes `attempt < 1`, the code clamps it to 1 for safe computation, rather than panicking or returning zero. Tests verify this edge case.

- **`SessionMaxRetries` is a value, not enforced here.** `Settings.SessionMaxRetries` is carried by the type but the kernel caller is responsible for tracking and enforcing it separately from `MaxRetries`. This split exists because the kernel needs both caps: one on the per-attempt retry loop (a single model call fails 5 times → give up), and one on the entire session (every model call in the session combined hits a cap, separate from per-call caps). The spec requires both be tracked independently; this package provides the data types but not the enforcement.

- **Classify defaults to ReactionFail for unknown/unspecified.** The conservative default for any unrecognized error category (including the zero/unspecified value) is to treat it as a non-retryable error, not to guess a retry policy.
