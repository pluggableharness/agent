# internal/eventbus

An ephemeral, in-process publish/subscribe event bus for the kernel.

## What it is

`Bus` lets any number of in-process components exchange `Event`s by topic, for the lifetime of one kernel process. It is deliberately not persistent: there is no history, no backlog, and no replay. A published `Event` is fanned out to every subscription currently registered on its `Topic`, and once that fan-out step returns, the `Bus` retains no reference to it — closing the process (or the `Bus` itself) discards everything, and a fresh `Bus` starts empty.

This exists so plugins can eventually be fed data pushed to them out-of-band from the kernel's own request/response RPC calls, rather than only ever being called synchronously. **This package does not wire that up.** It is a standalone, self-contained primitive: no agent-loop integration, no plugin RPC, no `docs/specifications/` entry. See `doc.go`'s "Design decisions" section for why, and what a future integration would need to add.

## Why it isn't a wire protocol

`docs/specifications/` already uses "emit," "subscribe," and "broadcast" for three unrelated things — state-backend `Emit` (persist an opaque event), the synchronous ordered hook-dispatch chain (`agent-loop/hook-dispatch.md`), and frontend `ServerEvent` broadcast (`frontend/frontend-protocol.md`). None of them is a pub/sub bus, and confirming that gap was part of this package's own design process. `internal/eventbus` sits below all of that — it never crosses the `hashicorp/go-plugin` boundary, so it has no proto, no category, and no spec document of its own; it's kernel-internal plumbing in the same sense as `internal/telemetry` or `internal/producer`.

## Shape

- `Bus` (`eventbus.go`) — the registry (`topic -> subscriptions`) plus `Publish`/`Subscribe`/`Close`.
- `Event` (`event.go`) — `{ Topic string; Payload any }`. See its doc comment for the read-only contract on `Payload`.
- `Subscription` (`subscription.go`) — one open registration: a `Handler` invoked on its own dedicated delivery goroutine, fed by an unbounded per-subscriber `queue` (`queue.go`).

Confirmed design choices (settled with the requester before implementation, recorded in full in `doc.go`):

1. **Handler-callback subscription API** — `Subscribe(ctx, topic, handler)`, not a channel the caller reads itself.
2. **Unbounded, never-blocking, never-dropping delivery** — `Publish` always returns immediately; a slow subscriber only grows its own queue, never anyone else's, and never stalls a publisher.
3. **`any` payload routed by a string `Topic`** — one `Bus` carries heterogeneous event kinds.

## Using it

```go
bus := eventbus.New() // logs via slog.Default(), telemetry disabled by default
defer bus.Close()

sub, err := bus.Subscribe(ctx, "tool.result", func(ctx context.Context, ev eventbus.Event) {
    result := ev.Payload.(myResultType) // caller-defined convention per topic
    // handle it — this runs on a dedicated goroutine, out-of-band from Publish
})
defer sub.Close()

err = bus.Publish(ctx, eventbus.Event{Topic: "tool.result", Payload: myResultType{...}})
```

`New` accepts functional options: `WithLogger`, `WithTelemetry`, `WithQueueWarnThreshold` (see `eventbus.go`'s doc comments).
