# internal/sessionstate — agent notes

- **Two write paths, and the split is by *who is writing*, not by event
  kind.** `Emit` is the plugin-facing path: it takes an `EmitRecord` and
  mints the event's id and timestamp itself, because a plugin has no
  business assigning either. `AppendEvent`/`AppendMessage`/`AppendPlan` are
  the kernel-internal path: they take an already-built
  `statebackend.Event`, because the kernel-side collaborator *does* own
  that identity — `internal/modelcall` deliberately reuses the
  kernel-assigned message id as the event id, and a method that minted a
  fresh one would silently overwrite that decision. Their signatures are
  exactly `*statebackend.Session`'s own `Append*` signatures, which is what
  lets a `*Live` satisfy the sink interface all five kernel collaborators
  (`contextassembly`, `modelcall`, `tooldispatch`, `hookdispatch`,
  `plangate`) already declare, with no adapter.

- **Nothing may hand out the wrapped `*statebackend.Session`.** A
  `Session()` accessor existed so the composition root could give those
  five collaborators the same open handle. It was removed because it
  defeated both properties this type exists to provide: writes through the
  raw handle skipped `mu` (so a session no longer had one writer at a time)
  **and** skipped the `kernel.event.{kind}` republish — so no
  kernel-originated event ever reached the bus, and a plugin subscribed to
  `kernel.event.*` saw other plugins' `Emit` calls and never a `message`,
  `tool_call`, `tool_result`, `plan`, or `apply`.
  `TestLive_AppendEvent_republishesEveryKernelKind` is the regression test.
  Don't reintroduce the accessor.

- **The `Append*` methods deliberately do NOT debit the budget tracker.**
  `internal/session`'s `absorb` debits `turn.Result.CostUSD` exactly once
  per turn, and it is the only thing on the path that can — so debiting
  here as well would count every completion twice, compounding silently
  rather than failing. `TestLive_AppendMessage_doesNotDebitBudget` asserts
  both the session's own tracker and its parent stay at zero (a stray debit
  would corrupt every ancestor, since `bounds.Tracker.Debit` walks the
  chain). Budget ownership lives in `internal/session`; see its `CLAUDE.md`.

- **`EVENT_KIND_MESSAGE`/`EVENT_KIND_PLAN` are still rejected on the
  plugin-facing `Emit`, and that is a correctness requirement rather than
  a style preference.** [`state-backend.md`](../../docs/specifications/state-backend.md)'s
  conformance table requires `cost_ledger` populated "at the same time as
  the message event that produced it," and `plan_items` populated
  alongside its plan event, both in the same transaction
  (`statebackend.Session.AppendMessage`/`AppendPlan` already enforce this
  at the sqlite level). A generic plugin-facing `Emit(EventKind, payload)`
  call has no way to also supply a `CostEntry` or `[]PlanItem` — those
  shapes don't exist on the wire `EmitRequest`
  ([`kernel-callbacks.md#emit`](../../docs/specifications/kernel-callbacks.md#emit)).
  `internal/kernelcallback`'s `Emit` handler rejects both kinds and the
  kernel's own model-call/plan-build code calls `AppendMessage`/
  `AppendPlan` instead — don't "simplify" by routing everything through
  the plain `Emit` and bolting the cost/plan-item write on separately;
  that reopens the exact race the same-transaction requirement exists to
  close.

- **Validation is the caller's job, not this package's.** `EmitRecord`'s
  own doc comment lists what a future `kernelcallback.Emit` handler is
  expected to have already checked (session_id authorized via
  `internal/sessionscope`, `kind != EVENT_KIND_UNSPECIFIED`,
  `schema_version` non-empty, payload non-nil, the kernel-owned-kind
  rejection above) before ever calling into `Live.Emit`. This package
  still gets `ErrInvalidKind`/`ErrInvalidProducer`
  for free from `statebackend.Session`'s own append validation (it never
  duplicates that logic), but it does not itself implement the
  session-scope authorization check or the plugin-vs-kernel kind
  partitioning — those live one layer up, deliberately, per this package's
  own `doc.go`.

- **`Live.mu` is held for the full duration of every write call — append
  then republish — never just the append.** This is what makes "one writer
  at a time per session" true for the whole write-then-republish sequence,
  not just the sqlite half of it. It holds only because every writer goes
  through this type: the moment something writes to the wrapped
  `*statebackend.Session` directly, the property is gone, which is why no
  accessor for that handle exists.
  Don't narrow the critical section to just the `AppendEvent`/
  `AppendMessage`/`AppendPlan` call on the theory that the republish
  doesn't need serializing — a narrower lock would let two concurrent
  `Emit` calls' republishes interleave in a different order than their
  commits, which is harmless for correctness here (the bus makes no
  cross-subscriber ordering guarantee, per `event-bus.md#delivery-semantics`)
  but is still a needless, hard-to-reason-about deviation from "one write
  at a time" — keep the whole method under one lock.

- **Republish ordering is load-bearing: append first, republish only on
  success.** `republish` is called only after the `AppendEvent`/
  `AppendMessage`/`AppendPlan` call already returned successfully — never
  reordered, and never called speculatively before the append to "save a
  branch." A republish failure is logged at `WARN` and swallowed; it must
  never cause `Emit`/`AppendEvent`/`AppendMessage`/`AppendPlan` to return an error, since the
  durable write already committed (`kernel-callbacks.md#emit`'s own
  documented rationale: "a subscriber that never connects... loses
  nothing durable").

- **This package MUST NOT import `internal/kernelcallback`.** It is the
  primitive `internal/kernelcallback`'s `Emit`/`ReadEvents`/`GetSession`
  implementation is built on top of, not a peer or a consumer of it —
  importing it here would be backwards and cyclic.

- **`query.go`'s `Meta`/`TotalCostUSD`/`Events` are the additive read
  pass-throughs `internal/kernelcallback`'s `GetSession`/`ReadEvents`
  needed, added deliberately narrow.** Each is a one-line delegation to
  the wrapped `*statebackend.Session` and, unlike every `Emit*` method in
  `emit.go`, takes no lock: they're read-only, and sqlite's own WAL-mode
  readers already see either the state before or after a concurrent
  write's commit, never a torn one, so serializing a read against `Live.mu`
  would only add unneeded contention with an in-flight `Emit*` call. If a
  future caller needs a read that isn't a direct pass-through to an
  existing `*statebackend.Session` method, add another narrow method here
  rather than exposing the `session` field itself.

- **`republish`'s `EventKindText`/`EventPayloadType` error branches are
  unreachable in practice, not dead code to delete.** `rec.Kind` already
  passed the identical `encodeEventKind` validation inside the
  `AppendEvent`/`AppendMessage`/`AppendPlan` call that produced the
  `id`/`seq` `republish` is given — by the time `republish` runs, the kind
  is known-valid. The branches stay as a defensive, logged failure path
  rather than a `panic` or an ignored error, consistent with this
  package's "never let a bus-side problem take down a durable write" rule
  above.

- **Budget rollup is `bounds.Tracker.Debit`'s job, and the single call site
  is `internal/session`'s `absorb` — not this package.** `Live` exposes the
  tracker via `Budget()` and otherwise leaves it alone. If a future path
  ever does need to debit from here, it debits once and trusts `Debit`'s
  own parent-chain walk ([`internal/bounds`](../../internal/bounds)) to
  roll the amount up through every ancestor — never a second rollup loop,
  which would risk diverging from `bounds_test.go`'s coverage of the
  ancestor-walk invariants.

- **Tests use a real `*statebackend.Store`/`*statebackend.Session` over
  `t.TempDir()` and a real `*eventbus.Bus`, and are still unit tier** — see
  `go-testing.md`'s reasoning already applied identically in
  `internal/statebackend`'s own tests: local sqlite with no subprocess
  stays inside the unit tier's "fakes, `t.TempDir()`, no external network"
  bound. Don't reach for an `integration`-tagged file just because a real
  file and a real bus are involved.
