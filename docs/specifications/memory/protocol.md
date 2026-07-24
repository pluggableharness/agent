# Memory provider — protocol

The RPCs a memory provider plugin exposes. See [`README.md`](README.md#transport--lifecycle) for the transport-level framing — every RPC here is unary.

## `GetCapabilities`

Returns a `MemoryCapabilities` value declaring what this provider supports — see [`data-types.md#memorycapabilities`](data-types.md#memorycapabilities) for the full shape. A provider MAY handle only a subset of the fixed `MemoryType`/`MemoryScope` taxonomies ([`taxonomy.md`](taxonomy.md)); it MUST declare exactly which subset via `supported_types`/`supported_scopes`.

The response MAY additionally include `slash_commands: []SlashCommandSpec` (declared once for the provider as a whole) and MUST include the provider's `ConfigSchema`, so the kernel knows what fields `Configure` expects before ever calling it — the reference tools ([`examples.md#write-triggers-reference-tools`](examples.md#write-triggers-reference-tools)) already cover the common `remember`/`forget`/`search` cases via the ordinary tool-provider path, so this is rarely needed in practice.

## `Configure`

Same contract as every other category: config decoded from the provider's `agent.hcl` block via the schema-to-cty bridge ([`configuration/blocks-reference.md`](../configuration/blocks-reference.md)), with `sensitive` fields enforced at the expression level. No exceptions for this category.

## `Recall` (the read side)

Fires at the same `context-assemble` hook point context providers fire at — memory providers are implicitly subscribed there too, per [`configuration/agent-profiles.md`](../configuration/agent-profiles.md#explicit-hook-subscriptions)'s category-implicit rule. Request and response shapes are in [`data-types.md#recallrequest--memoryrecord`](data-types.md#recallrequest--memoryrecord).

`token_budget` MUST be resolved the same way a context provider's cap is resolved — memory recall competes for the **same** budget pool as context providers, not a separate reserved pool. A `Recall` call whose candidate records still exceed `token_budget` after the provider's own truncation MUST fail with `budget_exceeded` ([`data-types.md#memoryerror`](data-types.md#memoryerror)), the same MUST-self-truncate principle [`context/protocol.md`](../context/protocol.md#contribute-the-context-assemble-rpc) applies to context providers.

`RecallRequest.turn_id` identifies the requesting turn as a ULID string, standardized across the whole protocol — the same treatment as [`context/protocol.md`](../context/protocol.md#contribute-the-context-assemble-rpc)'s `ContextRequest.turn_id` and `plan.v1`'s `turn_id` field.

A returned `MemoryRecord` MAY carry `relevance_score` (`[0, 1]`, normalized) on `Recall` (and `ListRecords`, below) responses — never persisted, never present on a write-side request or response. It lets the kernel merge multiple memory providers' results under one shared budget using a comparable figure. See [`data-types.md#relevance_score`](data-types.md#relevance_score).

`model_target` MUST be set, mirroring the context provider's `ContextRequest` field of the same name — it lets a provider pass a precise model reference into the `CountTokens` kernel callback ([`kernel-callbacks.md#counttokens`](../kernel-callbacks.md#counttokens)) when computing `MemoryRecord.tokens`.

`include_pending` MUST default to `false`: a `pending`-status record ([`protocol.md#ratification-optional`](protocol.md#ratification-optional)) MUST NOT surface through ordinary recall, only through an explicit review path.

### Relevance ranking

Ranking under `token_budget` pressure carries exactly one protocol-level rule, not a full ranking algorithm: a provider SHOULD weight `project`-type records more heavily toward recency than `user`/`feedback`/`reference` records when deciding what to keep, directly matching [`taxonomy.md#project`](taxonomy.md#project)'s definition of `project` as the type that decays fastest. Beyond that one rule, the ranking mechanism itself — keyword match, an internal embedding index, whatever a provider chooses — is entirely provider-internal, consistent with retrieval and embeddings being out of scope elsewhere ([`model/conformance.md`](../model/conformance.md#required-vs-optional-support--summary-matrix)).

### Kernel-side translation into context assembly

Each returned `MemoryRecord` is adapted into an ordinary `ContextSection` ([`context/data-types.md`](../context/data-types.md#contextsection)) before merging:

```go
translate(record) -> ContextSection {
  provider:  <this memory provider's declared name>
  label:     "memory:" + record.type + ":" + record.id
  content:   record.content
  tokens:    record.tokens
  stability: dynamic   // recall results can differ turn to turn as files_touched changes
}
```

The translated section is inserted into the chain at the memory provider's own `agent.hcl` declaration position — the same textual-position rule that governs any other implicit/explicit hook subscriber ([`configuration/agent-profiles.md#explicit-hook-subscriptions`](../configuration/agent-profiles.md#explicit-hook-subscriptions)) — and from that point on flows through the context provider's existing ordering, budget-validation, and (if applicable) compactor machinery completely unchanged: a memory provider's translated section can be rewritten or dropped by a `compactor: true` context provider positioned after it, exactly as any other provider's section could. Memory providers don't get a separate budget system or a separate ordering rule; they share the ones that already exist.

## `Record`, `UpdateRecord`, `DeleteRecord` (the write side)

Request/response shapes are in [`data-types.md#the-write-side`](data-types.md#the-write-side).

**Record IDs are human-meaningful slugs, kernel-enforced unique** — mirroring a kebab-case `name:` convention rather than an opaque generated ID. The kernel, not the provider, MUST enforce uniqueness within a provider's namespace: if a `Record` call's suggested or derived slug collides with an existing record, the kernel MUST append a disambiguating numeric suffix (`user-role`, `user-role-2`, ...) rather than silently overwriting or rejecting the call.

`UpdateRecord` and `DeleteRecord` MUST fail with a structured `MemoryError` (`not_found`, [`data-types.md#memoryerror`](data-types.md#memoryerror)) if `id` doesn't match an existing record, rather than silently no-op'ing. `UpdateRecordRequest.content` MUST replace existing content wholesale, not a patch.

A record MUST declare exactly one `MemoryType` and exactly one `MemoryScope` at creation, and both MUST be treated as immutable afterward — `UpdateRecord` MUST NOT change either field. Recategorizing a record means `DeleteRecord` followed by a new `Record` call, never an in-place type or scope change.

### Structural `[[name]]` cross-reference links

Records can reference one another with `[[name]]`-style structural links, not just prose convention — see [`examples.md#a-worked-cross-reference-example`](examples.md#a-worked-cross-reference-example) for a full worked pair of linked records.

- The kernel, not the memory provider, MUST parse `[[name]]` occurrences out of `content` at `Record`/`UpdateRecord` time and populate `MemoryRecord.links`. This is uniform kernel behavior so every memory provider gets it for free rather than each reimplementing the same regex.
- A link target MUST NOT be required to already exist — forward references are natural (a record can reference one about to be created in the same batch of work). The kernel MUST NOT reject a `Record`/`UpdateRecord` call over a dangling link, but SHOULD make dangling links queryable (e.g. for a future "clean up broken links" operation) rather than silently losing track of them.
- When rendering memory content (`Render` below, or the generic fallback), the kernel MUST resolve `[[name]]` occurrences into [`frontend/render-tree.md`](../frontend/render-tree.md)'s `link` `RenderNode` type, pointing at the target record. This is generic kernel-level post-processing specific to memory-category content, not something each provider's own `Render` implementation needs to reimplement.

## `ListRecords` / `GetRecord`

The enumeration/audit path — paginated browsing and single-record fetch, distinct from `Recall`'s budget-constrained, relevance-ranked read path. Request/response shapes are in [`data-types.md#listrecords--getrecord`](data-types.md#listrecords--getrecord). Both MUST be implemented — they're cheap for any real backend, and generic tooling (a ratification review UI, a "browse what this provider knows" command) has no other way to enumerate or spot-check records.

`ListRecordsRequest`'s `type_filter`/`scope_filter` follow the same "empty means all" convention as `RecallRequest`'s filters. `status_filter` is the one place this differs meaningfully from `Recall`: left unset, both `canonical` and `pending` records are eligible — **`PENDING` records ARE listable here**, with no `include_pending`-style gate. This is deliberate: `ListRecords` is the review-inbox path (an operator or a ratification UI paging through drafts awaiting approval), not the per-turn recall path `include_pending` guards against accidental surfacing of unratified content into a model's context. The two paths have opposite defaults because they serve opposite purposes.

`GetRecord` MUST fail with a structured `MemoryError` (`not_found`, [`data-types.md#memoryerror`](data-types.md#memoryerror)) for an unknown `id`, the same convention as `UpdateRecord`/`DeleteRecord`/`ApproveRecord`/`RejectRecord`.

## Ratification (optional)

Ratification is a pattern a provider MAY implement, not a protocol requirement — matching a low-friction default of writing rather than asking. Where a provider *does* implement it, the shape MUST be standardized so generic tooling (a frontend's "pending memories" view) doesn't need provider-specific handling:

- `RecordResult.status` MAY be `pending` instead of `canonical` — a provider declaring `ratification_supported: true` ([`data-types.md#memorycapabilities`](data-types.md#memorycapabilities)) uses this to mean "drafted, not yet part of what `Recall` normally surfaces."
- A provider supporting ratification MUST implement `ApproveRecord(id) -> RecordResult` (transitions `pending` → `canonical`) and `RejectRecord(id) -> DeleteResult` (discards the draft entirely, not a soft-delete).
- A provider with `ratification_supported: false` MUST NOT ever return `status: pending` — every write is `canonical` immediately, consistent with the low-friction default.

## Write triggers

Both an autonomous, hook-driven path and an explicit, model-invoked path exist side by side — see [`examples.md#write-triggers-reference-tools`](examples.md#write-triggers-reference-tools) for the full write-triggers table. In outline:

- A memory provider is implicitly subscribed to `post-model-response` (`observe` mode, fires every turn) and `session-end` (fires once, unconditionally) — giving every provider a guaranteed last chance to persist something even if its own turn-by-turn heuristic never triggered mid-session. Both ride the shared `pluggableharness.hook.v1.HookSubscriberService.DispatchHook` wire surface ([`agent-loop/hook-dispatch.md`](../agent-loop/hook-dispatch.md)) — the kernel calls `DispatchHook` with a `HookPayload` carrying the `post_model_response` or `session_end` variant, exactly as it would for any other category's hook subscription; this category has no separate, memory-specific dispatch mechanism. `MemoryCapabilities.supported_hook_points` ([`data-types.md#memorycapabilities`](data-types.md#memorycapabilities)) advertises which of these (and any other hook points) this provider actually subscribes to. Nothing in this protocol prescribes *when* within that stream a provider decides to call its own internal write logic.
- Three reference tool operations (`memory.remember`, `memory.forget`, `memory.search`) give the model an explicit path that isn't gated by whatever the automatic recall pass happened to surface that turn.

`memory.remember`'s `Invoke` MUST decide `Record` vs. `UpdateRecord` by checking whether the model-supplied (or derived) `id` already exists. Before concluding "no existing record, create new," it MUST also perform a fuzzy near-match check (e.g. string similarity against existing record titles/ids within the same `type`/`scope`). If a close-but-not-exact match is found, `Invoke` MUST NOT silently create a duplicate or silently update the near-match — it returns a tool result (not an error; this isn't a failure) listing the near-match candidate(s) and asking the model to confirm which was intended, either by re-invoking `memory.remember` with the corrected `id` or by explicitly proceeding with a new one. This reuses the ordinary tool-result-feeds-back-into-the-model-turn pattern rather than inventing a new interactive escalation — no `interactive`-kind tool involvement is needed, since the model, not a human, resolves the ambiguity on its next turn.

Content-quality guidance (what's worth remembering, what isn't, promoting verbose entries into topic files) is deliberately **not** encoded as a protocol constraint — it belongs in `memory.remember`'s tool `description` field, shown to the model at tool-selection time, the same way any tool's behavioral guidance lives in its description rather than in wire-protocol rules.

## Render

Memory providers MAY implement `Render` per the general Emit→Render→Paint pipeline ([`architecture.md`](../architecture.md#emit--render--paint-pipeline)), returning the `RenderTree` formally defined in [`frontend/render-tree.md`](../frontend/render-tree.md). A reference implementation might render a `pending` record specially (e.g. a review-inbox UI element distinct from ordinary recall) — this is exactly the kind of case where custom rendering matters more than the generic fallback. If not implemented, the kernel falls back to its generic default rendering.

`RenderRequest` carries `schema_version` alongside the opaque `payload` — see [`frontend/render-tree.md#schema-versioning`](../frontend/render-tree.md#schema-versioning) for what the value means and how a `Render` implementation is expected to use it.

## `Describe`

```text
Describe(DescribeRequest) -> DescribeResponse
```

MUST be implemented. Reports this plugin build's own identity — `{name, version, source, category, protocol_version}` via `common.v1.ProducerRef` — independent of any lock-file entry. This is how the kernel identifies a `dev_overrides`-resolved binary, which has no `provider "<name>" { ... }` entry to read identity from the normal way: see [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry) for the canonical explanation, shared verbatim across every plugin category that gains this RPC in this protocol revision.
