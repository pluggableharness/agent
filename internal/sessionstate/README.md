# internal/sessionstate

The kernel's live-session table and the sole-writer object for one
session's persisted event log
([`docs/specifications/state-backend.md#ordering--concurrency`](../../docs/specifications/state-backend.md#ordering--concurrency)).

## What this package does

- `sessionstate.go` — `Live`: wraps exactly one already-created/opened
  `*statebackend.Session` plus that session's in-memory budget tracker
  (`*bounds.Tracker`,
  [`state-backend.md#live-vs-post-hoc-tree-walking`](../../docs/specifications/state-backend.md#live-vs-post-hoc-tree-walking)
  — budget state is live and never persisted). `NewLive` constructs one;
  `Budget` exposes the tracker for a caller to check/debit directly;
  `Close` closes the underlying session.
- `emit.go` — `Emit`/`EmitMessage`/`EmitPlan`: the write-then-republish
  mechanics. Each persists an event via the matching `statebackend.Session`
  append method, then republishes it onto the event bus's reserved
  `kernel.event.{kind}` topic
  ([`kernel-callbacks.md#emit`](../../docs/specifications/kernel-callbacks.md#emit),
  [`event-bus.md#the-kernel-namespace`](../../docs/specifications/event-bus.md#the-kernel-namespace))
  — only after the sqlite commit succeeds, and never failing the call if
  the republish itself fails.
- `table.go` — `Table`: the process-wide registry of currently-live
  sessions, keyed by session id.

## What this package is not

This is the primitive a later phase's `internal/kernelcallback` `Emit`/
`ReadEvents`/`GetSession` implementation sits on top of — it is not itself
an RPC handler, and it does no session-scope authorization
(`internal/sessionscope`'s job, invoked by that future RPC handler before
ever reaching this package). See `CLAUDE.md` for why `EmitMessage`/
`EmitPlan` are kernel-internal, never reachable from a plugin-facing
`Emit` RPC.

## Using it

```go
live := sessionstate.NewLive(session, bus, limits, nil, time.Now, telemetryProvider, logger)
defer live.Close()

outcome, err := live.Emit(ctx, sessionstate.EmitRecord{
    Producer:      producerRef,
    Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
    SchemaVersion: "1",
    Payload:       payloadBytes,
})

table := sessionstate.NewTable()
table.Put(session.ID(), live)
live, ok := table.Get(session.ID())
```

`EmitMessage`/`EmitPlan` are called by kernel-internal code only (the
model-call path and the plan-build step, respectively) — never in response
to a plugin's own `Emit` RPC.
