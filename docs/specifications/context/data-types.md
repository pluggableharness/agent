# Context provider — data types

## `ContextRequest`

`Contribute`'s request, delivered once per `context-assemble` firing:

```protobuf
ContextRequest {
  session_id, parent_session_id, turn_number
  token_budget      int              // MUST — kernel-computed allocation
                                       // for this call, see #ordering--chaining
  model_target      ModelTarget       // MUST — { id, context_window, effective_ceiling },
                                       // from provider.md's ModelSpec
  files_touched     []string         // MAY be empty (e.g. turn 0 / session-start)
  working_directory string
  prior_sections    []ContextSection // MUST — accumulated output of earlier
                                      // providers in this hook's
                                      // declaration-order chain
  conversation_history []Message?    // populated ONLY for a compactor provider
}
```

`model_target` is the same `ModelTarget` shape [`memory/data-types.md`](../memory/data-types.md)'s `RecallRequest` carries — a rich "what am I assembling for" descriptor derived from the resolved model provider's `ModelSpec` ([`model/data-types.md#modelspec`](../model/data-types.md#modelspec)), distinct from the narrower `ModelRef` that [`kernel-callbacks.md#counttokens`](../kernel-callbacks.md#counttokens) uses to select a tokenizer. It lets a provider tailor content — and compute tokens against the right budget — for the model that will actually consume it, not just the one configured at session start; a sub-agent routed to a smaller model gets a correspondingly smaller `effective_ceiling` here automatically (see [`architecture.md#context-budget`](../architecture.md#context-budget)).

`conversation_history` arrives populated only when this provider's own `ContextCapabilities.compactor == true` (see [`protocol.md#session-wide-conversation-compaction`](protocol.md#session-wide-conversation-compaction)); for every other provider it's empty, indistinguishable from "not provided."

## `ContextSection`

One provider's contribution to the assembled prompt context:

```protobuf
ContextSection {
  provider  string         // producing plugin's declared name — the
                            // identity key for a provider re-finding and
                            // replacing its own prior section
  label     string         // MUST — see #content-structuring-requirements
  content   []ContentBlock // MUST — text-only in v1; a non-text block MUST
                            // be rejected by the kernel, not silently dropped
  tokens    int             // MUST — computed via the kernel's CountTokens
                             // callback, never a provider-local heuristic
  stability enum { static, dynamic }
  truncated bool
}
```

**`tokens` MUST be computed via the kernel's `CountTokens` callback primitive** ([`kernel-callbacks.md#counttokens`](../kernel-callbacks.md#counttokens)), never a provider-local heuristic estimate. This resolves to whichever model provider's real tokenizer is available for `ContextRequest.model_target` (marked `exact: true`) or, failing that, the one canonical fallback formula (`ceil(utf8_byte_length / 4)`, [`kernel-callbacks.md#the-fallback-heuristic`](../kernel-callbacks.md#the-fallback-heuristic)) — the same primitive `provider.md`'s own `CountTokens` RPC and `memory.md`'s recall path resolve through, so a token figure means the same thing everywhere it appears in the system rather than each category inventing its own estimate.

`truncated` records whether this section was cut down to fit its budget, but setting it `true` is not itself sufficient to satisfy the budget constraint — see [`#ordering--chaining`](#ordering--chaining)'s budget-violation rule below.

## `ContextContribution`

`Contribute`'s response:

```protobuf
ContextContribution {
  sections           []ContextSection  // the full accumulated chain, in
                                        // declaration order, including this
                                        // provider's own new/updated section(s)
  rewritten_history  []Message?        // MAY be included by a compactor
                                        // provider alongside its section
                                        // contribution
}
```

`sections` is the full chain, never a delta (see [`protocol.md#contribute-the-context-assemble-rpc`](protocol.md#contribute-the-context-assemble-rpc)). `rewritten_history`, when present, replaces the turn's conversation history before the next model call — see [`protocol.md#session-wide-conversation-compaction`](protocol.md#session-wide-conversation-compaction).

## Ordering & chaining

Per [`architecture.md`](../architecture.md#hook-dispatch-semantics)'s hook dispatch semantics, context providers subscribe at `context-assemble` in `transform` mode, running as an **ordered chain in `agent.hcl` declaration order** (not runtime registration order). Concretely: provider *N* receives provider *1..N-1*'s already-merged `prior_sections` and returns the full chain including its own addition.

Because `transform` mode permits a subscriber to "return a modified version" of the whole payload, a literal reading would let any context provider silently rewrite or delete another provider's section. That's undesirable for budget attribution and for reasoning about what a plugin can do to another plugin's content, so this protocol narrows the contract:

- A context provider MUST only append or edit its **own** section(s) (matched by `provider` name) unless it declares `compactor: true` in `GetCapabilities`.
- A provider declaring `compactor: true` MAY rewrite, merge, or drop **other** providers' sections in the chain it receives — the seam for a compaction/summarization provider under budget pressure, modeled directly on Gemini CLI's dedicated chat-compression pass and Claude Code's auto-compaction (both reduce/reflow already-assembled context rather than each contributor self-limiting in isolation). The kernel MUST still enforce that the compactor's own returned chain fits `token_budget` below — a compactor's whole point is fitting the ceiling, and it MUST produce a chain that does, not merely attempt to.
- A compactor's rewrite SHOULD summarize rather than bluntly truncate wherever the content permits it — the point of granting a provider license to touch content it doesn't own is to preserve meaning under pressure, not just to make bytes fit. Truncation MAY still be the right fallback for content that genuinely can't be meaningfully summarized (e.g. a raw diff), but a compactor that only ever truncates isn't using the capability for what it's for.

### Budget mechanics

Per [`architecture.md#context-budget`](../architecture.md#context-budget), the ceiling is **not** a config value — it is asserted at runtime from the resolved model provider's declared `ModelSpec.context_window`:

```text
effective_ceiling = context_window − reserved_output − system_overhead
```

computed fresh per call, so a sub-agent routed to a smaller model gets a correspondingly smaller pool automatically. Allocation policy stays v1-simple — **fixed per-provider token caps**, no adaptive priority negotiation:

- Each context provider's cap is `agent.hcl`'s declared override if present, else `ContextCapabilities.default_token_budget`.
- At config-load time the kernel SHOULD sum all declared/default caps and warn if the sum could exceed the smallest model's `effective_ceiling` across any configured routing/fallback chain.
- At assembly time (each `context-assemble` firing) the kernel MUST pass each provider its resolved cap as `ContextRequest.token_budget` and MUST validate the returned section's `tokens` against it after that provider's turn in the chain.
- **A context provider MUST NOT return a section exceeding its allocated `token_budget`.** If the provider's underlying source content is larger than the budget, the provider itself MUST perform the reduction — truncation, priority selection, or summarization — before returning. The kernel MUST NOT attempt semantic truncation of an opaque `content` payload; it can only cut raw bytes, which risks truncating mid-unit garbage. This generalizes the dominant cross-harness pattern: Aider binary-searches its PageRank output into a token budget, Gemini CLI compresses at 50% of the context window while preserving the most recent 30% of history — in both cases the *content producer*, not a generic byte-slicer, owns the reduction.
- If a provider violates its budget anyway, the kernel MUST reject that section (drop it — `truncated: true` alone is not sufficient) and MUST NOT fail the whole turn for one misbehaving provider, consistent with the microkernel's plugin-isolation posture.
- `token_budget` MAY be smaller than the provider's own declared default if a smaller-context model is in play this turn (routing-aware, the same principle applied to thinking/effort parameters).

See [`examples.md#budget-worked-example`](examples.md#budget-worked-example) for these mechanics worked with real numbers.

## Content-structuring requirements

### Labeling

Every `ContextSection` MUST carry a `label`. The kernel MUST wrap each section in a clearly delimited boundary (e.g. a heading or tag) when concatenating the chain into the final prompt. This generalizes Aider's named `ChatChunks` segments (`system, examples, done, repo, readonly_files, chat_files, cur, reminder`) — ambiguous section boundaries are a real failure mode surveyed harnesses have had to engineer around.

### Stripping non-semantic artifacts

A context provider SHOULD strip authoring artifacts not intended for model consumption (HTML comments, front-matter markers) before returning content, matching Claude Code's documented behavior of stripping HTML block comments before injection.

### Stability hint & cache-prefix ordering

Every section carries `stability` (`static` — unchanged for the session, e.g. a repo convention file; `dynamic` — may differ turn to turn, e.g. git status or a file tree). This is a direct translation of the strongest cross-cutting finding among surveyed harnesses: convergence on a tools → system → static-project-context → conversation-tail prefix ordering because it is a constraint, not a preference, for prompt-cache reuse — several harnesses' own source comments explicitly document that longer-TTL content must appear earlier than shorter-TTL content sharing the same prefix.

Assembly order is still `agent.hcl` declaration order (author-controlled, deterministic) — this protocol does not override that with silent kernel reordering. Instead: **the kernel SHOULD warn at config-load time if a `dynamic`-stability provider is declared before a `static`-stability provider**, since that ordering invalidates the cache prefix for every provider after it on every turn where the dynamic content changes. This is a lint, not an enforced reorder, consistent with declaration order being an author decision, not a kernel one.
