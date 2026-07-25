# internal/tooldispatch

The turn-level tool-call scheduler and `Invoke` client, per [`docs/specifications/agent-loop/turn-algorithm.md#turn-level-tool-call-concurrency`](../../docs/specifications/agent-loop/turn-algorithm.md#turn-level-tool-call-concurrency) and [`docs/specifications/tool/protocol.md#invoke`](../../docs/specifications/tool/protocol.md#invoke).

This package serves **both** `RunTurn` step 9 (`data_source`, executes freely) and step 12 (`resource`, after plan approval) with the same scheduling mechanism — the turn algorithm is explicit that this is "one mechanism for both, not two separate rules." Approval gating (which calls even reach `Execute`) is decided upstream, by `internal/plangate` (not built yet); this package only ever sees calls it has already been told to run.

## What this package does

- `tooldispatch.go` — `Call`/`Outcome` (this package's own types, declared independently of `internal/plangate`), `EventSink`, `Config`, `Scheduler`, `New`, the provider/key semaphore maps, and `concurrencyKey`/`acquireLocks` — the `ConcurrencySpec` scheduling mechanics.
- `execute.go` — `Execute`: the concurrent (or, under `Config.SerializeAll`, sequential) fan-out over `resource`/`data_source` calls. Persists a `tool_call` event before and a `tool_result` event after every call, applies the per-operation `default_timeout`, enforces `output_schema` strictly, classifies a crashed plugin process and feeds `Config.Breaker`, and records `internal/telemetry`'s tool-call span/metrics.
- `interactive.go` — `ExecuteInteractive`: the strictly-sequential path for `interactive`-kind calls, via `internal/interactive.Resolver`, which never touches `ConcurrencySpec`, `Config.Breaker`, or a per-call timeout at all.

## Concurrency model

Per [`tool/data-types.md#concurrencyspec`](../../docs/specifications/tool/data-types.md#concurrencyspec), every `resource`/`data_source` call computes a scheduling key from its `ConcurrencySpec`:

- `safe: false`, or no `ConcurrencySpec` declared at all — a provider-wide exclusive lock (this call excludes every other call, safe or not, against the same provider).
- `safe: true`, no `key_fields` — a shared provider-wide slot only; the operation asserts no two of its own calls can ever conflict, so distinct calls run fully concurrently.
- `safe: true`, `key_fields: [...]` — a shared provider-wide slot **plus** a per-key exclusive lock, keyed by `(provider_name, tool_name, value(key_fields))` via `internal/callhash.Fields`. Calls sharing an identical key serialize; calls with distinct keys run concurrently.

Implemented as two semaphore families, one `golang.org/x/sync/semaphore.Weighted` per provider (capacity `1<<20`; an exclusive acquire takes the whole capacity, a shared acquire takes weight 1) and one per key (capacity 1). **Every acquire takes the provider semaphore first and the key semaphore second, always** — see `Scheduler`'s doc comment in `tooldispatch.go` for why this one rule is what makes the scheme deadlock-free.

`Config.SerializeAll` bypasses all of this — set true for a model whose `ModelSpec.supports_parallel_tool_calls` is false, it collapses `Execute` to one call at a time in input order, ignoring every call's own `ConcurrencySpec`.

## What this package does NOT do

- It does not decide *which* calls are allowed to run — that's `internal/plangate`'s job (plan/apply gate, policy precheck), upstream of anything reaching `Execute`/`ExecuteInteractive`. This package MUST NOT import `internal/plangate` — see `CLAUDE.md`.
- It does not route a tripped `Config.Breaker` through the limit-reached graceful-degradation path — it only records the crash and surfaces the trip signal via `Outcome.Error.Details["breaker_tripped"]`. Deciding what to do about a tripped provider is a future `internal/turn`'s job.
- It does not accumulate or render `output_chunk`/`progress`/`partial_result` content — `internal/streamaccum` owns that, for whatever future consumer needs the intermediate stream content rather than just the terminal outcome.

## How it fits in

Neither call site is built yet. When `internal/turn` exists, it will: split a turn's `tool_use` blocks by `kind` (turn-algorithm.md step 8), run `interactive` calls through `ExecuteInteractive`, run `data_source` calls through `Execute` directly (step 9), build a plan from `resource` calls, dispatch `plan-ready`, and run the approved subset of `resource` calls through the *same* `Scheduler.Execute` (step 12) — one `Scheduler` per session, shared across every turn.
