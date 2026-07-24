# Event bus

This is the third kernel-owned (non-plugin) spec, alongside [`kernel-callbacks.md`](kernel-callbacks.md) and [`state-backend.md`](state-backend.md). It defines the ephemeral, best-effort, cross-plugin publish/subscribe primitive exposed by `KernelCallbackService.Publish`/`.Subscribe` ([`kernel-callbacks.md#publish`](kernel-callbacks.md#publish), [`kernel-callbacks.md#subscribe`](kernel-callbacks.md#subscribe)) — the wire shape lives there; this document covers topic grammar, delivery semantics, and the boundary against three other things that already use "event" vocabulary in this project.

## Why a fourth "event" concept, and why it's not one of the other three

`docs/specifications/` already uses "emit," "subscribe," and "broadcast" for three unrelated things, and this bus is deliberately a fourth, distinct from all of them:

| Mechanism | Durability | Ordering | Delivery | Who decides subscription |
|---|---|---|---|---|
| **`Emit`** ([`kernel-callbacks.md#emit`](kernel-callbacks.md#emit)) | Durable — persisted to sqlite, survives a restart | `sequence`, ordering-authoritative | Write-only from the plugin's side; read back via `ReadEvents` (pull) | N/A — there is no subscription, only a per-session log |
| **Hook dispatch** ([`agent-loop/hook-dispatch.md`](agent-loop/hook-dispatch.md)) | Not persisted as its own record (though `EVENT_KIND_HOOK_ERROR` can result) | Strictly sequential per hook point; can veto and short-circuit | Synchronous, unary `DispatchHook` per subscriber, in declared order | `agent.hcl`'s `hook{}` blocks, resolved at config-load time — static, not runtime |
| **Frontend broadcast** (`frontend/frontend-protocol.md`) | Not persisted (the persisted record is the underlying `Emit`, if any) | Delivery order only, no ordering guarantee across frontends | Push over the `Attach` bidi stream to every attached frontend | Attaching to a session, a control-plane operation |
| **Event bus** (this document) | **Not persisted at all** — vaporizes on process exit or when a subscription closes | Per-subscriber FIFO only; no cross-subscriber or global ordering | Push over `Subscribe`'s server stream, best-effort | Whichever plugin calls `Subscribe`, at runtime, with no `agent.hcl` declaration |

The bus exists for a case none of the other three covers: two plugins that want to react to each other's activity in near-real-time, without either being declared as a hook subscriber in `agent.hcl` (hook dispatch's static, ordered, potentially-vetoing model is the wrong shape for this — a broken or slow bus subscriber MUST NOT be able to block or veto anything), and without needing the durability or `ReadEvents` polling overhead `Emit` provides for state that must survive a restart.

**A hook point firing does not imply a bus event, and a bus event does not imply anything was persisted.** These three mechanisms deliberately don't share a code path, mirroring [`state-backend.md`](state-backend.md)'s own observation that a hook point firing is not the same thing as an event being persisted.

## Implementation note: this bus already exists, in-process

The kernel-internal primitive backing `Publish`/`Subscribe` is `internal/eventbus` — an existing, already-tested, telemetry-aware in-process pub/sub `Bus` (topic string, handler callback, per-subscriber unbounded FIFO queue, never-blocking, never-dropping). Everything in this document about topic grammar, the reserved `kernel.*` namespace, and delivery semantics governs the plugin-facing RPC surface built on top of that primitive — the in-process `Bus` itself is unopinionated about topic naming and unaware that "plugin" or "kernel" mean anything in particular. Where this document's guarantees differ from `internal/eventbus`'s own (see "Backpressure" below), the difference is entirely in the RPC bridge layered on top, not a change to the underlying `Bus`.

## Topic grammar

A topic is a dot-separated string. Two namespaces exist, and a plugin can only ever construct a topic in one of them:

- **`plugin.{category}.{name}.{event_type}`** — every event a plugin publishes lands here. `category` and `name` come from the publishing plugin's own producer identity (`common.v1.Category`'s lowercase text, `state-backend.md`'s own vocabulary, and the plugin's declared name), server-derived from the authenticated callback connection exactly as `Emit`'s and `Log`'s producer attribution is — never a value the plugin supplies. `event_type` is the one segment a `Publish` caller does supply (`kernel-callbacks.md#publish`'s `PublishRequest.event_type`), constrained to a single dot-free, wildcard-free segment. `category` is part of the topic, not just `name`, because two different categories can each ship a plugin with the same declared name (a `github` tool provider and a `github` context provider are different producers and MUST land on different topics).
- **`kernel.*`** — reserved for the kernel itself. A plugin MAY subscribe to any `kernel.*` topic but MUST NOT be able to publish onto one — enforced by construction, not by a runtime check: `Publish` only ever builds a `plugin.*` topic (`kernel-callbacks.md#publish`), so there is no code path through which a plugin-originated `Publish` call could produce a `kernel.*` topic. The only kernel-side publisher defined so far is `Emit`'s post-write republish onto `kernel.event.{kind}` (`kernel-callbacks.md#emit`); the namespace is reserved now so that a future kernel-originated topic (a turn-lifecycle notification, for instance) has a home without a naming collision, even though no second publisher exists yet.

A topic segment is `[a-z0-9_]+`; the dot is the only separator, and `*` is never a literal character within a segment — it exists only as the special last-position wildcard described below.

## Filter grammar

`SubscribeRequest.topic_filters` (`kernel-callbacks.md#subscribe`) is a non-empty list of filters. Each filter is one of:

- **An exact topic** — matches only that literal string.
- **A trailing-wildcard prefix** — a filter ending in `.*` matches any topic sharing every segment before the `*`. `plugin.tool.github.*` matches `plugin.tool.github.file_changed` and `plugin.tool.github.pr_opened`, but not `plugin.tool.gitlab.file_changed` (different segment) and not `plugin.tool.github` itself (the wildcard requires at least one further segment to match against).

No other wildcard form exists in v1 — no mid-string wildcard, no multi-segment wildcard (no MQTT-style `#`), no negation. A subscriber wanting "every event from this one plugin" uses `plugin.{category}.{name}.*`; a subscriber wanting "every kernel event" uses `kernel.*`. This is a deliberately small grammar: the two real use cases identified so far (all events from one plugin, all events in one kernel-reserved family) are both trailing-prefix matches, and a richer grammar is added only once a use case that needs one actually surfaces.

## Delivery semantics

- **Best-effort, not guaranteed.** A `Publish` call returns as soon as the kernel has fanned the event out to every currently-subscribed stream's queue; it does not wait for any subscriber to actually receive or process the event, and a subscriber that connects after a `Publish` call already returned never sees that event. There is no backlog and no replay — this is `internal/eventbus`'s own ephemeral contract, inherited unchanged.
- **Per-subscriber ordering only.** A single `Subscribe` stream sees events matching its filters in the order they were published; there is no ordering guarantee across two different `Subscribe` streams, and no ordering guarantee relative to `ReadEvents` or anything hook-dispatch related. Nothing here carries a `sequence` number — [`determinism.md`](../.claude/rules/determinism.md)'s ordering-authority rule governs persisted, replay-critical ordering, and this bus persists nothing and participates in no replay, so it is deliberately outside that rule's scope, exactly as `internal/eventbus`'s own design notes already state.
- **Observe-only, never a veto.** Unlike a hook subscriber in `veto` mode, a bus subscriber cannot block, modify, or reject the event it received — the bus has no response channel for that at all. A slow or broken subscriber can only ever affect itself (see "Backpressure" below), never the publisher or any other subscriber.

## Backpressure

`internal/eventbus`'s own contract is unbounded, never-blocking, never-dropping: a slow in-process subscriber's queue simply grows, because the kernel controls that subscriber's goroutine and its memory is the kernel's own to spend. That guarantee does not survive crossing a subprocess boundary unchanged: a `Subscribe` stream is driven by a remote plugin process the kernel does not control, and an unbounded queue behind a hung or dead plugin's stream would be unbounded kernel-side memory growth driven by a party outside the kernel's control — a materially different risk than a slow in-process handler.

The RPC bridge therefore imposes a **per-stream bound**: once a `Subscribe` stream's undelivered-event queue exceeds that bound, the kernel terminates the stream with `codes.ResourceExhausted` rather than continuing to grow it, and increments a dedicated metric so a slow-consumer disconnect is observable rather than silent. `internal/eventbus`'s own never-drop guarantee is unchanged by this — the bound lives entirely in the bridge layered on top, and an in-process `Subscribe` (the bridge's own `Handler`) is still never blocked or dropped from the `Bus`'s point of view; only the plugin-facing stream can be closed. A plugin that needs to survive a bounded disconnect resubscribes; it has already lost nothing durable, because nothing on this bus was ever durable in the first place.

The per-stream bound is an `event_bus{}` config value (`configuration/blocks-reference.md`), not a wire field — a subscriber cannot request a larger bound for itself.

## The kernel namespace

`kernel.*` is reserved, subscribable, and never publishable by a plugin (see "Topic grammar" above). Its topic set grows as kernel-side publishers are added; the only one defined at this revision:

- **`kernel.event.{kind}`** — republished by `Emit` immediately after a successful persisted write, where `{kind}` is the lowercase text form of the persisted `EventKind` (`state-backend.md#the-kind-enum`'s own vocabulary — e.g. `kernel.event.tool_call`, `kernel.event.message`). This lets a plugin observe the durable event stream live without polling `ReadEvents`, while the durability guarantee is untouched: the sqlite row is committed before the republish happens, so a subscriber that never connects, or that disconnects mid-stream, loses nothing durable — it can always fall back to `ReadEvents` for anything it missed.

A future kernel-originated topic (a turn-lifecycle notification, a plan-ready signal mirrored onto the bus for observability) has a home in this namespace without a naming collision with any plugin's own topics, since no plugin can ever construct a `kernel.*` topic.

## Open questions

- **Authorization.** Any plugin can currently `Subscribe` to `plugin.*` (or `plugin.{category}.{name}.*` for a specific other plugin) and observe every other plugin's published events — there is no `agent.hcl`-declared allowlist gating who may subscribe to whom, unlike hook dispatch's static, declared subscriber list. This revision ships that gap open deliberately rather than half-building an authorization model: the kernel logs every `Subscribe` call's resolved filters at `INFO`, so an operator can audit who is listening, but nothing prevents it. A per-plugin subscribe allowlist in `agent.hcl` is the obvious future control, tracked here as a named extension rather than built now.
- **Whether `kernel.*` should eventually carry more than event republishing** — a turn-lifecycle or plan-ready signal mirrored onto the bus purely for observability tooling, distinct from the hook points of the same names that already fire for dispatch purposes. No second publisher exists yet; this section is a placeholder for when one is proposed, not a commitment that one will be.
