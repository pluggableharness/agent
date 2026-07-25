# internal/sessionstate — agent notes

- **`EmitMessage` and `EmitPlan` are kernel-internal paths, never reachable
  from a plugin-facing `Emit` RPC — and this is a correctness requirement,
  not a style preference.** [`state-backend.md`](../../docs/specifications/state-backend.md)'s
  conformance table requires `cost_ledger` populated "at the same time as
  the message event that produced it," and `plan_items` populated
  alongside its plan event, both in the same transaction
  (`statebackend.Session.AppendMessage`/`AppendPlan` already enforce this
  at the sqlite level). A generic plugin-facing `Emit(EventKind, payload)`
  call has no way to also supply a `CostEntry` or `[]PlanItem` — those
  shapes don't exist on the wire `EmitRequest`
  ([`kernel-callbacks.md#emit`](../../docs/specifications/kernel-callbacks.md#emit)).
  The future `internal/kernelcallback` `Emit` RPC handler MUST reject
  `EVENT_KIND_MESSAGE`/`EVENT_KIND_PLAN` from a plugin's own `Emit` call
  and route the kernel's own model-call/plan-build code to `EmitMessage`/
  `EmitPlan` directly instead — don't "simplify" by routing everything
  through the plain `Emit` and bolting the cost/plan-item write on
  separately; that reopens the exact race the same-transaction requirement
  exists to close.

- **Validation is the caller's job, not this package's.** `EmitRecord`'s
  own doc comment lists what a future `kernelcallback.Emit` handler is
  expected to have already checked (session_id authorized via
  `internal/sessionscope`, `kind != EVENT_KIND_UNSPECIFIED`,
  `schema_version` non-empty, payload non-nil, the kernel-owned-kind
  rejection above) before ever calling into `Live.Emit`/`EmitMessage`/
  `EmitPlan`. This package still gets `ErrInvalidKind`/`ErrInvalidProducer`
  for free from `statebackend.Session`'s own append validation (it never
  duplicates that logic), but it does not itself implement the
  session-scope authorization check or the plugin-vs-kernel kind
  partitioning — those live one layer up, deliberately, per this package's
  own `doc.go`.

- **`Live.mu` is held for the full duration of every `Emit*` call —
  append, budget debit, and republish, in that order — never just the
  append.** This is what makes "one writer at a time per session" true for
  the whole write-then-republish sequence, not just the sqlite half of it.
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
  never cause `Emit`/`EmitMessage`/`EmitPlan` to return an error, since the
  durable write already committed (`kernel-callbacks.md#emit`'s own
  documented rationale: "a subscriber that never connects... loses
  nothing durable").

- **This package MUST NOT import `internal/kernelcallback`.** It is the
  primitive a later phase's `kernelcallback` `Emit`/`ReadEvents`/
  `GetSession` implementation is built on top of, not a peer or a
  consumer of it — importing it here would be backwards and likely
  cyclic once that phase lands.

- **`republish`'s `EventKindText`/`EventPayloadType` error branches are
  unreachable in practice, not dead code to delete.** `rec.Kind` already
  passed the identical `encodeEventKind` validation inside the
  `AppendEvent`/`AppendMessage`/`AppendPlan` call that produced the
  `id`/`seq` `republish` is given — by the time `republish` runs, the kind
  is known-valid. The branches stay as a defensive, logged failure path
  rather than a `panic` or an ignored error, consistent with this
  package's "never let a bus-side problem take down a durable write" rule
  above.

- **Budget rollup is `bounds.Tracker.Debit`'s job, not this package's.**
  `EmitMessage` calls `l.budget.Debit(cost.CostUSD)` exactly once and
  trusts `Debit`'s own parent-chain walk
  ([`internal/bounds`](../../internal/bounds)) to roll the same amount up
  through every ancestor. Don't add a second rollup loop here — `bounds`
  already owns that lock-ordering-sensitive logic, and duplicating it
  would risk diverging from `bounds_test.go`'s own coverage of the
  ancestor-walk invariants.

- **Tests use a real `*statebackend.Store`/`*statebackend.Session` over
  `t.TempDir()` and a real `*eventbus.Bus`, and are still unit tier** — see
  `go-testing.md`'s reasoning already applied identically in
  `internal/statebackend`'s own tests: local sqlite with no subprocess
  stays inside the unit tier's "fakes, `t.TempDir()`, no external network"
  bound. Don't reach for an `integration`-tagged file just because a real
  file and a real bus are involved.
