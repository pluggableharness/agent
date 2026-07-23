# Model provider — protocol

The five RPCs a model provider plugin exposes. See [`README.md`](README.md#transport--lifecycle) for the transport-level framing (server-streaming, cancellation) that applies to `StreamCompletion` specifically.

## `GetCapabilities`

Returns a `Capabilities` value with one `ModelSpec` per model the plugin can serve. This MUST be re-queryable cheaply (the kernel may call it often — e.g. before every routing decision) and MUST NOT require network calls to the vendor if avoidable; a plugin SHOULD ship its model list built in and refresh it lazily/periodically rather than blocking on a live API call per invocation.

The response MAY additionally include `slash_commands: []SlashCommandSpec` (declared once for the provider as a whole, not per model) and MUST include the provider's `ConfigSchema`, so the kernel knows what fields `Configure` expects before ever calling it. See [`data-types.md`](data-types.md#modelspec) for the full `ModelSpec` shape.

### `CountTokens`

```text
CountTokens(text: string) -> { count: int }
```

SHOULD be implemented per model, using that vendor's real tokenizer: rather than investing in a smarter kernel-side fallback heuristic, the expectation is that providers actually implement this against real vendor tokenizers wherever the vendor makes it available, and the fallback ([`kernel-callbacks.md#the-fallback-heuristic`](../kernel-callbacks.md#the-fallback-heuristic)) stays a genuine last resort, not a normal operating path. This is the model-provider side of [`kernel-callbacks.md`](../kernel-callbacks.md)'s `CountTokens` primitive — a model provider that implements this gets its counts marked `exact: true` when the kernel resolves a `CountTokens` call against it; a model provider that doesn't falls back to the documented heuristic. Still not a MUST, because not every vendor makes exact counting cheap or even possible without a network round-trip — but a provider author should treat skipping it as the exception, not the default.

## `Configure`

Accepts a config object decoded from the provider's `agent.hcl` block via the schema-to-cty bridge (see [`configuration/blocks-reference.md`](../configuration/blocks-reference.md)). Field contents are provider-specific (API key, base URL override, org/project IDs, etc.) — this protocol doesn't mandate a shape beyond:

- `Configure` MUST reject with a clear, structured error on missing required fields (e.g. no API key) rather than deferring the failure to the first `StreamCompletion` call.
- A plugin MUST NOT echo any received secret value into an `Emit`'d event, a `Render` output, a log line, or an error message. Secrets flow into the process once, at `Configure` time, and stay there.
- Resolving `env(...)`-style indirection in `agent.hcl` is the kernel's job (part of the HCL/`cty` bridge), not the plugin's — by the time `Configure` is called, the plugin receives resolved literal values regardless of how the operator wrote them in HCL. The `env(name)` argument MUST be a literal string, syntax-validated before evaluation (whether the named variable is actually set is a separate, evaluation-time check).

## `StreamCompletion`

Request: canonical messages ([`data-types.md#canonical-message--content-block-schema`](data-types.md#canonical-message--content-block-schema))
+ tool specs ([`data-types.md#tool-schema`](data-types.md#tool-schema)) +
generation params. Response: a stream of [`StreamEvent`](data-types.md#streamevent)s — see [`examples.md#a-full-streamcompletion-event-sequence`](examples.md#a-full-streamcompletion-event-sequence) for a worked sequence.

A plugin whose backend does not natively stream (batch-only) MUST still implement this RPC shape, emitting the full response as a single terminal burst of events followed by `stop`. `ModelSpec.supports_streaming = false` is how the plugin signals this to the kernel/frontend as a UX hint (e.g. "don't render a live-typing cursor"); it does not change what RPC gets called.

### Generation-parameter validation and capability-aware routing

`GenerationParams.thinking_effort`/`thinking_budget_tokens` MUST be validated against the resolved model's declared [`ThinkingSpec`](data-types.md#thinkingspec) before the request is dispatched to the plugin — an effort level outside `ThinkingSpec.effort_levels`, or a budget outside `ThinkingSpec.budget_range`, is a kernel-level reject-or-fallback, not something sent to the vendor and left to surface as a raw API error three layers up the stack. A caller (the turn loop, a sub-agent spawn) that needs a parameter the resolved model doesn't support MUST either drop back to that model's default behavior or fail the selection, never forward an invalid combination.

This is the same reasoning that makes model routing and fallback chains capability-aware: a fallback candidate ([`configuration/agent-profiles.md#model-routing`](../configuration/agent-profiles.md#model-routing)) is only eligible for a given turn if its declared `ModelSpec`/`ThinkingSpec`/`CachingSpec` actually satisfy that turn's real requirements — context window needed, tool-use, vision, thinking — checked mechanically against `GetCapabilities`' declared envelope, not assumed from declaration order alone. A model that's merely *listed* as a fallback but can't actually serve the turn is skipped, the same way an unmet generation parameter is rejected rather than shipped to the wire.

### Cost computation

`usage`'s token counts are what the vendor reports; converting them into an actual dollar figure is a **kernel** responsibility, not the plugin's — the kernel already has both the counts and the resolved `ModelSpec.pricing` ([`data-types.md#pricing`](data-types.md#pricing)), so there's nothing for the plugin to compute:

```text
cost_usd = input_tokens * pricing.input_per_mtok / 1e6
         + output_tokens * pricing.output_per_mtok / 1e6
         + (cache_write_tokens ?? 0) * pricing.cache_write_per_mtok / 1e6
         + (cache_read_tokens ?? 0) * pricing.cache_read_per_mtok / 1e6
```

These four counters are non-overlapping as vendors report them (a cached-read token is never also counted in `input_tokens`), so this is a plain sum, not a subtraction.

**The kernel MUST compute `cost_usd` immediately upon receiving each `usage` event, using whichever provider plugin version is active at that moment, and MUST persist the computed dollar figure into the state backend event's payload** — not just the raw token counts. This is a replay-fidelity requirement, the same reasoning [`architecture.md`](../architecture.md#versioning--schema-drift--supersedes)'s "supersedes" mechanism already applies elsewhere: vendor pricing changes over time (an "intro pricing through 2026-08-31" window is a realistic example), and a session replayed months later must show what was actually paid at the time, not a figure recomputed against whatever the currently-loaded plugin version happens to declare today. See [`examples.md#cost-computation-worked-example`](examples.md#cost-computation-worked-example) for a worked example illustrating this alongside telemetry, a distinct, side-band concern from the persisted `cost_usd` figure.

## Render

Model providers MAY implement `Render` per the general Emit→Render→Paint pipeline ([`architecture.md`](../architecture.md#emit--render--paint-pipeline)), returning the `RenderTree` formally defined in [`frontend/render-tree.md`](../frontend/render-tree.md) — e.g. to render a `thinking` block collapsed by default, or to render usage/cost info specially. If not implemented, the kernel falls back to its generic default rendering. This is a MAY, not a SHOULD — most model-provider payloads (plain text, tool calls) render fine under the generic fallback; the tool-result side (owned by tool providers) is where custom rendering matters more.
