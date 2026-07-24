# Context provider protocol

Covers the **context provider** category — a plugin that hooks `context-assemble` and contributes text content to the prompt before each model call (e.g. a CLAUDE.md reader, an AGENTS.md reader, a git-status/ file-tree summarizer). Multiple context providers may be configured and load simultaneously in the same `agent.hcl` — this sidesteps the convention-file format war entirely; which file(s) a session reads is a plugin/config choice, not a kernel opinion. See [`architecture.md`](../architecture.md#the-seven-provider-categories) for where this category sits among the other six.

## Scope boundary

This category covers content injected into the prompt *before* a model call, sourced from convention files, repo orientation, or session-state summarization. It does **not** cover:

- On-demand code-intelligence retrieval (grep, LSP, embeddings) — that's tool-provider territory, see [`tool/README.md`](../tool/README.md).
- Cross-session persisted knowledge an agent itself decides to write — that's memory-provider territory, see [`memory/README.md`](../memory/README.md).
- Keeping a vendor prompt-cache prefix warm across a long tool-execution gap — that's a model-provider concern, owned by the adapter that already understands its own vendor's TTL mechanics, not this category or the kernel loop. See [`CachingSpec.keepalive_supported`](../model/data-types.md#cachingspec).

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). The standard handshake applies uniformly across all seven provider categories and isn't repeated per category.

A context provider plugin exposes four RPCs: `GetCapabilities`, `Configure`, `Contribute`, `Describe`. It MAY additionally implement `Render` (see [`protocol.md#render`](protocol.md#render)).

**`Contribute` is unary request/response, not streamed.** Unlike a model provider's `StreamCompletion`, context assembly happens before the model call starts, and convention-file/orientation content is small enough that no researched harness needed token-level streaming for it. See [`protocol.md#contribute-the-context-assemble-rpc`](protocol.md#contribute-the-context-assemble-rpc).

## Firing cadence & JIT loading

The kernel MUST invoke `context-assemble` at least once per turn, before each model call — not only once at `session-start` — so providers can react to `ContextRequest.files_touched` and contribute newly-relevant, narrower-scoped content as the session progresses. This is the JIT-loading pattern: the converging trend across surveyed harnesses (Claude Code, Gemini CLI, Amp, Kilo Code) is lazy, subdirectory-scoped injection once the agent has actually touched files in that area, over eager whole-repo, every-turn injection. A provider MAY still choose whole-session eager injection instead (returning the same section unchanged on every firing) — the kernel places no restriction either way; loading strategy is a provider implementation choice, not a kernel opinion. See [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining) for the per-firing chain contract this cadence feeds into.

Per [`architecture.md`](../architecture.md#hook-dispatch-semantics), context providers subscribe at `context-assemble` in `transform` mode, running as an ordered chain in `agent.hcl` declaration order — determinism matters here specifically because order affects what the model attends to (also see [`data-types.md#stability-hint--cache-prefix-ordering`](data-types.md#stability-hint--cache-prefix-ordering) for why declaration order interacts with prompt-cache reuse).

## Category structure

- [`protocol.md`](protocol.md) — the four/five RPCs: `GetCapabilities`, `Configure`, `Contribute`, `Describe`, `Render`.
- [`data-types.md`](data-types.md) — `ContextRequest`, `ContextSection`, `ContextContribution`, the ordering/chaining and compaction contract, and content-structuring requirements.
- [`examples.md`](examples.md) — the real proto wire definitions, a worked two-provider `context-assemble` sequence, and a budget worked example.
- [`conformance.md`](conformance.md) — the error taxonomy and the MUST/SHOULD/MAY summary matrix, plus genuinely open questions.
