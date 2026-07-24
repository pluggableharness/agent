# Memory provider — data types

## `MemoryCapabilities`

```protobuf
MemoryCapabilities {
  default_token_budget    int          // MUST — same convention as the context
                                        // provider's reserved token_budget config field
  supported_types         []MemoryType // MUST — see taxonomy.md; a provider MAY handle
                                        // only a subset of the fixed taxonomy
  supported_scopes        []MemoryScope // MUST — a provider MAY handle only a subset
                                         // (e.g. project-only)
  ratification_supported  bool         // MUST, default false — see protocol.md#ratification-optional
  slash_commands           []common.v1.PromptExpansionSpec  // MAY — static template-expansion
                                                    // commands only; see
                                                    // frontend/frontend-protocol.md#slash-commands.
                                                    // A direct-invoke command is declared by a
                                                    // slashcommand.v1 provider instead, never here
  config_schema            ConfigSchema  // MUST — decoded per configuration/blocks-reference.md
  supported_hook_points    []common.v1.HookPoint  // MAY be empty — which hook points this
                                                    // provider subscribes DispatchHook to,
                                                    // see protocol.md#write-triggers
}
```

## `MemoryScope`

```protobuf
MemoryScope = enum { session, project, global }
```

- **`session`** — visible only within the session (and its descendants) that wrote it; not persisted beyond the session tree's lifetime in the sense of being recalled by unrelated future sessions. It's still durably logged to the state backend for audit ([`state-backend.md`](../state-backend.md)), just not part of ordinary cross-session recall.
- **`project`** — scoped to the current working directory/project, recalled by any session operating in that project.
- **`global`** — recalled across every project, mirroring a memory system that spans all of a subject's work.

`MemoryScope` MUST be set on every `MemoryRecord` and is immutable after creation, the same as `MemoryType` — see [`taxonomy.md`](taxonomy.md#scope-vs-type). A single provider instance MAY support multiple scopes (storing them under different internal roots, e.g. a file-based provider using `.agent/memory/` for `project` and `$XDG_DATA_HOME/agent/memory/` for `global`) rather than requiring separate provider instances per scope.

## `MemoryType`

```protobuf
MemoryType = enum { user, feedback, project, reference }
```

Fixed at the protocol level, not provider-defined. Full definitions, rationale, and recency-weighting guidance live in [`taxonomy.md`](taxonomy.md) — this category's dedicated file for its central, most-detailed concern.

## `RecallRequest` / `MemoryRecord`

```protobuf
RecallRequest {
  session_id            string
  turn_id                 string           // ULID, standardized across the whole
                                            // protocol — same treatment as
                                            // context.v1's ContextRequest.turn_id
  token_budget            int              // MUST — resolved the same way a context
                                            // provider's cap is resolved; memory recall
                                            // competes for the SAME budget pool
  model_target            ModelTarget      // MUST — { id, context_window, effective_ceiling };
                                            // lets a provider pass a precise model
                                            // reference into the CountTokens kernel
                                            // callback (kernel-callbacks.md#counttokens)
  files_touched           []string         // MAY be empty
  working_directory       string
  type_filter             []MemoryType     // MAY be empty = all supported types
  scope_filter            []MemoryScope    // MAY be empty = all scopes this provider supports
  include_pending         bool             // MUST default false — see protocol.md#ratification-optional
}

RecallResponse {
  records  []MemoryRecord   // this provider's own relevance order
}

MemoryRecord {
  id         string        // slug, unique within this provider — kernel-enforced,
                            // see protocol.md#record-updaterecord-deleterecord-the-write-side
  type       MemoryType    // immutable after creation
  scope      MemoryScope   // MUST; immutable after creation, same as type
  title      string
  content    []ContentBlock  // text-only in v1, same constraint as the context provider's
                              // ContextSection content
  tokens     int             // computed via the CountTokens kernel callback
                              // (kernel-callbacks.md#counttokens), never a provider-local
                              // heuristic
  status     enum { canonical, pending }
  links      []string        // MUST — record IDs this record references, kernel-parsed
                              // from "[[name]]" syntax; see protocol.md#structural-name-cross-reference-links
  created_at, updated_at  timestamp
  provenance  Provenance      // kernel-populated at Record time, immutable — see
                               // #provenance below
  relevance_score  double?    // [0, 1], set ONLY on Recall/ListRecords responses,
                               // never persisted — see #relevance_score below
}
```

## `Provenance`

```protobuf
Provenance {
  source_session_id  string   // the session that produced this record
  source_turn_id      string? // the turn (ULID) within source_session_id that
                               // produced this record, when known
  recorded_by          string // producing plugin's declared name, or the
                               // reference tool path that wrote it (e.g.
                               // "memory.remember")
}
```

Kernel-populated at `Record` time and immutable thereafter — never provider-supplied, never mutated by `UpdateRecord`. The memory category's [`README.md`](README.md) already frames provenance as a first-class concern; `MemoryRecord.provenance` is where the record shape finally carries it, so any consumer (a ratification review UI, an audit trail) has one place to look rather than reconstructing "who wrote this" from write-time logs.

## `relevance_score`

`MemoryRecord.relevance_score` is this record's recall-time relevance, in `[0, 1]`. It is set only on `Recall` and `ListRecords` responses — never on a `Record`/`UpdateRecord` request or response, and never persisted alongside the record itself. It exists so the kernel can merge multiple memory providers' results under one shared `token_budget` using a comparable figure, rather than relying on each provider's internal, incomparable ordering. A provider that sets `relevance_score` MUST normalize it to `[0, 1]` — scores from two different providers are only meaningfully comparable if both normalize to the same range; a provider that doesn't compute a meaningful relevance figure SHOULD leave the field unset rather than fabricating a value.

`RecallRequest.token_budget` exceeded by the candidate record set even after this provider's own truncation is a `budget_exceeded` error — see [`#memoryerror`](#memoryerror) below.

## The write side

```protobuf
RecordRequest {
  type      MemoryType   // MUST
  scope     MemoryScope  // MUST
  id        string?      // MAY be author-suggested (a slug); if omitted, the
                          // provider derives one from content — the kernel
                          // disambiguates collisions with a numeric suffix
                          // rather than overwriting or rejecting
  title     string
  content   []ContentBlock
}

RecordResult {
  id      string          // MUST — the final assigned slug
  status  enum { canonical, pending }
}

UpdateRecordRequest {
  id       string         // MUST match an existing record
  title    string?
  content  []ContentBlock  // MUST — replaces existing content wholesale, not a patch
}

DeleteRecordRequest { id string }
DeleteResult { deleted bool }
```

`RecordResult` is a reusable domain type shared across `Record`'s, `UpdateRecord`'s, and `ApproveRecord`'s responses (each keeps its own per-RPC response message wrapping the same `RecordResult` shape, not a literally-shared RPC response type). `DeleteResult` is the equivalent reusable shape for `DeleteRecord` and `RejectRecord`.

## `ListRecords` / `GetRecord`

```protobuf
ListRecordsRequest {
  type_filter    []MemoryType    // MAY be empty = all supported types
  scope_filter   []MemoryScope   // MAY be empty = all scopes this provider supports
  status_filter  RecordStatus?   // unset = both canonical and pending are eligible —
                                  // PENDING records ARE listable here, unlike Recall's
                                  // include_pending gate; see protocol.md#listrecords--getrecord
  page_size      int
  page_token     string          // opaque continuation token; empty on the first page
}

ListRecordsResponse {
  records          []MemoryRecord   // this page's records
  next_page_token  string           // empty when this is the last page
}

GetRecordRequest { id string }

GetRecordResponse {
  record  MemoryRecord
}
```

`GetRecord` MUST fail with a `MemoryError{category: not_found}` for an unknown `id`, the same convention as `UpdateRecord`/`DeleteRecord`/`ApproveRecord`/`RejectRecord`.

## `MemoryError`

```protobuf
MemoryError {
  category    MemoryErrorCategory
  message     string
  retryable   bool
}

MemoryErrorCategory = enum {
  not_found                 // UpdateRecord/DeleteRecord/ApproveRecord/RejectRecord
                             // referenced an id that doesn't exist
  invalid_type               // Record specified a MemoryType this provider doesn't
                             // support (absent from GetCapabilities.supported_types)
  ratification_unsupported  // ApproveRecord/RejectRecord called against a
                             // provider with ratification_supported: false
  budget_exceeded            // Recall's returned records exceed token_budget — same
                             // MUST-self-truncate principle as the context provider's
                             // budget handling
  source_unavailable         // backend storage unreachable at call time
  unknown
  invalid_scope               // Record specified a MemoryScope this provider doesn't
                             // support (absent from GetCapabilities.supported_scopes) —
                             // the scope-taxonomy mirror of invalid_type above
}
```

See [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy) for the full error-handling table, including the kernel's expected reaction per category.
