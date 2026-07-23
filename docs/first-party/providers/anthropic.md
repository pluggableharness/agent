# Anthropic

A first-party model-provider reference for PluggableHarness Agent: Anthropic's model lineup, reasoning/caching behavior, and wire-protocol shape, as they bear on building the `docs/specifications/provider/` plugin adapter for this vendor. Descriptive reference, not a protocol spec — see [`docs/specifications/provider/`](../../specifications/provider/README.md) for PluggableHarness Agent's own design authority.

## 1. Overview

Anthropic is the vendor whose Claude models are most directly relevant to a coding-agent harness — this project's own reasoning, and much of the terminology in its specs (`thinking`, `cache_control`, adaptive effort), is informed by Claude's API shape. The model family naming has moved away from purely dated snapshot IDs (`claude-3-5-sonnet-20241022`) toward a mix of plain generational names (`claude-opus-4-8`, `claude-sonnet-5`, `claude-sonnet-4-6`) and one remaining dated ID for the smaller tier (`claude-haiku-4-5-20251001`). A plugin author should not assume every Claude model ID follows the same pattern going forward.

Anthropic's overall API philosophy leans toward giving the model more autonomy over its own reasoning: the newest models default to an adaptive thinking mode the caller doesn't have to configure at all, in contrast to the older discrete `budget_tokens` knob that required the caller to pick a token count up front. Tool-call arguments and cache markers are both first-class, explicit constructs on the wire (`input` as a parsed object, `cache_control` as an explicit per-block annotation) rather than automatic, opaque vendor behavior — this is the vendor most likely to require an adapter to reason about `data-types.md`'s [`CachingSpec`](../../specifications/provider/data-types.md#cachingspec) `explicit_markers` mode correctly.

## 2. Model roster & capabilities

| Model | Context window | Max output | Tool use | Vision | Streaming | Notes |
|---|---|---|---|---|---|---|
| claude-3-5-sonnet-20241022 | 200K | 8,192 | Yes | Yes | Yes | **Retired 2025-10-28 — returns HTTP 404.** Does not predate extended thinking support at all; should not appear in a live roster. Include only as historical/legacy reference, never as an active plugin model entry. |
| claude-opus-4-8 | 1M | 128K | Yes | Yes | Yes | Flagship. Adaptive thinking only — `budget_tokens` is removed and returns a 400 error. Supports discrete effort levels (low/medium/high/xhigh/max) via a separate `output_config.effort` parameter, distinct from the thinking-budget mechanism. |
| claude-sonnet-5 | 1M | 128K | Yes | Yes | Yes | Adaptive thinking on by default — omitting `thinking` config still runs adaptive reasoning. Rejects non-default sampling parameters (e.g. custom temperature/top-p) in some configurations; verify against current docs before an adapter assumes standard sampling controls work unconditionally. |
| claude-sonnet-4-6 | 1M | 128K | Yes | Yes | Yes | Adaptive thinking recommended; `budget_tokens` is deprecated but still functional here as a transitional escape hatch — not yet a hard error, unlike Opus 4.8/Sonnet 5. |
| claude-haiku-4-5-20251001 | 200K | 64K | Yes | Yes | Yes | Fastest/cheapest tier. Legacy-only thinking style — `thinking:{type:"enabled", budget_tokens:N}`; the newer `effort` parameter errors on this model rather than being silently ignored. |

The context-window and max-output figures for `claude-sonnet-4-6` and `claude-haiku-4-5-20251001` are confirmed, per the table above. `claude-3-5-sonnet-20241022` is the one entry in this table that is fully specified yet not viable for a live roster — a plugin author should either drop it or keep it gated behind an explicit "legacy/historical" flag so it can't be selected for new sessions.

## 3. Reasoning & prompt caching

**Thinking/reasoning** is not uniform across the current lineup — this is a vendor with genuinely different `ThinkingSpec` shapes on different models, not just different defaults:

- **claude-opus-4-8** and **claude-sonnet-5** — adaptive-only (`thinking:{type:"adaptive"}`); the model decides internally when and how much to reason. `budget_tokens` is removed outright (400 error) rather than merely discouraged. Effort is instead controlled via a separate `output_config.effort` parameter (low/medium/high/xhigh/max) on Opus 4.8.
- **claude-sonnet-4-6** — adaptive is the recommended mode, but the older `budget_tokens` continuous-budget mechanism still works as a transitional option.
- **claude-haiku-4-5-20251001** — only the legacy discrete/continuous `budget_tokens` mechanism is supported; the newer `effort` parameter is rejected outright on this model, not just discouraged.

The `budget_tokens` deprecation status is genuinely model-specific and should never be represented as one blanket flag across the vendor: removed (400) on Opus 4.7+/4.8 and Sonnet 5, deprecated-but-functional on Opus 4.6 and Sonnet 4.6, and still the required/only mechanism on Haiku 4.5 and earlier models. Interleaved thinking with tool use is supported across the Claude 4 model family.

**Prompt caching** uses explicit markers (`cache_control` on individual content blocks) rather than automatic detection — the caller decides where cache breakpoints go, with two usage patterns: an automatic-at-request-level mode that places a single breakpoint on the last cacheable block, and explicit per-block breakpoints for finer control. TTL is either 5 minutes (standard) or 1 hour (extended); Anthropic reports up to 80% latency reduction and 90% cost reduction from cache hits. Anthropic states that raw prompt text is not retained at rest for caching purposes — only KV-cache representations and cryptographic hashes are kept in memory for the TTL window. All five roster models (including the retired 3.5 Sonnet) support this same caching mechanism; there is no per-model caching variation the way there is for thinking.

## 4. Wire format & auth

A tool call from the assistant appears as a `content` array entry of shape `{"type":"tool_use","id":"toolu_...","name":"...","input":{...}}` — `input` arrives as an **already-parsed JSON object**, not a string that needs decoding. Multiple `tool_use` blocks may appear within a single assistant turn (parallel tool calls); when that happens, all of the corresponding `tool_result` blocks must be returned together in a **single** subsequent `user` message, each shaped `{"type":"tool_result","tool_use_id":"toolu_...","content":"...","is_error"?:bool}`. Tools themselves are declared via `name`, `description`, and `input_schema` (a JSON Schema object); setting `strict:true` on a tool definition enforces exact schema validation against that tool's calls.

Authentication is API-key based via the `x-api-key` header, paired with a required `anthropic-version` header (e.g. `2023-06-01`). An OAuth alternative exists using `Authorization: Bearer <token>` plus an `anthropic-beta: oauth-2025-04-20` header — OAuth tokens must never be sent via `x-api-key`.

Rate limiting returns HTTP 429 on breach. Limits are enforced as separate input/output tokens-per-minute (TPM) counters per pricing tier, plus a weekly rolling cap; response headers carry rate-limit metadata (usage, reset time). As an order-of-magnitude reference, Tier 1 is roughly 20K input TPM / 4K output TPM for Sonnet-class models, roughly doubling at Tier 2 — treat these as illustrative, not a value to hardcode, since Anthropic adjusts tier thresholds independently of model releases.

## 5. Confirmed vs. uncertain

Anthropic's data in this report is materially more solid than the other three vendors this project ships adapters for. Specifically confirmed:

- The retirement of `claude-3-5-sonnet-20241022` (2025-10-28, live 404).
- `claude-sonnet-4-6`'s max output (128K) and `claude-haiku-4-5-20251001`'s context window and max output (200K / 64K).
- The model-by-model `budget_tokens` deprecation status described in §3 above.
- The tool-calling wire shapes and both auth mechanisms in §4.

Still worth an independent spot-check before an adapter relies on it:

- The exact current numeric rate-limit thresholds per tier (Tier 1/Tier 2 TPM figures above) — these are the kind of operational detail vendors revise without a model-catalog-level announcement.
- Whether claude-sonnet-5's rejection of non-default sampling parameters applies universally or only under specific request shapes.
- Pricing figures generally (this report's slice did not include per-model `Pricing` data — see §6).

Unlike the other three first-party vendors, there is no mini/flash/codex-style thinly-confirmed variant in the current Anthropic roster to flag — every non-retired model here has the same (high) confidence level across all capability fields.

## 6. Implications for PluggableHarness Agent

**`ThinkingSpec`** ([`data-types.md#thinkingspec`](../../specifications/provider/data-types.md#thinkingspec)): Anthropic is the vendor that most directly motivated this type's sum-type shape rather than a boolean flag. A conformant adapter cannot declare one `ThinkingSpec` for the whole plugin — it must vary per `ModelSpec`: `mode: always_on_adaptive` for Opus 4.8 and Sonnet 5 (with `can_disable` reflecting that `budget_tokens` now 400s rather than silently degrading), `mode: continuous_budget` for Haiku 4.5 (which never gained adaptive support), and — for Sonnet 4.6, which genuinely supports both mechanisms simultaneously during its transitional window — the adapter author will need to pick one canonical `mode` to declare (adaptive is recommended by Anthropic) while being aware `budget_tokens` still functions underneath as an escape hatch not directly representable in a single `ThinkingSpec` value. The `default` field matters concretely here: because Sonnet 5 runs adaptive thinking even when a request omits `thinking` entirely, a kernel wanting deterministic, budget-bounded behavior must know to always send an explicit override rather than relying on omission meaning "no thinking."

**`CachingSpec`** ([`data-types.md#cachingspec`](../../specifications/provider/data-types.md#cachingspec)): every current Anthropic model sets `mode: explicit_markers` — the adapter is responsible for placing `cache_control` breakpoints on content blocks per `data-types.md`'s explicit-markers semantics, not for detecting automatic caching. Given the 5-minute standard TTL, an adapter maintaining a long-running agentic session across tool-execution gaps is a strong candidate for implementing the optional `keepalive_supported` behavior described in `data-types.md`'s cache-keepalive note, since Anthropic's own API gives the adapter no server-side keepalive to rely on.

**Tool schema and `ToolCall`/`ToolResult`** ([`data-types.md#tool-schema`](../../specifications/provider/data-types.md#tool-schema)): Anthropic is one of the vendors (with Google and Ollama) whose tool-call arguments already arrive as a parsed object, so the Anthropic adapter's translation at the string/object boundary is the simpler direction — it serializes the kernel's parsed-JSON internal representation directly into `input` with no encode/decode step, unlike an OpenAI-shaped adapter. The adapter does need to handle the parallel-tool-call batching rule from §4 above (all `tool_result` blocks for one turn must land in a single `user` message) since this is stricter than what the generic protocol assumes about one-result-per-message.

**`StreamCompletion` and `thinking`/`redacted_thinking` blocks** ([`protocol.md#streamcompletion`](../../specifications/provider/protocol.md#streamcompletion), [`data-types.md#canonical-message--content-block-schema`](../../specifications/provider/data-types.md#canonical-message--content-block-schema)): Claude's extended-thinking output can carry an opaque signature the spec requires the kernel and state backend to round-trip verbatim (`StreamEvent.ThinkingSignature.signature`). An Anthropic adapter must store and replay this signature unmodified on subsequent turns of the same session — Anthropic's API rejects a request where a prior `thinking` block is missing or altered, so any adapter bug that drops or mangles this field will surface as an outright request failure, not a quality regression.

**`GetCapabilities`** ([`protocol.md#getcapabilities`](../../specifications/provider/protocol.md#getcapabilities)): given the retirement of `claude-3-5-sonnet-20241022`, the Anthropic adapter's built-in model list needs a real deprecation lifecycle — this vendor has already demonstrated it will pull a model ID from service with a hard 404 rather than a soft warning period, so `GetCapabilities` should not ship a plugin version whose roster still lists a model the live API no longer serves.

**`CountTokens`** ([`protocol.md#counttokens`](../../specifications/provider/protocol.md#counttokens)): Anthropic exposes a token-counting capability, so this adapter is a good candidate to satisfy the protocol's preference for exact (`exact: true`) counts against the kernel's `CountTokens` primitive rather than falling back to the generic heuristic — a plugin author should implement this against Anthropic's real tokenizer rather than treating it as optional.

**`Pricing`** ([`data-types.md#pricing`](../../specifications/provider/data-types.md#pricing)): because caching is supported, `cache_write_per_mtok` and `cache_read_per_mtok` are both required fields on every Anthropic `PricingTier`, not optional ones — this report's source data did not include per-model dollar figures, so an adapter author will need to source current Anthropic pricing separately before populating this shape.
