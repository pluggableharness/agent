# internal/circuitbreaker

Shared per-provider "stop trying" tripping logic used by two independent
call sites:

- the plan/apply gate's denial circuit breaker
  ([`docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials`](../../docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials)),
- the tool scheduler's repeated-plugin-crash circuit breaker
  ([`docs/specifications/agent-loop/error-recovery.md#tool-provider-plugin-crashes`](../../docs/specifications/agent-loop/error-recovery.md#tool-provider-plugin-crashes)).

Both spec sections describe the same underlying pattern — N consecutive bad
events, or M bad events within a sliding window, trips a "stop trying"
signal — so this is one package, not duplicated logic per call site.

## What this package does

- `circuitbreaker.go` — `Config` (the two independent thresholds),
  `Breaker` (per-provider consecutive-count + sliding-window tracking,
  mutex-guarded), and `New`/`RecordDenial`/`RecordCrash`/`RecordSuccess`/`Reset`.

`Breaker` tracks state per provider name in a map. Each provider gets its
own consecutive-event counter and its own fixed-size ring buffer of the
last `WindowSize` events, with a running bad-event count maintained
incrementally so a trip check never rescans the window.

## What this package does NOT do

- It does not decide what "tripped" means to a caller — routing a tripped
  provider through the limit-reached graceful-degradation path
  ([`turn-algorithm.md#limit-reached-behavior`](../../docs/specifications/agent-loop/turn-algorithm.md#limit-reached-behavior))
  is the caller's job.
- It does not log. No `log/slog`, no `internal/telemetry` import — pure
  domain logic, matching the rest of this repo's domain packages
  (`internal/policy`, `internal/statebackend`).
- It does not distinguish denials from crashes in its counters — see
  `CLAUDE.md` for the shared-signal design decision and reasoning.

## How it fits in

Neither call site is built yet. When they are: the plan/apply gate
constructs one `Breaker` per session (`New(cfg)`, `cfg` sourced from
`agent.hcl`'s `settings{}` block or an equivalent), calls `RecordDenial` on
every `deny` decision and `RecordSuccess` on every `allow`ed call that
completes without denial, and checks the returned `tripped` bool to decide
whether to route the session into the limit-reached path. The tool
scheduler shares the SAME `Breaker` instance for that session, calling
`RecordCrash` on a `process_crashed` error and `RecordSuccess` on a normal
`Invoke` return, so a provider that is both denied and crash-prone trips
one shared counter rather than two independent ones.
