# internal/callhash — agent notes

This is a pure-domain package implementing deterministic JSON canonicalization and hashing. It has zero logging, zero telemetry, and zero I/O.

## The one-encoder rule

This package exists specifically because two separate kernel subsystems need to canonicalize tool-call arguments: doom-loop detection and tool-call concurrency scheduling. Both must use the **exact same canonical encoding** — never duplicate it.

If another subsystem needs to canonicalize structpb.Value data, it MUST use `Canonical()` from this package, never implement its own JSON serialization. The replay-determinism contract depends on this.

## Key implementation details

- `Canonical()` and `canonicalValue()` recursively handle structpb values without importing anything except what structpb provides.
- Object keys are sorted before JSON output, eliminating Go map iteration order non-determinism.
- Arrays and lists preserve element order (no sorting).
- Absent struct fields and explicit null values both serialize as `null` (structural equivalence for concurrency key purposes).
- JSON string/number encoding delegates to stdlib `encoding/json` for consistency with Go's JSON standard library.

## Testing

- ~95% statement coverage with table-driven tests.
- Fuzz tests exercise stability of canonicalization and idempotency.
- No integration tests or dependencies on real backends.
