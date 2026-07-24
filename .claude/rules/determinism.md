---
paths:
  - "**/*.go"
---

# Replay determinism

The state backend (`docs/specifications/state-backend.md`) makes a strong promise:
an old session replays byte-for-byte the same way every time, using the exact
plugin version that produced each event. Code anywhere in the kernel that
touches ordering, hashing, or serialized output must uphold that promise —
a "mostly deterministic" implementation silently breaks replay.

## Ordering

- **`sequence` is the only ordering authority**, never wall-clock time.
  `docs/specifications/state-backend.md#ordering--concurrency` makes this
  explicit: `sequence` is an `INTEGER PRIMARY KEY AUTOINCREMENT` column, not
  a `timestamp` column. Any code that orders events, renders a transcript,
  or reconstructs a session tree sorts by `sequence`, full stop. A
  `time.Now()` comparison anywhere in that path is a bug.
- Where parallel `data_source` calls (`docs/specifications/agent-loop/turn-algorithm.md`)
  complete out-of-wall-clock-order, their events still get sequential
  `sequence` values in commit order — the code that assigns `sequence` is
  the kernel's sole-writer sqlite path
  (`docs/specifications/state-backend.md#ordering--concurrency`), never a
  plugin, never a client-generated timestamp.

## Serialization

- No serialized-and-persisted output (event payloads, `Render()` input, cost
  ledger rows, plan-item audit rows) may depend on Go map iteration order.
  Where a map's contents must appear in serialized output, sort the keys
  first. This applies recursively: a struct containing a map, marshaled to
  JSON for storage, needs the same care as marshaling the map directly.
- `Render()` output is derived, never persisted
  (`docs/specifications/state-backend.md#the-kind-enum`) — do not add a code
  path that caches a rendered tree to disk "for performance."
  If replay needs to be fast, that's solved by caching the *input* to
  `Render()`, not memoizing its output past the current process.

## The fallback token heuristic

`docs/specifications/kernel-callbacks.md#the-fallback-heuristic` specifies
exactly one fallback formula when a model provider doesn't implement
real-tokenizer `CountTokens`:
`ceil(utf8_byte_length / 4)`. This is deliberately the *only* fallback
formula in the codebase:

- Do not add a second, "smarter" heuristic for a specific content type
  (code, JSON, non-Latin scripts) — the spec rejected content-type-aware
  heuristics on purpose, pushing accuracy upstream to providers that
  implement real tokenization instead.
- The formula must produce the identical result regardless of caller —
  implement it once, in one shared location (likely `internal/tokencount/`
  or equivalent under the kernel-callback implementation), and have every
  caller use that single function. Two call sites computing
  `ceil(len(s)/4)` slightly differently (byte length vs rune count, for
  instance) is exactly the kind of divergence that breaks replay-time cost
  recomputation.

## Cost and budget rollup

Cost (`docs/specifications/model/protocol.md#cost-computation`) and depth
budget (`docs/specifications/agent-loop/subagents.md#depth-limits`) both roll
up a session tree the same way: computed and persisted at usage-event time,
not recomputed lazily on read. Code that reports a session's total cost or
remaining budget reads the persisted rollup — it does not re-walk the event
log and re-sum on every request. If the rollup and a fresh re-sum ever
disagree, that's a bug in the write path, not something to paper over by
preferring the read-time computation.
