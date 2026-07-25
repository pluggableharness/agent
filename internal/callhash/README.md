# callhash

`callhash` computes the deterministic call-hash used by the doom-loop detector and the tool-call concurrency scheduler.

## Purpose

Two distinct kernel subsystems need to canonicalize and serialize tool call arguments:

1. **Doom-loop detection** — detects when a model is stuck repeating the same call. Uses `hash(tool_name, canonicalize(input_json))` to identify repeated calls; a threshold of consecutive identical hashes triggers a recovery/final-answer turn (see [`docs/specifications/agent-loop/turn-algorithm.md#doom-loop-detection`](../../docs/specifications/agent-loop/turn-algorithm.md#doom-loop-detection)).

2. **Tool-call concurrency** — schedules concurrent execution of tool calls within a turn. A `ConcurrencySpec` declares which input fields form a concurrency key; the kernel computes `(provider_name, tool_name, value(key_fields))` and ensures calls sharing identical keys execute sequentially (see [`docs/specifications/tool/data-types.md#concurrencyspec`](../../docs/specifications/tool/data-types.md#concurrencyspec)).

Both operations depend on **one canonical JSON encoding** of structured values. Having two independent serialization implementations risks divergence (especially with Go's map iteration order non-determinism), which would silently break replay-time hash recomputation and cause the concurrency scheduler to misidentify conflicting keys.

## API

- `Call(toolName, args)` — computes the SHA-256 hash of a tool call for doom-loop detection.
- `Fields(args, keyFields)` — extracts and canonicalizes the named key-field values for concurrency key formation.
- `Canonical(v)` — produces the single deterministic JSON encoding used by both functions above.

## Determinism guarantees

- **Map iteration order independence** — Go maps iterate in random order; `Canonical` sorts object keys before serialization, guaranteeing byte-identical output regardless of insertion order.
- **Idempotency** — re-canonicalizing an already-canonical value produces identical bytes.
- **Structural equivalence** — absent and explicit `null` values are serialized identically (as `null`), so the concurrency scheduler treats "field omitted" and "field: null" as the same concurrency key.

See [`docs/specifications/determinism.md`](../../docs/specifications/determinism.md) for the broader replay-determinism contract this package upholds.
