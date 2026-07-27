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

`ModelError` MUST include: `category` (above), `message` (human-readable), `retryable` (bool), and SHOULD include `retry_after_seconds` and the raw vendor-provided error code/body for debugging.

On the wire, each category maps to a `grpc/codes.Code`: `context_length_exceeded` → `ResourceExhausted`, `rate_limited` → `ResourceExhausted` with structured detail, `overloaded` → `Unavailable`, `auth_error` → `Unauthenticated`, `invalid_request` → `InvalidArgument`, `content_filtered` → `FailedPrecondition`, cancellation → `Canceled` — never an application error, `unknown`/unmapped → `Internal`, never `Unknown`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `text` content, both directions | MUST | baseline |
| Streaming RPC shape | MUST | see [`README.md`](README.md#transport--lifecycle) / [`protocol.md`](protocol.md#streamcompletion) — applies even to non-streaming backends |
| `GetCapabilities` / `Configure` / `StreamCompletion` RPCs | MUST | the whole protocol surface |
| `GetCapabilities` makes no per-call network request | MUST | [`protocol.md#getcapabilities`](protocol.md#getcapabilities) — a gateway or locally-served provider resolves its roster once in `Configure` and serves it from memory; a background refresh MUST NOT block the call |
| Credential attribute declared `required` only when every supported deployment needs one | MUST | [`protocol.md#gateway-and-locally-served-providers`](protocol.md#gateway-and-locally-served-providers) — a loopback-served runtime typically has no auth; validate the combination in `Configure` instead |
| `Describe` RPC | MUST | [`protocol.md#describe`](protocol.md#describe) — identity for `dev_overrides` binaries with no lock-file entry |
| Structured error taxonomy (above) | MUST | |
| `Configure` is safely re-callable | MUST | [`protocol.md#configure`](protocol.md#configure) — replaces configured state wholesale; a second call carries the operator's complete intent, so a field absent from it is absent, not inherited |
| `tool_use` / `tool_result` | MUST, if any served model has `supports_tool_use = true` | |
| `image` (vision) | MUST support where `supports_vision = true`; MUST reject cleanly where `false` | |
| `document` | MUST support where `supports_documents = true`; MUST reject cleanly where `false` | [`data-types.md#canonical-message--content-block-schema`](data-types.md#canonical-message--content-block-schema) — mirrors `image`/`supports_vision`'s rule |
| Extended thinking/reasoning | MAY, capability-gated via `ThinkingSpec` | declare each axis it actually accepts, don't collapse to a bool or to one mutually-exclusive mode |
| `ThinkingSpec.effort.default` / `budget.default` | MUST when that control is present | [`data-types.md#thinkingspec`](data-types.md#thinkingspec) — `effort.default` names a level; `budget.default` MAY be omitted, meaning zero reasoning tokens by default |
| `ThinkingSpec.adaptive_by_default` / `disable` | MUST | [`data-types.md#thinkingspec`](data-types.md#thinkingspec) — `disable = conditional` tells the kernel a disable attempt MAY legitimately fail, so such a failure is vendor policy, not an adapter bug |
| `StreamEvent.redacted_thinking` | MUST, for a vendor that emits vendor-encrypted reasoning blocks | [`data-types.md#streamevent`](data-types.md#streamevent) — a whole block, never fragmented; stored and echoed back verbatim or the vendor rejects the whole conversation on a later turn |
| Prompt caching | MAY, capability-gated via `CachingSpec` | declare each axis it actually has; at least one MUST be true when `supported` |
| Cache breakpoints (`StreamCompletionRequest.cache_breakpoints`) | MUST honor where `CachingSpec.explicit_markers`; MUST ignore otherwise | [`protocol.md#cache-breakpoint-placement-policy`](protocol.md#cache-breakpoint-placement-policy) — placement is a kernel decision, never the plugin's. Gating on the axis rather than a mode is what lets a model declaring both caching axes still honor breakpoints |
| `StreamCompletionRequest.provider_options` | MAY consume; kernel MUST pass through untouched | [`data-types.md#provider_options`](data-types.md#provider_options) — vendor knobs the kernel has no semantics for. A value the kernel reads MUST be a typed field instead, never smuggled through here |
| Parallel tool calls in one turn | SHOULD declare via `supports_parallel_tool_calls` | kernel serializes calls if absent/false |
| Tool-choice constraint (`GenerationParams.tool_choice`) | MAY, capability-gated via `ModelSpec.supported_tool_choice_modes` | kernel MUST NOT send a mode absent from the declared list, mirroring `ThinkingSpec` validation |
| `Render` | MAY | generic fallback exists; `RenderRequest.schema_version` MUST be set when implemented |
| `CountTokens` | SHOULD | kernel falls back to [`kernel-callbacks.md`](../kernel-callbacks.md#the-fallback-heuristic)'s heuristic when absent, treated as a last resort; `CountTokensRequest.model_id` MUST be set |
| `CachingSpec.keepalive_supported` | MUST (field); actual keepalive loop MAY | [`data-types.md`](data-types.md#cachingspec) |
| `Pricing.tiers`, time-bounded/tiered/input-size-bounded rates | MUST | [`data-types.md`](data-types.md#pricing) — exactly one tier MUST match any given `(timestamp, input_token_count)` pair |
| `Pricing` on every `ModelSpec` | MUST | required even for `free: true` models |
| Kernel computes + persists `cost_usd` at usage-event time, not lazily at query time | MUST | [`protocol.md`](protocol.md#cost-computation) — includes `reasoning_tokens` billed at the output rate |
| `StreamEvent.stream_start` | SHOULD, when the vendor publishes a request id | [`data-types.md#stream_start-and-vendor-request-correlation`](data-types.md#stream_start-and-vendor-request-correlation) — emitted early, so a failed stream is still correlatable to the vendor's logs |
| `Usage.rate_limits` | SHOULD, when the vendor publishes rate-limit state | [`data-types.md#usagerate_limits`](data-types.md#usagerate_limits) — MUST NOT be synthesized from the adapter's own bookkeeping |
| `Usage.reasoning_tokens` | SHOULD, when the vendor reports it distinctly | [`data-types.md#streamevent`](data-types.md#streamevent) — never double-counted in `output_tokens` |
| `supported_hook_points` | MUST (field, MAY be empty) | [`data-types.md#capabilitiessupported_hook_points`](data-types.md#capabilitiessupported_hook_points) — kernel rejects an unsupported `hook{}` block at config-load time |
| Realtime/voice (WebSocket-style APIs) | MUST NOT — out of scope for v1 | likely a distinct wire protocol per vendor; treat as a future, separate plugin surface, not a mode of this one |
| Embeddings | MUST NOT — out of scope for v1 | a separate future concern (likely relevant to memory providers, not modeled here) |

## Open questions

- Whether `supports_parallel_tool_calls` needs a per-request override (some vendors may allow disabling parallel calls per-call even when generally supported).
- **`PricingTier` assumes one cache rate per model, and real vendors have several.** The tier carries a single `cache_write_per_mtok`/`cache_read_per_mtok` pair, but a model can bill cached tokens at more than one rate depending on *which* caching mechanism serviced the request: Anthropic publishes distinct 5-minute and 1-hour cache-write rates (the 1-hour rate is 2× input against 1.25× for 5-minute), and a Gemini model running both caching axes discounts implicit hits ~75% and explicit hits ~90%. Two consequences follow, and both are currently unresolved rather than solved:
  - There is no way to *select* a TTL. `CacheBreakpoint` carries no TTL field, so an adapter can only ever incur its vendor's default, and `internal/anthropic/catalog` correspondingly quotes only the 5-minute rate — quoting the 1-hour rate would overstate every cached turn by 60%.
  - There is no way to *price* the non-default path even if one were selectable.

  This deliberately did not become a `provider_options` knob: the kernel computes and persists `cost_usd` from `Pricing`, so a TTL that changed the rate while riding in a pass-through field would silently produce wrong ledger rows forever — exactly the failure the repository's replay-determinism rule (`.claude/rules/determinism.md`) treats as a correctness bug. Fixing it properly means making the cache rate a function of the mechanism used, which is a `Pricing` redesign, not a field addition.
- Whether a model needs to declare that it rejects `GenerationParams.temperature`. Several vendors' reasoning models reject non-default sampling parameters outright (Anthropic's effort-ladder models return a 400; other vendors' reasoning models ignore the value silently). There is no field for this, so an adapter facing such a model can only drop the operator's `temperature` on the floor — which is the right behavior, but it happens invisibly, and the kernel cannot tell a dropped parameter from an honored one. Today adapters infer it from the thinking shape, which is a proxy that will be wrong for the first model that has an effort ladder *and* accepts temperature. The same question applies to `top_p` and any other sampling parameter added later, so the fix is probably a general "declared sampling parameters" list rather than a per-parameter bool.
- Retry/backoff policy specifics (exponential backoff parameters) — likely belongs in the kernel's routing logic rather than this protocol, but needs to be decided somewhere; see [`configuration/blocks-reference.md`](../configuration/blocks-reference.md)'s `settings{}` retry defaults for the current kernel-side values.
- Whether `content_filtered` needs sub-categories (input filtered vs. output filtered) — there isn't enough vendor detail yet to decide.
- `Pricing.currency` is declared as a string but v1 only ever acts on `"USD"` — no conversion mechanism, no mixed-currency cost aggregation across providers with different currencies. Fine while vendors generally price in USD; would need real design work the moment that stops being true.
- Whether a plugin author republishing overlapping `Pricing.tiers` should be a hard capability-load-time rejection (as currently specified) or a softer warning-plus-last-write-wins — the former was chosen for consistency with this project's general "ambiguity is an error" posture, but hasn't been stress-tested against how often a plugin author might get tier boundaries slightly wrong.
