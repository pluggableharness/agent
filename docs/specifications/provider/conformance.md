# Model provider — conformance

## Error taxonomy

A plugin MUST classify every failure into one of the following, and MUST NOT collapse them into a single generic error — the kernel's routing/fallback/retry behavior depends on telling these apart. (An undifferentiated "API error" makes it impossible to tell a context-overflow from a transient overload without reading raw transcripts — this requirement exists to prevent exactly that ambiguity.)

| Category | Meaning | Kernel's expected reaction |
|---|---|---|
| `context_length_exceeded` | Request (or accumulated conversation) exceeds the model's context window | MUST NOT blindly retry as-is; shrink context, drop history, or fail the turn with a clear message — never silently loop |
| `rate_limited` | Vendor-side rate limit hit | Retry with backoff; honor `retry_after_seconds` if the plugin can supply it |
| `overloaded` | Transient vendor unavailability (5xx-equivalent) | Retry with backoff; candidate for capability-aware fallback chain |
| `auth_error` | Bad/expired/missing credentials | MUST NOT retry or silently fall back; surface to a human |
| `invalid_request` | Malformed request — almost always a kernel/adapter bug | MUST NOT retry as-is; log with full request shape for debugging |
| `content_filtered` | Vendor refused/filtered content | Surface distinctly from a generic failure — policy/UX may want to handle this differently |
| `unknown` | Anything else | MUST include the raw vendor error message/code for debugging; treat as non-retryable by default |

`ProviderError` MUST include: `category` (above), `message` (human-readable), `retryable` (bool), and SHOULD include `retry_after_seconds` and the raw vendor-provided error code/body for debugging.

On the wire, each category maps to a `grpc/codes.Code`: `context_length_exceeded` → `ResourceExhausted`, `rate_limited` → `ResourceExhausted` with structured detail, `overloaded` → `Unavailable`, `auth_error` → `Unauthenticated`, `invalid_request` → `InvalidArgument`, `content_filtered` → `FailedPrecondition`, cancellation → `Canceled` — never an application error, `unknown`/unmapped → `Internal`, never `Unknown`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `text` content, both directions | MUST | baseline |
| Streaming RPC shape | MUST | see [`README.md`](README.md#transport--lifecycle) / [`protocol.md`](protocol.md#streamcompletion) — applies even to non-streaming backends |
| `GetCapabilities` / `Configure` / `StreamCompletion` RPCs | MUST | the whole protocol surface |
| Structured error taxonomy (above) | MUST | |
| `tool_use` / `tool_result` | MUST, if any served model has `supports_tool_use = true` | |
| `image` (vision) | MUST support where `supports_vision = true`; MUST reject cleanly where `false` | |
| Extended thinking/reasoning | MAY, capability-gated via `ThinkingSpec` | declare `mode` precisely, don't collapse to a bool |
| Prompt caching | MAY, capability-gated via `CachingSpec` | declare `mode` (explicit vs. implicit) |
| Parallel tool calls in one turn | SHOULD declare via `supports_parallel_tool_calls` | kernel serializes calls if absent/false |
| `Render` | MAY | generic fallback exists |
| `CountTokens` | SHOULD | kernel falls back to [`kernel-callbacks.md`](../kernel-callbacks.md#the-fallback-heuristic)'s heuristic when absent, treated as a last resort |
| `ThinkingSpec.default` | MUST when `mode != none` | [`data-types.md`](data-types.md#thinkingspec) |
| `CachingSpec.keepalive_supported` | MUST (field); actual keepalive loop MAY | [`data-types.md`](data-types.md#cachingspec) |
| `Pricing.tiers`, time-bounded/tiered rates | MUST | [`data-types.md`](data-types.md#pricing) — exactly one tier MUST match any given timestamp |
| `Pricing` on every `ModelSpec` | MUST | required even for `free: true` models |
| Kernel computes + persists `cost_usd` at usage-event time, not lazily at query time | MUST | [`protocol.md`](protocol.md#cost-computation) |
| Realtime/voice (WebSocket-style APIs) | MUST NOT — out of scope for v1 | likely a distinct wire protocol per vendor; treat as a future, separate plugin surface, not a mode of this one |
| Embeddings | MUST NOT — out of scope for v1 | a separate future concern (likely relevant to memory providers, not modeled here) |

## Open questions

- Whether `supports_parallel_tool_calls` needs a per-request override (some vendors may allow disabling parallel calls per-call even when generally supported).
- Whether `ThinkingSpec.budget_range` needs a per-model default separate from the overall min/max (several vendors default to a specific level like `medium`/`HIGH` rather than "off").
- Retry/backoff policy specifics (exponential backoff parameters) — likely belongs in the kernel's routing logic rather than this protocol, but needs to be decided somewhere; see [`configuration/blocks-reference.md`](../configuration/blocks-reference.md)'s `settings{}` retry defaults for the current kernel-side values.
- Whether `content_filtered` needs sub-categories (input filtered vs. output filtered) — there isn't enough vendor detail yet to decide.
- `Pricing.currency` is declared as a string but v1 only ever acts on `"USD"` — no conversion mechanism, no mixed-currency cost aggregation across providers with different currencies. Fine while vendors generally price in USD; would need real design work the moment that stops being true.
- Whether a plugin author republishing overlapping `Pricing.tiers` should be a hard capability-load-time rejection (as currently specified) or a softer warning-plus-last-write-wins — the former was chosen for consistency with this project's general "ambiguity is an error" posture, but hasn't been stress-tested against how often a plugin author might get tier boundaries slightly wrong.
