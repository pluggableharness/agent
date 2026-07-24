# internal/statebackend

The kernel state backend from [`docs/specifications/state-backend.md`](../../docs/specifications/state-backend.md) — sqlite-per-session persistence for durability, replay, and audit. Unlike the plugin categories, this is a kernel foundation: not pluggable, not configurable, one sqlite file per session at `$XDG_STATE_HOME/agent/sessions/<session_id>.sqlite`.

## What this package does

- `statebackend.go` — `Store`: manages the sessions directory. `Create` makes a new session file (schema applied, `PRAGMA user_version` stamped, initial `session_meta` row inserted) and returns an open `Session`. `Open` opens an existing one, running corruption recovery and schema migration first. `List`/`Children` scan `session_meta` across every file for cross-session queries — there is no separate index.
- `schema.go` — the five-table DDL, reproduced verbatim from the spec, plus the ordered `migrationStep` slice `Open` walks when a file's `user_version` is older than `currentSchemaVersion`.
- `event.go` — `Event`, `CostEntry`, `PlanItem`: the Go mirrors of the `events`, `cost_ledger`, and `plan_items` columns, plus the encode/decode helpers between their proto enum fields and the lowercase TEXT vocabulary the spec stores on disk.
- `session.go` — `Session`'s write path: `AppendEvent`, `AppendMessage`, `AppendPlan`, `SetStatus`, `Close`. The kernel is this file's sole writer; every append runs in one transaction so an event row never exists without its accompanying `cost_ledger`/`plan_items`/`producers` rows.
- `query.go` — `Session`'s read path: `Meta`, `Events` (a sequence-ordered `iter.Seq2[Event, error]` — replay's entry point), `Producers`, `TotalCostUSD`, `CostLedger`, `PlanItems`.
- `integrity.go` — `PRAGMA integrity_check` on every `Open`, and the salvage recovery path when it fails.
- `sessionid.go` — `NewSessionID`/`ValidateSessionID`: session IDs are canonical uppercase ULIDs, sortable chronologically by filename alone.

## Public API sketch

```go
store, err := statebackend.NewStore(dir, statebackend.WithLogger(logger), statebackend.WithTelemetry(prov))

sess, err := store.Create(ctx, statebackend.SessionMeta{SessionID: statebackend.NewSessionID(time.Now()), Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING})
seq, err := sess.AppendEvent(ctx, statebackend.Event{ID: "...", Kind: kernelv1.EventKind_EVENT_KIND_TOOL_CALL, Producer: ref, Payload: b})
err = sess.SetStatus(ctx, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, &endedAt)
err = sess.Close()

sess, err = store.Open(ctx, sessionID)
for ev, err := range sess.Events(ctx) { ... } // sequence-ordered replay
meta, err := sess.Meta(ctx)
metas, err := store.Children(ctx, sessionID)
```

`Session` write methods return `ErrClosed` once `Close` has been called. `Store.Open`/`Store.Create` reject a malformed or non-canonical session ID via `ValidateSessionID`.

## Recovery behavior

On `Open`, `checkIntegrity` runs `PRAGMA integrity_check`. If the file can't be opened at all, or the check reports problems, the damaged original is renamed to `<session_id>.sqlite.corrupt` — never deleted — and salvage begins: a fresh, schema-correct file is built table-by-table from the damaged one, tolerating per-row scan/insert failures (a bad row is skipped, not fatal to the rest of the table). Recovery only counts as successful once the single `session_meta` row is itself salvaged; every other table is best-effort on top of that. A successful recovery installs the new file at the original path and returns an open handle to it, after logging a `WARN` with per-table salvaged/skipped counts — recovery is never silent. If `session_meta` itself can't be salvaged, `Open` returns `ErrUnrecoverable` and the `.corrupt` file is left in place for manual inspection.

## Testing notes

- Unit tests are the default tier (`go-testing.md`) — in-memory sqlite files under `t.TempDir()`, no external fixtures.
- Concurrency-sensitive paths (every write method, corruption recovery) run under `go test -race`, per `.claude/rules/go-testing.md`'s hard requirement for anything touching the state backend.
- `event_fuzz_test.go`'s `FuzzEventRoundTrip` and `sessionid_fuzz_test.go`'s `FuzzValidateSessionID` are the two fuzz targets — event append/scan round-tripping and ULID validation, respectively.
- `integrity_test.go` covers the corruption-recovery path directly: deliberately truncated/corrupted files, partial-table salvage, and the `ErrUnrecoverable` case.
- Replay-adjacent assertions compare `sequence`, never wall-clock time, per `.claude/rules/determinism.md`.
