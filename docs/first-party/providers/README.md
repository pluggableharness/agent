# Model provider reports — index

This directory distills real, sourced capability data for the four LLM vendors PluggableHarness Agent ships first-party model-provider plugins for: **Anthropic**, **OpenAI**, **Google** (Gemini), and **xAI** (Grok). Three other vendors appear in the underlying research (Mistral, Cohere, Ollama) but are out of scope for first-party support and aren't covered here.

> [!IMPORTANT]
> **These are descriptive reference documents, not authoritative PluggableHarness Agent protocol specs.** [`docs/specifications/provider/`](../../specifications/provider/README.md) remains the sole source of truth for the model-provider plugin protocol itself (`GetCapabilities`, `Configure`, `StreamCompletion`, `CountTokens`, the `ModelSpec`/`ThinkingSpec`/`CachingSpec`/`Pricing` data types). Each report's closing "Implications for PluggableHarness Agent" section makes non-authoritative, recommendation-grade observations about building that vendor's specific plugin adapter, cross-referenced into the protocol spec by heading anchor.

Confidence varies sharply by vendor: Anthropic's roster and behavior are well-established throughout; the other three vendors' data is internally consistent but leaves "uncertain" fields genuinely uncertain rather than smoothed over. Within a single vendor, confidence also tracks how flagship a model is — a newest/most-prominent model is typically fully specified, while smaller, newer, or less-visible variants (mini/flash/codex/realtime/build variants) are frequently "uncertain" across most fields. Each report's own "Confirmed vs. uncertain" section is the authoritative account of this per vendor — don't assume parity between a flagship and its siblings just because they share a naming prefix.

## Reports

| Vendor | Report | Confidence | Notable gap to know about |
|---|---|---|---|
| Anthropic | [anthropic.md](anthropic.md) | High — well-established | One model (`claude-3-5-sonnet-20241022`) is retired and must not appear in a live roster |
| OpenAI | [openai.md](openai.md) | Mixed — flagship trio solid, most other models thin | Streaming support is uncertain even for current flagships; the `gpt-realtime-*` family likely needs a separate (WebSocket) transport, not the standard `StreamCompletion` path |
| Google (Gemini) | [google.md](google.md) | Mixed — 3.x flagship solid, 2.5/1.5 generations thin | Runs two prompt-caching modes concurrently (implicit + explicit), which the protocol's single-value `CachingSpec.mode` can't fully represent |
| xAI (Grok) | [xai.md](xai.md) | Mixed — wire/auth/reasoning solid, several roster fields open | Reasoning cannot be disabled on any Grok model; tool-result submission wire shape is inferred (unconfirmed), not directly documented |

Each report follows the same six-section structure:

1. **Overview** — the vendor, its model-family naming scheme, and anything distinctive about its API philosophy relevant to a plugin author.
2. **Model roster & capabilities** — per-model table (context window, max output, tool use, vision, streaming, notes), confirmed/uncertain stated honestly per cell.
3. **Reasoning & prompt caching** — how extended-thinking control and prompt-caching work for this vendor, including model-to-model variation.
4. **Wire format & auth** — tool-call wire shape (call emission and result submission), auth mechanism, rate-limit behavior.
5. **Confirmed vs. uncertain** — an honest accounting of what's solid versus what needs independent verification before a plugin adapter relies on it.
6. **Implications for PluggableHarness Agent** — non-authoritative design recommendations mapping this vendor's real behavior onto the protocol's concrete data types and RPCs, with the governing spec file+anchor named explicitly.
