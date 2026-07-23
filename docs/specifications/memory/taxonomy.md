# Memory provider — record taxonomy

The record taxonomy is **fixed at the protocol level**, not provider-defined. This is the memory category's central, most-detailed concern, hence its own file rather than folding into [`data-types.md`](data-types.md).

The four types are a general, non-coding-specific organizing principle — roles and preferences, not implementation details — chosen to generalize across projects rather than being left for each provider to define independently.

## `MemoryType`

```protobuf
MemoryType = enum { user, feedback, project, reference }
```

A record MUST declare exactly one `MemoryType`; it is immutable after creation — [`protocol.md`](protocol.md#record-updaterecord-deleterecord-the-write-side)'s `UpdateRecord` MUST NOT change a record's type. Recategorizing means `DeleteRecord` followed by a new `Record` call.

### `user`

The subject's role, goals, responsibilities, and knowledge. Tailors future behavior to who they are and what they already know — durable across every project, not tied to any one piece of work.

### `feedback`

Guidance on how to approach work, captured from both corrections ("stop doing X") and confirmations ("yes, keep doing that"). Both directions matter equally: a provider that only records corrections will drift away from validated approaches over time, since a confirmation is what tells a future session "this pattern already survived review, don't second-guess it."

### `project`

Ongoing work, goals, decisions, and incidents not otherwise derivable from code or git history — the record type for facts that live in the gap between "the code already says this" and "no one wrote this down anywhere durable." Decays faster than the other three types: yesterday's in-progress decision is often obsolete a week later in a way a `user` record's role description rarely is. A provider SHOULD weight recency more heavily for this type when ranking recall results under budget pressure (see [`protocol.md#relevance-ranking`](protocol.md#relevance-ranking)).

### `reference`

Pointers to where information lives in external systems — an issue tracker, a dashboard, a channel — not the information itself. A `reference` record is a durable address, not a durable copy; it stays useful precisely because it doesn't try to duplicate content that changes out from under it.

## Scope vs. type

`MemoryScope` ([`data-types.md#memoryscope`](data-types.md#memoryscope)) is an orthogonal axis to `MemoryType`: type answers "what kind of knowledge is this," scope answers "who gets to recall it." Both are fixed at the protocol level and both are immutable per record once set. A `user`-type record is very commonly `global`-scoped (the subject's role doesn't change per project), while a `project`-type record is very commonly `project`-scoped — but the protocol doesn't couple them: a `feedback` record about how to review pull requests in one specific repository is `feedback` + `project`, not `feedback` + `global`, and nothing prevents a provider from supporting that combination.

## Cross-reference links between records

Records reference one another structurally via `[[name]]` syntax inside `content`, kernel-parsed into `MemoryRecord.links` at write time — see [`protocol.md#structural-name-cross-reference-links`](protocol.md#structural-name-cross-reference-links) for the parsing/rendering rules and [`examples.md#a-worked-cross-reference-example`](examples.md#a-worked-cross-reference-example) for a full worked pair of linked records (a `project`-type record referencing a `reference`-type architecture pointer via `[[project-x-architecture]]`). Links are a taxonomy-crossing mechanism — nothing restricts a link's source and target to share a `MemoryType` or `MemoryScope`.
