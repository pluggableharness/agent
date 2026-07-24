// Package eventbus implements a deliberately ephemeral, in-process
// publish/subscribe fan-out: a Bus lets any number of components exchange
// Events by topic within one kernel process, and holds nothing beyond
// that process's lifetime. There is no persistence, no backlog, and no
// replay — closing the process (or the Bus itself) discards everything;
// a fresh Bus starts empty and must be re-filled by its publishers.
//
// This is kernel-internal plumbing, not a plugin-protocol category: it
// sits below the wire protocol entirely, and nothing in
// docs/specifications/ describes it (confirmed absent during design —
// the existing "emit"/"subscribe"/"broadcast" vocabulary there names
// three unrelated things: state-backend Emit, the synchronous ordered
// hook-dispatch chain, and frontend ServerEvent broadcast; none of them
// is a pub/sub bus, and none exists anywhere in internal/ or pkg/ prior
// to this package). This package deliberately does not integrate with
// any of them — no agent-loop wiring, no plugin RPC, no docs/specifications/
// entry. When a later change feeds plugin RPCs from Bus events, that
// integration is a separate, spec-first effort.
//
// Bus (eventbus.go) holds a topic -> subscriber registry guarded by a
// mutex; Publish (eventbus.go) fans an Event out to every current
// subscriber of its Topic and returns immediately — it never blocks and
// never drops. Subscribe (eventbus.go) returns a Subscription
// (subscription.go), each with its own unbounded per-subscriber queue
// (queue.go) drained by a dedicated delivery goroutine that invokes the
// registered Handler out-of-band from any publisher.
//
// # Design decisions
//
// The user who requested this package settled the following choices up
// front, before implementation:
//
//   - Handler-callback subscription API (Subscribe(ctx, topic, handler)),
//     not a channel the subscriber reads itself — simpler to wire to an
//     RPC call later, at the cost of the Bus owning the delivery
//     goroutine.
//   - Unbounded, never-blocking, never-dropping delivery: Publish always
//     returns immediately, and a slow or stuck Handler only grows its
//     own subscription's queue — it cannot stall Publish, another
//     subscriber, or the Bus itself. Because unbounded memory growth is
//     the accepted cost of this choice, WithQueueWarnThreshold gives an
//     operator visibility (a throttled WARN log) without changing the
//     never-drop guarantee.
//   - Event.Payload is `any`, routed by a caller-chosen string Topic, so
//     one Bus instance carries heterogeneous event kinds. The Bus does
//     not defensive-copy: the same Payload value/reference reaches every
//     subscriber, so publishers MUST NOT mutate a Payload after
//     Publish returns and subscribers MUST treat it as read-only (see
//     Event's doc comment).
//
// Beyond those three, this package makes the following implementation
// choices:
//
//   - Ordering: a single subscription sees its topic's events in publish
//     order (per-subscriber FIFO), but the Bus makes no cross-subscriber
//     or global ordering promise, and assigns no sequence number.
//     .claude/rules/determinism.md's sequence-is-the-only-ordering-
//     authority rule governs persisted, replay-critical event ordering
//     (internal/statebackend); this package persists nothing and
//     participates in no replay, so it is deliberately outside that
//     contract. Do not read Event ordering across subscriptions as
//     meaningful, and do not add a sequence field here to make it look
//     replay-adjacent — it explicitly is not.
//   - Lifecycle: Subscription.Close cancels that subscription's own
//     context (derived from the ctx passed to Subscribe) and waits for
//     its delivery goroutine to exit before returning, so Close never
//     leaks a goroutine — the same "signal, then wait" shape as
//     internal/pluginruntime's closeWithKill (shutdown.go), though built
//     on context.CancelFunc's own documented idempotency rather than a
//     bespoke done channel plus sync.Once: a context.CancelFunc is safe
//     to call more than once, and a closed channel is safe to receive
//     from more than once, so Close is idempotent for free without an
//     extra guard. Bus.Close cancels and waits for every open
//     subscription the same way, and is itself guarded by a sync.Once
//     since it also mutates the shared subscriber registry. Undelivered,
//     still-queued events are discarded on either kind of Close — the
//     vaporize contract applies to in-flight events too, not just to the
//     Bus's own memory.
//   - Bus.closed is a sync/atomic.Bool, checked at the top of Publish and
//     Subscribe, so a call made after Close returns ErrClosed immediately
//     rather than doing any work first — the same fast-reject shape
//     internal/statebackend.Session uses for its own closed flag.
//   - queue (queue.go) reclaims its backing array once the drained prefix
//     dominates it, rather than only ever slicing forward — an unbounded
//     FIFO that always grows and never reclaims capacity would leak
//     memory even after its items are consumed, which would defeat the
//     "vaporizes once delivered" half of this package's contract just as
//     surely as never dropping does.
package eventbus
