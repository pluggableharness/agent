# Memory provider — conformance

## Error taxonomy

A plugin MUST classify every failure into one of the following `MemoryErrorCategory` values ([`data-types.md#memoryerror`](data-types.md#memoryerror)) and MUST NOT collapse them into a single generic error:

| Category | Meaning | Kernel's expected reaction |
|---|---|---|
| `not_found` | `UpdateRecord`/`DeleteRecord`/`ApproveRecord`/`RejectRecord` referenced an `id` that doesn't exist | Surface distinctly — a caller passed a stale or wrong id, not a transient failure |
| `invalid_type` | `Record` specified a `MemoryType` this provider doesn't support (absent from `GetCapabilities.supported_types`) | MUST NOT retry as-is; the caller (or kernel routing) picked the wrong provider for this type |
| `ratification_unsupported` | `ApproveRecord`/`RejectRecord` called against a provider with `ratification_supported: false` | MUST NOT retry; a caller bug, since `GetCapabilities` already declared this |
| `budget_exceeded` | `Recall`'s candidate records exceed `token_budget` even after the provider's own truncation | Same MUST-self-truncate principle as the context provider's budget handling; surface as a context-assembly failure, not a generic error |
| `source_unavailable` | This provider's backend storage was unreachable at call time | Retry candidate — transient by nature (a file lock, a down remote service) |
| `unknown` | Anything else | MUST include enough detail for debugging; treat as non-retryable by default |

`MemoryError` MUST include `category` (above), `message` (human-readable), and `retryable` (bool).

On the wire, each category maps to a gRPC status code: `not_found` → `NotFound`, `invalid_type` → `InvalidArgument`, `ratification_unsupported` → `FailedPrecondition`, `budget_exceeded` → `ResourceExhausted`, `source_unavailable` → `Unavailable`, `unknown` → `Internal`, never `Unknown`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `GetCapabilities`/`Configure`/`Recall`/`Record`/`UpdateRecord`/`DeleteRecord` RPCs | MUST | the core protocol surface |
| Fixed `MemoryType` taxonomy (user/feedback/project/reference) | MUST | [`taxonomy.md`](taxonomy.md) — protocol-level, not provider-defined |
| Fixed `MemoryScope` taxonomy (session/project/global) | MUST | [`data-types.md#memoryscope`](data-types.md#memoryscope) |
| Record type and scope immutable after creation | MUST | [`taxonomy.md`](taxonomy.md), [`data-types.md#memoryscope`](data-types.md#memoryscope) |
| Kernel-side `Recall`→`ContextSection` translation, shared budget pool | MUST | [`protocol.md#kernel-side-translation-into-context-assembly`](protocol.md#kernel-side-translation-into-context-assembly) |
| `include_pending` defaults to `false` | MUST | [`protocol.md#recall-the-read-side`](protocol.md#recall-the-read-side) |
| `project`-type records weighted toward recency under budget pressure | SHOULD | [`protocol.md#relevance-ranking`](protocol.md#relevance-ranking) |
| Human-meaningful slug IDs, kernel-enforced uniqueness | MUST | [`protocol.md#record-updaterecord-deleterecord-the-write-side`](protocol.md#record-updaterecord-deleterecord-the-write-side) |
| `UpdateRecord`/`DeleteRecord` fail on unknown `id` rather than no-op | MUST | [`protocol.md#record-updaterecord-deleterecord-the-write-side`](protocol.md#record-updaterecord-deleterecord-the-write-side) |
| Kernel parses `[[name]]` into `links`, resolves to `link` `RenderNode` | MUST | [`protocol.md#structural-name-cross-reference-links`](protocol.md#structural-name-cross-reference-links) |
| Dangling `[[name]]` links rejected at write time | MUST NOT | same section — a dangling link MUST be queryable, never a write-time rejection |
| `memory.remember` fuzzy near-match check before creating a new record | MUST | [`protocol.md#write-triggers`](protocol.md#write-triggers) |
| `ApproveRecord`/`RejectRecord` | MAY, standardized shape if implemented | [`protocol.md#ratification-optional`](protocol.md#ratification-optional) |
| `status: pending` ever returned | MUST NOT, unless `ratification_supported: true` | same section |
| Autonomous write via `post-response`/`session-end` hooks | SHOULD | [`examples.md#autonomous-hook-driven`](examples.md#autonomous-hook-driven) |
| `memory.remember`/`memory.forget`/`memory.search` reference tools | SHOULD (reference implementation) | [`examples.md#explicit-model-invoked`](examples.md#explicit-model-invoked) |
| Multi-protocol plugin (memory + tool provider in one process) | MAY | [`README.md`](README.md#transport--lifecycle) |
| Structured error taxonomy (above) | MUST | |
| `Render` | MAY | generic fallback exists |
| `MemoryRecord.tokens` computed via `CountTokens` kernel callback | MUST | [`kernel-callbacks.md#counttokens`](../kernel-callbacks.md#counttokens); never a provider-local heuristic |
| `RecallRequest.model_target` set | MUST | [`data-types.md#recallrequest--memoryrecord`](data-types.md#recallrequest--memoryrecord) |

## Open questions

None specific to this category:

- **Cross-scope recall** is handled by `MemoryScope` as a real protocol-level enum, with a `scope_filter` on `Recall` and a `scope` field on `Record`/`MemoryRecord`, rather than leaving scoping to provider-side `Configure`-time convention. See [`data-types.md#memoryscope`](data-types.md#memoryscope).
- **Relevance ranking under budget pressure** carries one lightweight protocol rule (`project`-type records SHOULD weight toward recency); the ranking mechanism beyond that stays provider-internal by design. See [`protocol.md#relevance-ranking`](protocol.md#relevance-ranking).
- **Cross-reference linking** has full structural support: `[[name]]` is kernel-parsed into `MemoryRecord.links` and kernel-resolved into `link` `RenderNode`s at render time. See [`protocol.md#structural-name-cross-reference-links`](protocol.md#structural-name-cross-reference-links).
- **`memory.remember`'s existing-record lookup** uses a fuzzy near-match check that surfaces ambiguity as a tool result for the model to resolve, rather than silently duplicating or updating the wrong record. See [`protocol.md#write-triggers`](protocol.md#write-triggers).
- **Token counting** is handled centrally by [`kernel-callbacks.md#counttokens`](../kernel-callbacks.md#counttokens): `MemoryRecord.tokens` is computed via the `CountTokens` kernel callback, and `RecallRequest` carries a `model_target` field so a provider can pass a precise model reference into that callback.
