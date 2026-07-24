# State backend

Unlike every other document in this series, this is **not** a plugin protocol. The state backend is a kernel foundation: not pluggable, not swappable, not configurable. Every conforming implementation of this harness stores session state exactly this way. There is no plugin author to give SHOULD/MAY latitude to here — nearly everything below is MUST.

This document covers **durability, replay, and audit** — persisting what happened so it can be reconstructed later. It does **not** cover live transport: a live session's events reach the frontend via the frontend provider's genuinely bidirectional `Attach` stream ([`frontend/README.md`](frontend/README.md#session-scope--multi-attach)) and reach widgets via their own `Attach` push stream ([`frontend/widget-protocol.md`](frontend/widget-protocol.md)), neither of which touches sqlite. Writes to the database happen in parallel with — not instead of — live delivery; nothing needs to poll the database to render an active session. See [`architecture.md#state-backend`](architecture.md#state-backend) for how this fits into the surrounding system narrative (the event envelope sketch, the "you'll need to install X to re-render this" preflight check); this document is the full formal schema that narrative links down to.

## File layout

```text
$XDG_STATE_HOME/agent/sessions/<session_id>.sqlite
```

See [`architecture.md#xdg-layout`](architecture.md#xdg-layout) for the general XDG table; this is the one state-backend-specific path in it. One file per session, no exceptions, no configuration option to change this. `session_id` MUST be a sortable identifier (a ULID or equivalent timestamp-prefixed scheme) — since [cross-session queries](#cross-session-queries) below establish there is no separate cross-session index, a plain `ls` of the sessions directory needs to already be in chronological order without opening a single file.

## Ordering & concurrency

- Every table's primary ordering key is an `INTEGER PRIMARY KEY AUTOINCREMENT` `sequence` column, **not** a timestamp. Wall-clock timestamps are stored alongside for display/reference but are explicitly **not** ordering-authoritative — clock precision and skew make them unsuitable for determining replay order when multiple events land in close succession (e.g. parallel `data_source` calls, see [`agent-loop/turn-algorithm.md`](agent-loop/turn-algorithm.md)). `sequence` is the only ordering authority anywhere in the kernel; comparing wall-clock timestamps in any replay or ordering decision is a protocol violation.
- The kernel is the **sole writer** to any given session's file — plugins never get direct file access; they call `Emit` (a kernel RPC, [`kernel-callbacks.md`](kernel-callbacks.md)), and the kernel does the actual write. There is no cross-process write contention to solve, only the kernel's own internal serialization. Parallel `data_source` calls that complete out of wall-clock order still receive sequential `sequence` values in commit order, since the kernel's sole-writer path is the only thing that ever assigns one.
- The database MUST be opened in **WAL mode**. This isn't for write concurrency (there's only one writer) — it's so a concurrent reader (a CLI `agent sessions show <id>` run in another terminal, a query against a still-running session) doesn't block on or get blocked by the kernel's active writes.

## Schema

Five tables, each owning one concern. `events` is the only fully opaque-payload table — everything else stores kernel-computed, structured data that the kernel already understands and therefore shouldn't have to re-parse out of a JSON blob every time it's queried.

### events

The raw audit log.

```sql
CREATE TABLE events (
  sequence          INTEGER PRIMARY KEY AUTOINCREMENT,
  id                TEXT NOT NULL UNIQUE,   -- stable event identifier, independent of storage
  timestamp         TEXT NOT NULL,          -- wall-clock, display only, not ordering-authoritative
  kind              TEXT NOT NULL,          -- see "The kind enum" below for the authoritative enum
  producer_category TEXT NOT NULL,          -- model | tool | context | memory | frontend | widget
  producer_name     TEXT NOT NULL,
  producer_version  TEXT NOT NULL,
  schema_version    TEXT NOT NULL,
  payload           BLOB NOT NULL           -- opaque; the kernel never inspects this
);
CREATE INDEX idx_events_kind ON events(kind);
CREATE INDEX idx_events_producer ON events(producer_category, producer_name, producer_version);
```

This is the kernel's event envelope, verbatim, as an append-only table — nothing is ever updated or deleted from `events` (retention/pruning, see [Retention & pruning](#retention--pruning), operates at the whole-file level, not row level).

### session_meta

Session summary.

```sql
CREATE TABLE session_meta (
  session_id         TEXT PRIMARY KEY,    -- matches the filename stem
  parent_session_id  TEXT,                -- NULL for a root session
  profile            TEXT NOT NULL,
  status             TEXT NOT NULL,       -- running | completed | error_max_turns |
                                           -- error_max_budget_usd | error_max_wall_clock |
                                           -- cancelled | failed (agent-loop/turn-algorithm.md)
  depth              INTEGER NOT NULL,    -- cached depth-budget value, avoids re-deriving
                                           -- via parent-chain walks when scanning
                                           -- (agent-loop/subagents.md#depth-limits)
  started_at         TEXT NOT NULL,
  ended_at           TEXT                 -- NULL while running
);
```

The one table that isn't append-only — a single row, updated in place as the session progresses. This is the table [Cross-session queries](#cross-session-queries)'s "scan session files directly" approach reads: cheap enough (one row) that opening every session file in the directory to check `parent_session_id`/ `status` is inexpensive even at moderate history size.

`status` is not append-only-monotonic: a `completed` or `cancelled` row MAY transition back to `running` — a re-open, per [`frontend/frontend-protocol.md#resume-and-re-open-semantics`](frontend/frontend-protocol.md#resume-and-re-open-semantics)'s `ResumeSession` — and `ended_at` is cleared back to `NULL` on that transition, the same as for a session's original creation. This is an ordinary in-place `UPDATE` of the existing row, not a new row and not a schema change; `error_max_turns`/`error_max_budget_usd`/`error_max_wall_clock`/`failed` never make this transition — those statuses are terminal and replay-only, never re-opened to `running`. `session.v1.SessionInfo` (the frontend protocol's wire-level read model of this row, see below) carries the same status value either way, so a frontend distinguishes "still on its first run" from "re-opened after completing" only via the sequence of `SessionStatusUpdate` events it has observed, not from `SessionInfo` alone.

`session.v1.SessionInfo` — the message `frontend/frontend-protocol.md`'s `SessionCreated`/`SessionAttached`/`SessionList` `ServerEvent` variants carry — mirrors this table's columns field-for-field (`session_id`, `parent_session_id`, `profile`, `status`, `depth`, `started_at`, `ended_at`), plus one derived field with no column of its own: `cost_usd`, a cheap `SUM(cost_usd)` over this session's `cost_ledger` rows (below), computed at read time rather than cached in `session_meta` — the same indexed-`SUM` query [`cost_ledger`](#cost_ledger)'s own description already establishes as cheap.

### cost_ledger

Structured spend.

```sql
CREATE TABLE cost_ledger (
  sequence           INTEGER PRIMARY KEY AUTOINCREMENT,
  event_sequence     INTEGER NOT NULL REFERENCES events(sequence),
  provider_name      TEXT NOT NULL,
  model_id           TEXT NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_usd           REAL NOT NULL
);
```

Appended once per completed model turn, populated from the same `cost_usd` computation the model provider protocol already requires the kernel to perform at usage-event time ([`model/protocol.md#cost-computation`](model/protocol.md#cost-computation)) — the kernel already has these numbers in hand at write time, so this table costs nothing extra to populate and turns running-total cost tracking (`SUM(cost_usd)`) into a single indexed query instead of a full scan and JSON-parse of every `message`-kind event.

### plan_items

Structured plan/apply audit trail.

```sql
CREATE TABLE plan_items (
  sequence         INTEGER PRIMARY KEY AUTOINCREMENT,
  event_sequence   INTEGER NOT NULL REFERENCES events(sequence),
  turn_id          TEXT NOT NULL,
  tool_call_id     TEXT NOT NULL,
  provider_name    TEXT NOT NULL,
  tool_name        TEXT NOT NULL,
  decision         TEXT NOT NULL,   -- allow | ask | deny
  decided_by       TEXT NOT NULL    -- policy rule name, or the subscriber that decided
);
```

One row per plan item ([`agent-loop/plan-apply-gate.md`](agent-loop/plan-apply-gate.md)), independent of the generic `events` table — "show every call this session denied," "which policy rule fires most" become direct queries instead of payload archaeology.

### producers

Distinct plugin manifest.

```sql
CREATE TABLE producers (
  category             TEXT NOT NULL,
  name                 TEXT NOT NULL,
  version              TEXT NOT NULL,
  first_seen_sequence  INTEGER NOT NULL REFERENCES events(sequence),
  PRIMARY KEY (category, name, version)
);
```

One row per distinct `(category, name, version)` that has ever written to this session. This is what makes the "walk the event log, collect the distinct producer set, diff against installed, report what's missing before touching anything" check ([`architecture.md#state-backend`](architecture.md#state-backend)) a fast primary-key scan of a small table instead of a `DISTINCT` query over potentially thousands of `events` rows.

## The kind enum

This is the authoritative, complete enumeration of `events.kind` — the source [`kernel-callbacks.md`](kernel-callbacks.md) restates rather than re-derives. Like every enum in this system, `kind`'s zero value (`KIND_UNSPECIFIED`) is never valid on a persisted event — the kernel is the sole writer ([`architecture.md#state-backend`](architecture.md#state-backend)), so this MUST NOT ever appear in a written row; its only purpose is making an uninitialized value detectable rather than silently indistinguishable from a real kind.

```protobuf
kind = enum {
  message               // a completed model turn's accumulated canonical
                         // message (model/data-types.md#canonical-message);
                         // usage/cost figures are embedded in this payload
                         // and drive cost_ledger above
  tool_call
  tool_result
  plan                   // a built Plan, pre plan-ready dispatch; drives plan_items above
  apply                  // post-apply outcome (the agent loop's post-apply hook)
  context_contribution   // a context provider's Contribute output
                         // (context/protocol.md#contribute-the-context-assemble-rpc),
                         // OR a memory provider's Recall output after kernel
                         // translation (memory/protocol.md#kernel-side-translation-into-context-assembly)
  memory_write
  memory_update
  memory_delete
  hook_error             // kernel-synthesized when a transform or veto hook
                          // subscriber fails
                          // (agent-loop/hook-dispatch.md#subscriber-error-handling);
                          // payload shape is
                          // pluggableharness.agent.hook.v1.HookError,
                          // wrapped by the forthcoming event.v1 package's
                          // HookErrorEvent
}
```

`hook_error` is the one `kind` the kernel writes on a subscriber's behalf rather than in response to that subscriber's own `Emit` call — a hook subscriber that just failed can't be relied on to call `Emit` itself; the kernel detects the failure during dispatch and persists the event directly. `producer_category`/`producer_name`/`producer_version` still identify the failing subscriber (`HookError.subscriber`, a `ProducerRef`), not the kernel itself — see [`kernel-callbacks.md#emit`](kernel-callbacks.md#emit) for how every other `kind` is written by the producing plugin's own callback connection.

Three things deliberately do **not** get their own `kind`:

- **Usage/cost** is not a separate kind — it's a structured field inside a `message` event's payload, extracted into `cost_ledger` at write time. Giving it a separate event would duplicate data already present in the message that produced it.
- **`Render` output is never persisted at all.** A `RenderTree` is derived on demand from a persisted raw payload, whenever something needs to display it — live or replayed, by calling the (possibly historical, per the "supersedes" versioning rule, [`architecture.md#versioning--schema-drift--supersedes`](architecture.md#versioning--schema-drift--supersedes)) plugin version's `Render`. Persisting rendered output would be redundant with the source payload and would itself need its own versioning story for no benefit.
- **`session_start`/`session_end` are not events either.** Session-level status lives authoritatively in `session_meta` above, updated directly by the kernel. The hook points of the same names still fire for dispatch purposes ([`architecture.md#hook-dispatch-semantics`](architecture.md#hook-dispatch-semantics)); they just don't need a corresponding persisted event, since `session_meta` is the single source of truth for session lifecycle rather than something derived by replaying events.

## Live vs. post-hoc tree walking

`session_meta.parent_session_id` exists for **post-hoc** queries — reconstructing a session tree after the fact (a CLI command, an audit), by scanning files (see [Cross-session queries](#cross-session-queries)). It is **not** how live mechanisms like cost-rollup or depth-budget threading ([`agent-loop/subagents.md#depth-limits`](agent-loop/subagents.md#depth-limits)) work — those operate on the kernel's own in-memory session state while `RunSession` calls are actively executing, via the callback data flow already defined in [`kernel-callbacks.md`](kernel-callbacks.md), and never need to open a sqlite file to find an ancestor. The two mechanisms answer different questions (what's happening right now vs. what happened previously) and deliberately don't share a code path.

Replay is the other live/post-hoc distinction worth being explicit about: replaying an old session means feeding its persisted events back through the same Render/Paint path a live session uses, against the state backend instead of the live loop ([`architecture.md#emit--render--paint-pipeline`](architecture.md#emit--render--paint-pipeline)). [`frontend/frontend-protocol.md#backfill--the-replay-path-not-a-new-subsystem`](frontend/frontend-protocol.md#backfill--the-replay-path-not-a-new-subsystem) is exactly this mechanism wearing its frontend-facing name: a newly attaching (or resuming) frontend's backfill batch is this same event-replay-through-Render walk over `events`, in `sequence` order, using each event's own `producer_category`/`producer_name`/`producer_version`/`schema_version` columns to invoke the correct (possibly historical, "supersedes"-resolved) plugin build's `Render` — not a separate, frontend-specific replay implementation. Telemetry is the one thing that must *not* replay faithfully — trace/span IDs are genuinely non-deterministic and MUST NOT be persisted to `events`, `cost_ledger`, or `plan_items`: this schema deliberately has no `trace_id`/`span_id` column anywhere. Consequently, whatever eventually implements replay MUST select a no-op telemetry driver unconditionally, ignoring whatever driver was configured for the live session. A replayed session re-emitting production telemetry, or attempting to reproduce identical trace/span IDs, would both be wrong in ways this schema's silence on trace/span columns is designed to make structurally impossible.

## Cross-session queries

There is no separate index file. Listing sessions, finding a session's children, or reconstructing a tree means iterating `$XDG_STATE_HOME/agent/sessions/*.sqlite`, opening each file, and reading its `session_meta` row — a single-row lookup per file, not a scan of that session's full `events` history. This is the one deliberate performance trade-off in this design: acceptable at the session-count scale a single operator accumulates, not necessarily at a scale this document assumes never arrives (see [Open questions](#open-questions)).

## Retention & pruning

Sessions are retained **indefinitely by default** — the kernel MUST NOT auto-delete or auto-expire a session file on any timer or count-based policy. Pruning MUST be an explicit operator action (a CLI command), never implicit background garbage collection, consistent with this project's established "never destroy without being asked" posture and directly required by the plugin-cache-eviction rule (a plugin version referenced by a retained session must not be evicted — [`architecture.md#versioning--schema-drift--supersedes`](architecture.md#versioning--schema-drift--supersedes) — an implicit prune would silently violate that).

## Schema migration & corruption recovery

### Schema migration

Every session file MUST carry a `PRAGMA user_version` set to this document's schema revision number at creation. On opening any session file, the kernel MUST compare its `user_version` against the current kernel's expected schema version and, if older, MUST apply an ordered sequence of migration steps (kernel-shipped, one per version increment) before any other operation touches the file — bringing the file's tables up to the current shape in place. A kernel MUST refuse to open a session file with a `user_version` *newer* than it understands (a downgrade scenario), surfacing a clear error rather than attempting to operate on tables it doesn't recognize.

The configuration lock file follows this same posture: it checks a version field via a separate pre-decode pass before reading anything else in the file, refusing a too-new lock file outright (see [`configuration/lock-file.md`](configuration/lock-file.md)).

### Corruption recovery

On opening a session file, the kernel MUST run `PRAGMA integrity_check`. If it fails:

- The kernel MUST attempt automatic recovery, salvaging whatever rows are still readable into a freshly-created replacement file (sqlite's own `.recover` mechanism, or equivalent, is the expected implementation route — not designed at the byte level here).
- The damaged original MUST be renamed (e.g. `<session_id>.sqlite.corrupt`), never deleted — consistent with this project's "never destroy" posture applied everywhere else (git operations, memory pruning, etc.). The recovered file takes the original filename; the damaged original persists alongside it for manual inspection.
- The kernel MUST log/flag that recovery occurred, including which file, so the operator knows this session's data may be incomplete — recovery MUST NOT happen silently.
- If recovery itself fails (the file is unsalvageable even partially), the kernel MUST flag the file as unreadable and move on, consistent with the original "flag as unreadable" fallback — automatic repair is an attempt, not a guarantee.

## Required vs. optional support

| Behavior | Level |
|---|---|
| Not a plugin category — kernel-built-in, no config | MUST |
| One sqlite file per session, fixed XDG path | MUST |
| Sortable `session_id` (ULID-style) | MUST |
| `sequence` column is ordering-authoritative, not `timestamp` | MUST |
| Kernel is sole writer; no cross-process write contention | MUST |
| WAL mode | MUST |
| Five-table schema exactly as specified above | MUST |
| `events.payload` opaque, never inspected by the kernel | MUST |
| `cost_ledger` populated at the same time as the `message` event that produced it | MUST |
| `plan_items` populated at plan-ready time | MUST |
| `producers` updated on first sighting of a new `(category, name, version)` | MUST |
| Complete `kind` enum, no `render`/`session_start`/`session_end` kinds | MUST |
| Live rollup/depth tracking uses in-memory state, not file reads | MUST |
| Cross-session queries scan files; no separate index | MUST (by decision) |
| No implicit retention expiry; pruning is explicit only | MUST |
| `PRAGMA user_version` set at creation, checked/migrated on open | MUST |
| Refuse to open a session file with a newer `user_version` than understood | MUST |
| `PRAGMA integrity_check` on open; automatic repair attempted on failure | MUST |
| Damaged original renamed, never deleted, on recovery | MUST |
| Recovery logged/flagged, never silent | MUST |

## Open questions

- **Scan performance at scale** (see [Cross-session queries](#cross-session-queries)) — acceptable for what a single operator accumulates, not validated against a much larger session count. If this ever becomes a real bottleneck, the fix is a cache/index layered *on top* of file-scanning (derivable from it, never a second source of truth), not a reversal of the "no separate index" decision itself.
- **`cost_ledger`/`plan_items` referencing `events.sequence` via a foreign key within the same file** is straightforward; nothing here addresses whether those tables need `ON DELETE` behavior given `events` is meant to be append-only and never actually deleted from in practice.
