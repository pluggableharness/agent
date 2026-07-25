# retrypolicy

Classifies model-provider error categories into reaction types and computes exponential-backoff delays.

## Overview

This package implements the kernel's response policy to errors returned by model providers, per `docs/specifications/agent-loop/error-recovery.md#model-provider-errors`. It performs two key operations:

1. **Classify** — maps a model provider's error category to a kernel reaction:
   - `rate_limited` / `overloaded` → `ReactionRetry` (exponential backoff + jitter)
   - `context_length_exceeded` → `ReactionReduceContext` (no blind retry; triggers context reduction)
   - `auth_error` / `invalid_request` → `ReactionFail` (no retry, no fallback)
   - `content_filtered` → `ReactionSurface` (surfaced distinctly to caller)
   - unknown/unspecified → `ReactionFail` (conservative default)

2. **Delay** — computes the backoff duration before a retry attempt, using the formula:
   ```
   delay = base_delay * (backoff_factor ^ (attempt - 1)) * (0.5 + 0.5 * jitter)
   ```
   When the provider supplies `retry_after_seconds`, that duration is honored verbatim, overriding the computed backoff entirely.

## Key invariants

- Per-attempt and per-session retry caps are tracked separately by the caller using `Settings.MaxRetries` (per-attempt) and `Settings.SessionMaxRetries` (session-wide). This package carries both values but only uses `MaxRetries` for delay computation; the caller enforces both caps.
- The package is pure domain logic: deterministic, I/O-free, and carries no mutable state. It is never instrumented with logging or telemetry.
- Jitter is caller-supplied (production code sources it from `math/rand`, tests pin it to fixed values 0.0 or 0.999 for determinism) so `Delay` remains a pure function.

## Canonical defaults

- `base_delay_ms = 500`
- `backoff_factor = 2`
- `max_retries = 5` (per-attempt cap)
- `session_max_retries` — operator-configured via `agent.hcl`, no built-in default

These apply via `internal/config.DefaultRetrySettings` and `FromConfig()`.
