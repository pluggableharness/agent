# Google (Gemini)

A first-party model-provider reference for PluggableHarness Agent: Google's model lineup, reasoning/caching behavior, and wire-protocol shape, as they bear on building the `docs/specifications/model/` plugin adapter for this vendor. Descriptive reference, not a protocol spec — see [`docs/specifications/model/`](../../specifications/model/README.md) for PluggableHarness Agent's own design authority.

## 1. Overview

Google's Gemini API is the relevant surface for a coding-agent plugin (as opposed to Vertex AI's enterprise-facing variant, which is out of scope here). The model family spans three active generations — 3.x (`gemini-3-pro`, `gemini-3-flash`, `gemini-3.5-flash`), the still-served 2.5 generation (`gemini-2.5-pro`, `gemini-2.5-flash`), and the legacy `gemini-1.5-pro` — with a "pro" (larger, more capable) and "flash" (faster, cheaper) tier running in parallel within each generation.

Two things stand out for a plugin author. First, Google's own API surface changed shape between generations: the 2.5 line controls extended reasoning with a continuous token budget (`thinkingBudget`), while the 3.x line replaced that with a discrete `thinking_level` enum — an adapter that supports both generations has to speak two different reasoning-control dialects, not one. Second, Google runs two caching mechanisms side by side (automatic and manual) rather than picking one, which is unusual among the vendors surveyed for this project and is expanded on in §3.

## 2. Model roster & capabilities

| Model | Context window | Max output | Tool use | Vision | Streaming | Notes |
|---|---|---|---|---|---|---|
| `gemini-3-pro` | 1M tokens | 64K | Yes | Yes | Yes | Flagship; fully confirmed. Discrete `thinking_level` (LOW/MEDIUM/HIGH), default HIGH. |
| `gemini-3-flash` | 200K tokens | Uncertain | Uncertain | Uncertain | Uncertain | Named as a GA-sounding model but almost entirely unconfirmed beyond context window — don't assume parity with `gemini-3-pro`. |
| `gemini-3.5-flash` | 1M tokens | 65K | Yes | Uncertain | Yes | Vision is notably unconfirmed for a "flash" model marketed as multimodal — worth an independent spot-check before shipping `supports_vision: true`. Discrete `thinking_level` (MINIMAL/LOW/MEDIUM/HIGH), default MEDIUM. |
| `gemini-2.5-pro` | 1M tokens (current) | Uncertain | Uncertain | Uncertain | Uncertain | Source material also cites a "2M tokens coming soon" figure — that is a roadmap claim, not the model's current capability, and should not be recorded as the live `context_window` value. Uses continuous `thinkingBudget`, not `thinking_level`. |
| `gemini-2.5-flash` | Uncertain | 65,536 | Uncertain | Uncertain | Uncertain | An oddly precise max-output figure (65,536) paired with a completely unknown context window — an inconsistency in the underlying confidence, not just a gap, and worth confirming both numbers together rather than trusting one because the other looks precise. |
| `gemini-1.5-pro` | 2M tokens | Uncertain | Uncertain | Uncertain | Uncertain | Legacy generation, predates Gemini's thinking feature entirely (not "uncertain" — genuinely not applicable). Treat its other capability gaps as "probably no" pending confirmation, not as neutral unknowns, since implicit caching and current tool-calling conventions were both introduced after this generation shipped. |

No models in this lineup are flagged as retired in the source material.

## 3. Reasoning & prompt caching

**Reasoning.** Google's extended-thinking control is genuinely two different mechanisms depending on model generation:

- **3.x generation** (`gemini-3-pro`, `gemini-3.5-flash`, and by extension `gemini-3-flash` once confirmed) uses a **discrete effort levels** model via a `thinking_level` parameter. `gemini-3-pro` exposes LOW/MEDIUM/HIGH (default HIGH); `gemini-3.5-flash` exposes an extra MINIMAL tier — MINIMAL/LOW/MEDIUM/HIGH (default MEDIUM). Source material also references a `gemini-3.1-pro` variant using this same discrete style with a default of HIGH, though that model isn't part of the roster data available here and shouldn't be treated as confirmed.
- **2.5 generation** (`gemini-2.5-pro`, `gemini-2.5-flash`) instead uses a **continuous token budget** via `thinkingBudget` — the older style, not the `thinking_level` enum.
- **1.5 generation** (`gemini-1.5-pro`) predates thinking entirely; there is no reasoning-control surface to speak of for this model.

When a request omits reasoning configuration, the default behavior auto-adjusts effort based on the complexity of the prompt rather than falling back to a fixed level — a plugin author who wants deterministic behavior needs to send an explicit override rather than relying on "unspecified."

**Prompt caching.** Google is unusual among the vendors surveyed for this project in running **two caching mechanisms concurrently, not as alternatives**:

- **Implicit (automatic) caching** — enabled by default for Gemini 2.5+ models. The vendor detects and caches common prompt prefixes across requests transparently; the caller does nothing (no markers, no declarations). Cache hits get a **75% token discount**.
- **Explicit (manual) caching** — the caller declares cached content up front via API parameters (available through the Enterprise API surface). This yields a **90% discount** on 2.5+ models, or 75% on 2.0 models. Nothing about the request shape changes beyond that declaration — there's no special content-block marker analogous to Anthropic's `cache_control`.

Both modes are available at once on the same 2.5+ models: a request that does nothing still benefits from the 75%-discount implicit path, while a caller willing to declare content explicitly can get the deeper 90% discount instead. This dual-mode structure — and its tension with a single-value caching mode field — is picked up again in §6.

Model versioning also creates real discontinuities here: implicit caching was only added starting with the 2.5 generation, so `gemini-1.5-pro` predates it and should not be assumed to support automatic caching at all.

## 4. Wire format & auth

A tool call from the model arrives as a `functionCall` object inside `candidate.content.parts`: `{id, name, args}`, where **`args` is already a parsed object**, not a JSON-encoded string (unlike OpenAI/Mistral). The caller sends the result back as a `function_response` message/part keyed by the matching `id`. The Live API (WebSocket) uses the same message shapes as the REST API, just over a different transport. Tools themselves are declared in OpenAPI-schema format under a `tools` "setup" block.

**Auth** is a genuine gap in the available source material: it mentions an API key and an account-subscription-tier mechanism, but the actual wire detail — header name, query parameter, or bearer-token format — isn't specified. This needs independent confirmation before an adapter is built against it; don't assume it matches another vendor's `Authorization: Bearer` convention without checking.

**Rate limits** are enforced per-project across three dimensions — Requests Per Minute (RPM), Tokens Per Minute (TPM), and Requests Per Day (RPD) — plus a spend-based cap evaluated on a rolling 10-minute window. Exceeding any single dimension triggers a rate-limit error.

## 5. Confirmed vs. uncertain

**Solidly confirmed:** `gemini-3-pro`'s full capability row (context, max output, tool use, vision, streaming), its `thinking_level` reasoning control and HIGH default, `gemini-3.5-flash`'s context/max-output/tool-use/ streaming figures and its MEDIUM default, the existence and default-on status of implicit caching for 2.5+ models, the 75%/90% discount split between implicit and explicit caching, the `functionCall`/`function_response` wire shape, and the RPM/TPM/RPD/spend-window rate-limit structure.

**Genuinely uncertain — spot-check before an adapter relies on them:**

- `gemini-3-flash` — every field except context window (200K) is unconfirmed, despite sounding like a named, GA-ready model.
- `gemini-3.5-flash` — vision support specifically, which is surprising for a model marketed as multimodal.
- `gemini-2.5-pro` — max output, tool use, vision, and streaming are all unconfirmed; only the context window is pinned down, and even that required stripping out a conflated "2M coming soon" roadmap figure to get a clean current-state number (1M).
- `gemini-2.5-flash` — context window is completely unknown despite an exact-looking max-output figure (65,536); treat the precision of one field as no evidence for the other.
- `gemini-1.5-pro` — max output, tool use, vision, and streaming are all unconfirmed. Given this is a legacy pre-thinking, pre-implicit-caching generation, capability gaps here should be treated as "likely absent," not neutral unknowns.
- Auth wire format (header/scheme) for the whole vendor.

As a general pattern: confidence tracks how flagship a model is. `gemini-3-pro` (the newest, most prominent model) is essentially fully specified; every other model in the lineup has at least one major capability gap, and several have almost all fields open. Don't backfill an uncertain field by assuming it matches `gemini-3-pro`'s value.

## 6. Implications for PluggableHarness Agent

**Per-model `ThinkingSpec.mode`, not a per-vendor constant.** Google's own lineup spans three different values of the [`ThinkingSpec`](../../specifications/model/data-types.md#thinkingspec) `mode` enum on its own: `discrete_effort` for the 3.x line (with `effort_levels` populated from the LOW/MEDIUM/HIGH or MINIMAL/LOW/MEDIUM/HIGH sets and `default` set to that model's actual default, e.g. `"HIGH"` for `gemini-3-pro`), `continuous_budget` for the 2.5 line (`budget_range` populated from `thinkingBudget`'s token-count bounds), and effectively `none` for `gemini-1.5-pro`. This is exactly the scenario `data-types.md` cites as the reason `ThinkingSpec` lives on each `ModelSpec` rather than being a single vendor-level flag — a Google adapter must build a distinct `ThinkingSpec` per model, and must not assume the `thinking_level` vs `thinkingBudget` parameter name generalizes across the whole roster.

**`CachingSpec.mode` cannot represent Google's actual behavior as a single value.** [`CachingSpec`](../../specifications/model/data-types.md#cachingspec)'s `mode` field is a single enum per model (`none` / `explicit_markers` / `implicit_automatic`) — a sum in the "one active mode" sense, not a set. But Google's 2.5+ models genuinely run both simultaneously: implicit automatic caching is on by default (75% discount, no caller action) *and* explicit manual declaration is available concurrently for a deeper 90% discount. Neither enum value alone captures this. A plugin author building this adapter has to make a real choice here rather than treating it as a formality: the pragmatic default is `mode: implicit_automatic` (it matches what happens when the caller does nothing, which is the common case), with the explicit/manual pathway and its better discount rate exposed separately — e.g. surfaced only through documentation or a provider-specific `Configure` option rather than through `CachingSpec` itself. This is worth flagging back to the protocol's designers as a real gap: the current `CachingSpec` shape has no way to declare "this model supports two caching modes concurrently, with different discount rates," which is precisely the situation Google's own docs describe.

**Tool-call arguments arrive pre-parsed.** Per the [tool schema](../../specifications/model/data-types.md#tool-schema) section's cross-vendor note, Google — like Anthropic and Ollama, unlike OpenAI and Mistral — delivers `functionCall.args` as an already-parsed object rather than a JSON-encoded string. The adapter's `ToolCall` translation should pass this straight through into the kernel's internal parsed-JSON representation without a parse step, and the reverse `function_response` submission needs no re-encoding to a string either.

**Context window: record current state, not roadmap state.** `gemini-2.5-pro`'s "1M tokens (2M coming soon)" phrasing from the vendor's own material is a trap for a naive `ModelSpec.context_window` value — that field is a single `int` representing what the model can do today ([`ModelSpec`](../../specifications/model/data-types.md#modelspec)), so the adapter must publish `1_000_000`, not a value that anticipates the unreleased 2M figure, and should update it only once the larger window actually ships.

**Vision and streaming gaps need resolving before publishing `ModelSpec`, not defaulting to true.** Because `supports_vision` and `supports_streaming` are both plain, required booleans on `ModelSpec`, and the [canonical message schema](../../specifications/model/data-types.md#canonical-message--content-block-schema) requires an `image` block be rejected with a clear `invalid_request` error whenever `supports_vision` is `false`, an adapter cannot ship "uncertain" as a value for `gemini-3-flash`, `gemini-2.5-pro`, `gemini-2.5-flash`, or `gemini-1.5-pro` — each needs an actual confirmed answer before its `ModelSpec` is published, since guessing wrong in either direction either silently breaks image-capable requests or incorrectly rejects valid ones.

**Auth details block `Configure` today.** Because the exact API-key wire format isn't confirmed in the available material, whoever implements this plugin's [`Configure`](../../specifications/model/protocol.md#configure) RPC needs to independently verify the header/parameter shape against Google's actual API reference before writing the implementation — `Configure` MUST reject cleanly on a missing key, which is straightforward, but it also needs to know where the key actually goes on the wire, which the source material doesn't specify.

**`CountTokens` and `Render` are unaddressed by the available research.** Nothing in the source material speaks to whether Google exposes a real per-model tokenizer that could back [`CountTokens`](../../specifications/model/protocol.md#counttokens) with `exact: true` results, so this needs a direct look at Google's API reference rather than an assumption either way; until confirmed, the adapter should let counts fall through to the kernel's fallback heuristic rather than claiming exactness it hasn't verified. `Render` is a MAY and nothing here suggests Google needs anything beyond the kernel's generic fallback.
