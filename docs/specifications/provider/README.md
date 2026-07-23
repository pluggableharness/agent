# Model provider protocol

Covers the **model provider** category — an LLM vendor plugin (Anthropic, OpenAI, Gemini, etc.). Named `provider`, not `model-provider`, because in this system's Terraform-derived vocabulary the LLM vendor plugin is the closest analog to what Terraform itself calls a "provider" — it's the anchor spec category; the other five (`tool/`, `context/`, `memory/`, `frontend/`) follow the shape it establishes.

Real-world LLM vendors (Anthropic, OpenAI, Google Gemini, Mistral, Cohere, xAI, Ollama, and others) diverge in significant ways — reasoning control, caching mechanics, tool-call wire shape — and this category's data types are shaped to accommodate that heterogeneity rather than assuming one vendor's design is universal.

See [`architecture.md`](../architecture.md) for the surrounding system (transport, hook dispatch, plan/apply, state backend) — this directory only covers the model-provider RPC surface and data types in detail.

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). Standard handshake (magic cookie, protocol version negotiation) applies uniformly across all six provider categories and isn't repeated per category.

A model provider plugin exposes four RPCs: `GetCapabilities`, `Configure`, `StreamCompletion`, `CountTokens`. It MAY additionally implement `Render` (see [`protocol.md#render`](protocol.md#render)).

**`StreamCompletion` is server-streaming, not bidirectional.** The kernel sends one request (full message history + tool specs + params) and receives a stream of response chunks back — this matches how vendor completion APIs actually work (one HTTP request, SSE/chunked response; vendors generally don't accept mid-stream client input on the same call). Cancellation (the one thing bidirectional streaming would otherwise be needed for) is handled by the kernel simply cancelling/closing the gRPC stream — a standard, natively-supported operation on a server-streaming call. Plugin authors MUST treat stream cancellation as a normal, expected event (stop generating, release resources), never as an error condition.

## Category structure

- [`protocol.md`](protocol.md) — the four/five RPCs: `GetCapabilities`, `Configure`, `StreamCompletion`, `CountTokens`, `Render`.
- [`data-types.md`](data-types.md) — `ModelSpec`, `Pricing`/`PricingTier`, `ThinkingSpec`, `CachingSpec`, the canonical message/content-block schema, and the shared tool-schema subset.
- [`examples.md`](examples.md) — a worked `agent.hcl` provider block, the wire protocol definitions, a cost-computation walkthrough, and a full `StreamCompletion` event sequence.
- [`conformance.md`](conformance.md) — the error taxonomy and the MUST/SHOULD/MAY summary matrix, plus genuinely open questions.
