# Memory provider protocol

Covers the **memory provider** category — plugins that persist knowledge across sessions and recall it into future ones. Sibling category to [`model/`](../model/README.md), [`tool/`](../tool/README.md), [`context/`](../context/README.md), and [`frontend/`](../frontend/README.md).

A memory provider does two things: it reads relevant recall into context assembly, and it writes new knowledge worth persisting across sessions. This is a **distinct plugin category with its own protocol**, not a reuse of the context provider's `Contribute` RPC — memory-specific data (record type, scope, provenance, ratification status) stays first-class through a dedicated `Recall` RPC, and the kernel adapts results into `ContextSection`s before merging them into the assembled prompt. See [`protocol.md#recall-the-read-side`](protocol.md#recall-the-read-side).

The underlying storage mechanism — files, sqlite, a vector store, a remote service — is entirely a provider implementation detail. This category is backend-agnostic behind one interface, the same abstraction-over-backend move Terraform makes for state; see [`architecture.md`](../architecture.md#the-seven-provider-categories).

The design draws on patterns seen across coding harnesses — automatic session memory, tiered recall, and inbox-style ratification — while fixing one specific piece, the record taxonomy ([`taxonomy.md`](taxonomy.md)), as a deliberate protocol-level choice rather than leaving it to each provider.

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport) — the standard handshake applies uniformly across all seven provider categories and isn't repeated here.

A memory provider plugin exposes nine RPCs: `GetCapabilities`, `Configure`, `Recall`, `Record`, `UpdateRecord`, `DeleteRecord`, `ListRecords`, `GetRecord`, `Describe`. It MAY additionally implement `ApproveRecord`/`RejectRecord` (the optional ratification pattern, [`protocol.md#ratification-optional`](protocol.md#ratification-optional)) and `Render` ([`protocol.md#render`](protocol.md#render)). All eleven RPCs are unary — unlike the model provider's `StreamCompletion` or the tool provider's `Invoke`, nothing in this category streams.

**A plugin process MAY implement more than one provider-category protocol.** The reference memory provider ([`examples.md#write-triggers-reference-tools`](examples.md#write-triggers-reference-tools)) implements both this protocol and registers as a tool provider (per [`tool/README.md`](../tool/README.md)) for `memory.remember`/`memory.forget`/ `memory.search`, in the same process, with the tool's `Invoke` calling directly into its own `Record` method — no cross-plugin RPC needed. Nothing in this protocol prohibits that; it is simply the natural shape once a category's read/write RPCs and a tool-shaped trigger for the write side turn out to belong to the same plugin.

## Category structure

- [`protocol.md`](protocol.md) — the RPCs: `GetCapabilities`, `Configure`, `Recall`, `Record`/`UpdateRecord`/`DeleteRecord`, `ListRecords`/`GetRecord`, `ApproveRecord`/`RejectRecord`, `Render`, `Describe`.
- [`data-types.md`](data-types.md) — `MemoryCapabilities`, the `MemoryScope` and `MemoryType` enums, `RecallRequest`/`MemoryRecord`, the write-side request/result types, and the `MemoryError` taxonomy.
- [`taxonomy.md`](taxonomy.md) — the fixed record taxonomy in full: what each `MemoryType` means, how it interacts with `MemoryScope`, and why both are fixed at the protocol level rather than left to each provider.
- [`examples.md`](examples.md) — an illustrative wire-format excerpt of the service definition, a worked `Recall`/`Record` sequence, a worked `[[name]]` cross-reference example, and the write-triggers table (autonomous hook-driven vs. explicit model-invoked reference tools).
- [`conformance.md`](conformance.md) — the error taxonomy and the MUST/SHOULD/MAY summary matrix; states plainly that this category carries no open questions of its own.
