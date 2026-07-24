# internal/eventbus

An ephemeral, in-process publish/subscribe event bus for the kernel.

## What it is

`Bus` lets any number of in-process components exchange `Event`s by topic, for the lifetime of one kernel process. It is deliberately not persistent: there is no history, no backlog, and no replay. A published `Event` is fanned out to every subscription currently registered on its `Topic`, and once that fan-out step returns, the `Bus` retains no reference to it — closing the process (or the `Bus` itself) discards everything, and a fresh `Bus` starts empty.

This lets plugins be fed data pushed to them out-of-band from the kernel's own request/response RPC calls, rather than only ever being called synchronously. **The plugin-facing wiring now exists**: `internal/kernelcallback`'s `Publish`/`Subscribe` RPC handlers (`eventbus.go` there) bridge `KernelCallbackService.Publish`/`.Subscribe` to this package's `Bus.Publish`/`Bus.SubscribeFilters` — see `docs/specifications/event-bus.md` for the wire-facing topic grammar, filter grammar, and delivery semantics that bridge implements, and `doc.go`'s "Design decisions" section for this package's own, lower-level design record.

## Where it sits relative to the wire protocol

`docs/specifications/event-bus.md` is this package's wire-facing counterpart, distinguishing it from three other "event"-shaped mechanisms already in this project: state-backend `Emit` (durable, sequenced), the synchronous ordered hook-dispatch chain (`agent-loop/hook-dispatch.md`, static, `agent.hcl`-declared, can veto), and frontend `ServerEvent` broadcast (`frontend/frontend-protocol.md`, connection-scoped). None of those three is a pub/sub bus. `internal/eventbus` itself still never crosses the `hashicorp/go-plugin` boundary directly and has no proto of its own — `internal/kernelcallback` is the seam that does that translation, using `kernel.v1`'s `PublishRequest`/`BusEvent` messages.

## Shape

- `Bus` (`eventbus.go`) — the registry (an exact-topic map plus a linearly-scanned trailing-wildcard list, `filter.go`) plus `Publish`/`Subscribe`/`SubscribeFilters`/`Close`.
- `Event` (`event.go`) — `{ Topic string; Payload any }`. See its doc comment for the read-only contract on `Payload`.
- `Subscription` (`subscription.go`) — one open registration under one or more filters: a `Handler` invoked on its own dedicated delivery goroutine, fed by an unbounded per-subscriber `queue` (`queue.go`).
- `filter.go` — `isWildcardFilter`/`wildcardPrefix`/`matchesFilter`, the exact-or-trailing-`*` matching `SubscribeFilters` and `Publish` share. Deliberately permissive (any trailing `*` counts) — the stricter wire-level grammar (`event-bus.md`'s "ending in `.*`," whole segments only) is validated by `internal/kernelcallback`'s RPC boundary, not here.

Confirmed design choices (settled with the requester before implementation, recorded in full in `doc.go`):

1. **Handler-callback subscription API** — `Subscribe(ctx, topic, handler)`, not a channel the caller reads itself. `SubscribeFilters(ctx, filters, handler)` extends this to multiple exact-or-wildcard filters per subscription; `Subscribe` is sugar over it for the single-exact-topic case.
2. **Unbounded, never-blocking, never-dropping delivery** — `Publish` always returns immediately; a slow subscriber only grows its own queue, never anyone else's, and never stalls a publisher. `internal/kernelcallback`'s `Subscribe` RPC layers its own separate, bounded buffer on top of a `Subscription` for the plugin-facing gRPC stream (`event-bus.md#backpressure`) — that bound lives entirely in the bridge; this guarantee is unchanged here.
3. **`any` payload routed by a string `Topic`** — one `Bus` carries heterogeneous event kinds. `internal/kernelcallback`'s bridge always uses `*kernelv1.BusEvent` as the payload shape for anything reaching a plugin.

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
