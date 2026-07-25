# internal/hookdispatch

The kernel's ordered, declaration-order hook dispatcher — the implementation of [`docs/specifications/agent-loop/hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md).

Every one of the seven plugin categories can subscribe to hook points through one shared wire surface, `pluggableharness.hook.v1.HookSubscriberService`. This package is the kernel side of that surface: it decides who is in a hook point's chain, in what order, under what deadline, and what happens when one of them fails.

## What lives here

**`Registry`** resolves subscriptions into one ordered chain per hook point. Two kinds of subscription feed it — *implicit* ones a provider's category implies, and *explicit* `hook{}` blocks an operator wrote in `agent.hcl` — and both are ordered by the same authority: textual declaration position ([`configuration/agent-profiles.md#explicit-hook-subscriptions`](../../docs/specifications/configuration/agent-profiles.md#explicit-hook-subscriptions)). An implicit subscription's position is its `provider{}` block's range; an explicit one's is its `hook{}` block's range.

`NewRegistry` is also where the config-load-time rejections live. A subscription naming a point the plugin never advertised in `supported_hook_points`, a `veto`-mode subscription at a point that gates nothing, and a duplicate `(provider, point)` pair are all errors before a session runs its first turn — never a surprise discovered mid-dispatch.

**`Registry.Pin`** registers the kernel-privileged veto — the policy engine, which is [not a plugin category](../../docs/specifications/architecture.md#policy--first-party-not-a-plugin-category) and never goes through `HookSubscriberService`. A pinned veto runs ahead of every plugin subscriber unconditionally. It is declared here as the narrow `KernelVeto` interface so this package never imports `internal/policy`.

**`Dispatcher`** walks one point's chain, exactly per [the spec's pseudocode](../../docs/specifications/agent-loop/hook-dispatch.md#dispatch-order-and-payload-flow). The three modes have deliberately asymmetric failure semantics:

| Mode | On success | On error or timeout |
|---|---|---|
| `observe` | response discarded, chain continues | logged, persisted as `hook_error`, chain continues — "a broken logger MUST NOT be able to break the loop" |
| `transform` | payload merged via `internal/hookpayload`, chain continues with the new payload | chain aborts, `hook_error` persisted, `ErrTransformFailed` returned — never a silent fallback to the pre-transform payload |
| `veto` | `ALLOW` continues; any non-`ALLOW` short-circuits the rest of the chain | fails **closed** to `DENY` |

Fail-closed for `veto` is the safety property this package exists to guarantee: a malfunctioning or slow veto subscriber can only ever make the kernel more conservative, never widen what gets auto-applied.

## What does not live here

Payload shape validation and transform merging belong to [`internal/hookpayload`](../hookpayload/), which is pure domain — it knows which fields each point makes mutable and what response variant each mode requires, and it does no I/O. This package composes it and owns everything hookpayload deliberately is not: ordering, deadlines, gRPC, telemetry, and `hook_error` persistence.

Building the implicit subscription list is also not this package's job. No category-to-hook-point derivation table exists in any spec this package could cite, so `NewRegistry` takes implicit subscriptions as a parameter rather than deriving them from a mapping it would have had to invent.

## Timeouts and cancellation

Each subscriber's deadline is transport-level — a `context.WithTimeout` on the `DispatchHook` call itself, never a field on the request ([`#per-subscriber-timeout`](../../docs/specifications/agent-loop/hook-dispatch.md#per-subscriber-timeout)). It is the `hook{}` block's `timeout_ms` override when one is declared, otherwise `settings.default_hook_timeout_ms`.

A subscriber's *own* deadline firing is a subscriber failure, and at a veto point it fails closed to `DENY`. The *parent* context being canceled — the turn or session being torn down — is not: `Dispatch` abandons the chain and returns that cancellation rather than manufacturing a decision for a turn nobody is waiting on. See this package's `CLAUDE.md` for why that distinction is load-bearing.

## Parallelism

Sequential by default. `Options.ConcurrentObserve` enables the spec's MAY: a maximal run of *consecutive* `observe`-mode subscribers may execute concurrently with each other. It never reorders around a neighboring `transform` or `veto` subscriber, and the resulting `hook_error` events are still persisted in declaration order so replay stays deterministic.
