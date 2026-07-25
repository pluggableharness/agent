# internal/sessionscope

A refcounted grant registry answering one question: "is this plugin
currently authorized to make a session-scoped callback naming this
session id?"

## What it is

`docs/specifications/kernel-callbacks.md#the-callback-channel` requires
the kernel to reject any session-scoped callback RPC (`Emit`,
`ReadEvents`, `GetSession`, and the optional-`session_id` RPCs like
`Log`) naming a session other than the one the calling plugin was
actually invoked for. `Registry` is the primitive that answers that
check. It owns no gRPC handling, no logging, and no telemetry — it is
pure in-memory bookkeeping that `internal/kernelcallback`'s future RPC
handlers will call into.

## Why refcounted, not boolean

A plugin subprocess is long-lived and can be invoked by more than one
session at once (parallel `data_source` calls today, nested sub-agent
sessions in a future phase), and the same plugin can be invoked twice
concurrently for the *same* session (two parallel tool calls in one
turn). A single "current session" slot breaks under the first case; a
boolean authorized flag per `(key, session)` breaks under the second,
since whichever of the two concurrent calls finishes first would
incorrectly revoke the other's still-in-flight authorization.

`Registry.Grant` returns a `release` closure bound to the one grant it
took. `Authorized` is true whenever a `(key, sessionID)` pair's
outstanding grant count is above zero. See `doc.go`'s "Design decisions"
for the full three-constraint derivation.

## Shape

- `Key` (`sessionscope.go`) — `{Category, Name}`, mirroring the producer
  identity a kernel-side callback connection is bound to at handshake
  (`kernel-callbacks.md#the-callback-channel`), not the plugin's
  `agent.hcl` local name.
- `KeyFor(*commonv1.ProducerRef) Key` — derives a `Key` from the wire
  producer identity, dropping `version` (a running process's version is
  fixed, and `configuration/blocks-reference.md#required_providers`
  already rules out two concurrently-loaded builds of one
  category+name).
- `Registry` — `NewRegistry()` constructs it; `Grant`, `Authorized`, and
  `Sessions` are its whole public surface.

## Using it

```go
reg := sessionscope.NewRegistry()

key := sessionscope.KeyFor(producerRef) // from the callback connection's handshake
release := reg.Grant(key, sessionID)    // called once per session-scoped invocation
defer release()

// ... inside a session-scoped RPC handler (Emit, ReadEvents, GetSession, ...):
if !reg.Authorized(key, req.GetSessionId()) {
    return nil, fmt.Errorf("sessionscope: session %q not authorized for this plugin", req.GetSessionId())
}
```

`Grant` should be taken for the duration of the one invocation that
established the plugin's participation in `sessionID` (see `CLAUDE.md`
for the exact contract), and released when that invocation ends —
never held open-endedly "just in case" a later call needs it.
