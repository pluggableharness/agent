# Memory provider — examples

## The wire protocol

The service declaration and its two taxonomy enums, illustrating the wire-level shape of the protocol:

```protobuf
service MemoryService {
  // GetCapabilities reports what this provider supports before any other
  // RPC is issued. Unary.
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);

  // Configure decodes this provider's agent.hcl config block, per the
  // same schema-to-cty bridge contract as the rest of this spec series.
  // Unary.
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);

  // Recall is the read side: it fires at context-assemble time, competing
  // for the same token budget pool as context providers, and returns the
  // records this provider judges relevant. Unary.
  rpc Recall(RecallRequest) returns (RecallResponse);

  // Record is the write side: it creates a new record. Unary.
  rpc Record(RecordRequest) returns (RecordResponse);

  // UpdateRecord replaces an existing record's title/content wholesale
  // (not a patch). MUST fail with a structured MemoryError if `id`
  // doesn't match an existing record, rather than silently no-op'ing.
  // Unary.
  rpc UpdateRecord(UpdateRecordRequest) returns (UpdateRecordResponse);

  // DeleteRecord removes an existing record. MUST fail with a structured
  // MemoryError if `id` doesn't match an existing record, rather than
  // silently no-op'ing. Unary.
  rpc DeleteRecord(DeleteRecordRequest) returns (DeleteRecordResponse);

  // ApproveRecord transitions a record from PENDING to CANONICAL. MAY be
  // implemented; a provider declaring ratification_supported = true MUST
  // implement it. Unary.
  rpc ApproveRecord(ApproveRecordRequest) returns (ApproveRecordResponse);

  // RejectRecord discards a pending draft entirely — not a soft delete.
  // MAY be implemented, under the same ratification_supported = true
  // requirement as ApproveRecord. Unary.
  rpc RejectRecord(RejectRecordRequest) returns (RejectRecordResponse);

  // Render returns this provider's own RenderTree for its content (e.g. a
  // review-inbox view distinct from ordinary recall), in place of the
  // kernel's generic fallback. MAY be implemented. Unary.
  rpc Render(RenderRequest) returns (RenderResponse);
}

enum MemoryType {
  MEMORY_TYPE_UNSPECIFIED = 0;
  MEMORY_TYPE_USER = 1;
  MEMORY_TYPE_FEEDBACK = 2;
  MEMORY_TYPE_PROJECT = 3;
  MEMORY_TYPE_REFERENCE = 4;
}

enum MemoryScope {
  MEMORY_SCOPE_UNSPECIFIED = 0;
  MEMORY_SCOPE_SESSION = 1;
  MEMORY_SCOPE_PROJECT = 2;
  MEMORY_SCOPE_GLOBAL = 3;
}
```

Note that every enum carries an explicit `_UNSPECIFIED = 0` zero value — a caller that forgets to set `type` or `scope` gets a named "unspecified" state, never a silently-valid-looking value like `MEMORY_TYPE_USER` by accident of field omission.

## A worked `Recall`/`Record` sequence

A session working in a project recalls memory at `context-assemble`, then later in the same session the model writes a new `project`-type record via the `memory.remember` reference tool ([`#write-triggers-reference-tools`](#write-triggers-reference-tools)):

```text
→ RecallRequest{
    session_id: "sess_042", turn_number: 3,
    token_budget: 1500,
    model_target: { id: "claude-opus-5", context_window: 500000, effective_ceiling: 480000 },
    working_directory: "/home/user/code/acme-widgets",
    scope_filter: [MEMORY_SCOPE_PROJECT, MEMORY_SCOPE_GLOBAL],
    include_pending: false,
  }

← RecallResponse{
    records: [
      { id: "user-role", type: MEMORY_TYPE_USER, scope: MEMORY_SCOPE_GLOBAL,
        title: "Operator role", tokens: 42, status: canonical, links: [] },
      { id: "deploy-pipeline-migration-in-progress", type: MEMORY_TYPE_PROJECT,
        scope: MEMORY_SCOPE_PROJECT, title: "Migrating the release pipeline to the new deploy tool",
        tokens: 88, status: canonical, links: ["deploy-pipeline-runbook"] },
    ],
  }

// ... several turns later, the model decides a new project fact is worth
// persisting, and invokes the memory.remember reference tool:

→ RecordRequest{
    type: MEMORY_TYPE_PROJECT, scope: MEMORY_SCOPE_PROJECT,
    id: "deploy-pipeline-migration-done",
    title: "Deploy pipeline migration complete",
    content: [{ text: "New deploy tool is live in production; old pipeline decommissioned. Rollback steps are documented in the runbook." }],
  }

← RecordResponse{ result: { id: "deploy-pipeline-migration-done", status: canonical } }
```

The kernel adapts each `RecallResponse` record into a `ContextSection` before merging it into the assembled prompt — see [`protocol.md#kernel-side-translation-into-context-assembly`](protocol.md#kernel-side-translation-into-context-assembly).

## A worked cross-reference example

Two records, one referencing the other via `[[name]]` syntax in its content:

```text
MemoryRecord{
  id: "deploy-pipeline-runbook",
  type: MEMORY_TYPE_REFERENCE,
  scope: MEMORY_SCOPE_PROJECT,
  title: "Deploy pipeline runbook",
  content: [{ text: "Canonical runbook for the release pipeline lives at https://wiki.example/acme/deploy-runbook — check there before re-deriving rollback steps from source." }],
  links: [],
}

MemoryRecord{
  id: "deploy-pipeline-migration-in-progress",
  type: MEMORY_TYPE_PROJECT,
  scope: MEMORY_SCOPE_PROJECT,
  title: "Migrating the release pipeline to the new deploy tool",
  content: [{ text: "Migrating from the legacy CI pipeline to the new deploy tool. See [[deploy-pipeline-runbook]] for rollback steps and canonical pipeline documentation." }],
  links: ["deploy-pipeline-runbook"],
}
```

The kernel parsed the `[[deploy-pipeline-runbook]]` occurrence out of the second record's `content` at `Record` time and populated its `links` field automatically — the memory provider never had to implement that parsing itself. The link target already existed in this example, but it wouldn't have needed to: a forward reference to a not-yet-created record is valid, and the kernel doesn't reject the write over it (it MUST make the dangling link queryable instead, per [`protocol.md#structural-name-cross-reference-links`](protocol.md#structural-name-cross-reference-links)). At render time, the kernel resolves `[[deploy-pipeline-runbook]]` into a `link` `RenderNode` ([`frontend/render-tree.md`](../frontend/render-tree.md)) pointing at the target record, so a frontend can render it as an actual clickable/navigable link rather than literal bracket text.

## Write triggers (reference tools)

Both an autonomous, hook-driven mechanism and an explicit, model-invoked mechanism exist side by side, giving automatic capture and an explicit command-driven path equal footing.

### Autonomous, hook-driven

A memory provider is implicitly subscribed to `post-response` (`observe` mode, fires every turn) and `session-end` (fires once). Nothing in the protocol prescribes *when* within that stream a provider decides to call its own internal write logic — a "10+ message session" heuristic is a candidate pattern a reference implementation might use, not a protocol requirement. `session-end` firing unconditionally gives every provider a guaranteed last chance to persist something even if its own turn-by-turn heuristic never triggered mid-session.

### Explicit, model-invoked

Three reference tool operations, typically implemented by the memory provider's own plugin process registering as a tool provider too (per [`README.md`](README.md#transport--lifecycle)):

| Tool | `kind` | `risk` | Calls back into |
|---|---|---|---|
| `memory.remember` | resource | moderate | `Record` or `UpdateRecord` (if `id` matches an existing record) |
| `memory.forget` | resource | moderate | `DeleteRecord` |
| `memory.search` | data_source | read_only | `Recall`, with an explicit free-text query the model supplies (not the passive per-turn `files_touched`/`working_directory` hints `Recall`'s ordinary path uses) |

`risk: moderate` — not `low` — because a memory write's blast radius, unlike a purely harness-internal operation, isn't confined to this session: it persists indefinitely and influences every future session that recalls it.

`memory.search` exists specifically because passive per-turn recall is necessarily budget-constrained and heuristic; an operator or the model explicitly asking "check your memory for X" needs a path that isn't gated by what the automatic recall pass happened to surface that turn.

See [`protocol.md#write-triggers`](protocol.md#write-triggers) for the full `memory.remember` decision logic, including the required fuzzy near-match confirmation step before creating a new record.
