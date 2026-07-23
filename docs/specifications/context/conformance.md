# Context provider — conformance

## Error taxonomy

Smaller than the model-provider taxonomy ([`provider/conformance.md#error-taxonomy`](../provider/conformance.md#error-taxonomy)), but a plugin MUST still classify every failure into one of the following rather than collapsing them into one generic error:

| Category | Meaning | Kernel's expected reaction |
|---|---|---|
| `source_unavailable` | Declared file/glob/source unreadable at call time (deleted mid-session, permission error) | Drop the section for this turn, log; do not fail the turn |
| `budget_exceeded` | Provider's own section (or, for a compactor, its whole returned chain) exceeds `token_budget` | Reject per [`data-types.md#budget-mechanics`](data-types.md#budget-mechanics); do not fail the turn for a non-compactor violator |
| `scope_violation` | Non-compactor provider mutated a section it doesn't own | Discard entire response, restore prior chain, log |
| `invalid_request` | Malformed request — a kernel/adapter bug | MUST NOT retry as-is; log with full request shape |
| `unknown` | Anything else | MUST include the raw plugin error message for debugging |

`ContextError` MUST include `category` (above), `message` (human-readable), and `retryable` (bool).

On the wire, using the canonical gRPC error-code mapping: `source_unavailable` → `codes.Unavailable` (a transient content-source problem, same reasoning as a crashed tool process); `budget_exceeded` → `codes.FailedPrecondition` (the provider failed a precondition of the call — fitting its own declared/allocated budget); `scope_violation` → `codes.PermissionDenied` (the provider exceeded what it's permitted to touch given its declared `compactor` value); `invalid_request` → `codes.InvalidArgument`; `unknown` → `codes.Internal`, never `codes.Unknown`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `GetCapabilities` / `Configure` / `Contribute` RPCs | MUST | the whole protocol surface |
| `text` content blocks | MUST | baseline; v1 has no other content type |
| Non-text content blocks (`image`, etc.) | MUST NOT (v1) | kernel MUST reject, not silently drop |
| `stability` declaration | MUST | [`data-types.md#stability-hint--cache-prefix-ordering`](data-types.md#stability-hint--cache-prefix-ordering) |
| `default_token_budget` declaration | MUST | [`data-types.md#budget-mechanics`](data-types.md#budget-mechanics) |
| Self-truncation/summarization to fit `token_budget` | MUST | kernel does not do this on the plugin's behalf |
| Section `label` | MUST | [`data-types.md#labeling`](data-types.md#labeling) |
| Stripping non-semantic authoring artifacts | SHOULD | [`data-types.md#stripping-non-semantic-artifacts`](data-types.md#stripping-non-semantic-artifacts) |
| Own-section-only mutation | MUST, unless `compactor: true` | [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining) |
| Cross-section rewrite (`compactor`) | MAY, capability-gated | [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining) |
| Compactor rewrites should summarize, not just truncate | SHOULD | [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining) |
| `conversation_history` visible only to `compactor: true` providers | MUST | [`protocol.md#session-wide-conversation-compaction`](protocol.md#session-wide-conversation-compaction) |
| Kernel applies `rewritten_history` when a compactor returns it | MUST | [`protocol.md#session-wide-conversation-compaction`](protocol.md#session-wide-conversation-compaction) |
| `tokens` computed via kernel `CountTokens` callback, never a provider heuristic | MUST | [`data-types.md#contextsection`](data-types.md#contextsection) |
| Reacting to `files_touched` for JIT scoping | MAY | [`README.md#firing-cadence--jit-loading`](README.md#firing-cadence--jit-loading) |
| Structured error taxonomy (above) | MUST | |
| `Render` | MAY | generic fallback exists |
| Config-load-time budget-sum warning | SHOULD (kernel-side) | [`data-types.md#budget-mechanics`](data-types.md#budget-mechanics) |
| Config-load-time stability-order warning | SHOULD (kernel-side) | [`data-types.md#stability-hint--cache-prefix-ordering`](data-types.md#stability-hint--cache-prefix-ordering) |

## Open questions

- **Whether `compactor` is confirmed as this category's own interpretation.** Nothing in [`architecture.md`](../architecture.md#hook-dispatch-semantics)'s general `transform`-mode hook text states explicitly that a subscriber may rewrite *another* subscriber's contribution — this protocol resolves a real tension (transform mode's stated ability to "return a modified version" of the whole payload vs. per-provider budget attribution) by introducing the `compactor` flag, but that resolution is this document's own design choice, not something asserted elsewhere. A future cross-cutting review of `transform`-mode semantics at other hooks (e.g. whether an analogous capability flag makes sense at `pre-model-call`) should treat this as precedent to confirm or revise, not settled law.
- **Memory-provider-adjacent findings, deferred to [`memory/README.md`](../memory/README.md):** agent-*written*, cross-session-*persisted* knowledge (auto-memory files, multi-tier memory with inbox ratification, auto-memories) is squarely the memory provider's territory (read at `context-assemble`, write at `post-response`/`session-end`), not this category's. Two patterns worth carrying forward into that document specifically: a ratification-gate write path (agent drafts, human/policy approves before content becomes canonical) and a "10+ message session" heuristic as a candidate trigger signal for when to persist. Neither is acted on here.
