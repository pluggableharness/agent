# Hook dispatch

[`architecture.md`](../architecture.md#hook-dispatch-semantics) establishes the ordered-chain model (declaration order, three subscriber modes — `observe`/`transform`/`veto`) but leaves mechanics unspecified; this document is that mechanics layer. No surveyed harness exposes a generalized, third-party-pluggable hook-dispatch subsystem in this shape, so the following is this kernel's own design, informed by the closest analogous patterns where one exists.

## Wire contract — `pluggableharness.agent.hook.v1`

`pluggableharness.agent.hook.v1.HookSubscriberService` (`api/pluggableharness/agent/hook/v1/hook.proto`) is the wire surface every hook subscriber implements, regardless of which of the six plugin categories the subscribing plugin otherwise belongs to. It is one shared service, not a per-category RPC: `hashicorp/go-plugin` natively muxes multiple gRPC services over a single subprocess connection, so the kernel dials `HookSubscriberService` on the same connection it already holds to that plugin's category service. A plugin declaring no `hook{}` block in `agent.hcl` simply never has `DispatchHook` called.

`DispatchHook` is unary — one hook-point firing, delivered to one subscriber, is one request/one response. There is no separate scheduling RPC or subscription-registration call; `agent.hcl`'s `hook{}` blocks are the sole source of "which plugins subscribe to which points in which mode," resolved at config-load time, and dispatch order within a point is the declaration order described in [Dispatch order and payload flow](#dispatch-order-and-payload-flow) below.

### Hook points

`hook.v1.HookPoint` enumerates eight of [`architecture.md`](../architecture.md#hook-dispatch-semantics)'s nine named points — every one except `context-assemble`, which stays on `ContextService.Contribute` ([`../context/protocol.md#contribute-the-context-assemble-rpc`](../context/protocol.md#contribute-the-context-assemble-rpc)) rather than riding this surface. `Contribute` already carries the full accumulated `ContextSection` chain as a first-class typed request/response; routing it through the generic `HookPayload` oneof below would just be a second, redundant path to the same effect with weaker typing.

| Hook point | `HookPayload` variant |
|---|---|
| `session-start` | `SessionStartPayload` |
| `pre-model-call` | `PreModelCallPayload` |
| `post-model-response` | `PostModelResponsePayload` |
| `pre-tool-call` | `PreToolCallPayload` |
| `plan-ready` | `PlanReadyPayload` |
| `post-tool-call` | `PostToolCallPayload` |
| `post-apply` | `PostApplyPayload` |
| `session-end` | `SessionEndPayload` |

`HookPayload` is a `oneof`; the set variant *is* the point being dispatched — `DispatchHookRequest` carries no separate `HookPoint` field. `HookPoint` exists on the wire only where there's no oneof to infer a point from: `HookError`, and the future `event.v1.HookErrorEvent` it's embedded in.

### Dispatch modes → response shapes

`DispatchHookRequest.mode` (`hook.v1.HookMode`) tells the subscriber which of `DispatchHookResponse`'s three outcome shapes is expected back:

| Mode | Expected response | Payload semantics |
|---|---|---|
| `HOOK_MODE_OBSERVE` | `ObserveAck` (empty) | Fire-and-forget. The kernel discards any payload the subscriber returns even if one is present — observe mode can never alter the chain. |
| `HOOK_MODE_TRANSFORM` | `TransformResult { payload }` | `payload` MUST be the same `HookPayload` oneof variant as the request. The kernel applies only the fields this point's [mutable-field table](#per-point-transform-mutable-fields) below documents as mutable; every other field is compared against the request and any change is rejected. |
| `HOOK_MODE_VETO` | `VetoResult { decision }` | `decision` MUST be `HOOK_DECISION_ALLOW` or `HOOK_DECISION_DENY`. `HOOK_DECISION_UNSPECIFIED` is an invalid response, not an implicit allow or deny. |

A response whose oneof variant doesn't match `mode` — an `observe` subscriber returning `VetoResult`, a `transform` subscriber returning `ObserveAck`, and so on — is `HOOK_ERROR_CATEGORY_INVALID_RESPONSE`, handled per [Subscriber error handling](#subscriber-error-handling) below.

### Per-point transform-mutable fields

Only `pre-model-call` grants a `transform` subscriber real payload mutation in v1; every other point's `transform` mode is either not meaningfully mutable or not expected to be subscribed in `transform` mode at all (a `transform` subscriber at a non-mutable point MUST return the payload byte-identical to what it received — the kernel rejects any diff as `HOOK_ERROR_CATEGORY_INVALID_RESPONSE`, the [Dispatch modes → response shapes](#dispatch-modes--response-shapes) table's variant-and-field check applies uniformly regardless of which point it's checking).

| `HookPayload` variant | Transform-mutable fields | Immutable fields |
|---|---|---|
| `SessionStartPayload` | none | `session_id`, `profile`, `parent_session_id`, `working_directory` |
| `PreModelCallPayload` | `messages` | `model` — a hook subscriber does not get to silently reroute a turn to a different model than the one the turn algorithm already resolved |
| `PostModelResponsePayload` | none | `message`, `model`, `usage`, `cost_usd` — the completion has already happened; there is nothing left to transform, only to observe |
| `PreToolCallPayload` | none | `call`, `plan_item` — argument mutation is the plan/apply gate's own concern ([`plan-apply-gate.md`](plan-apply-gate.md)), not a general hook capability |
| `PlanReadyPayload` | none | `plan` — a hook subscriber does not rewrite plan items; only the plan/apply gate itself and the `veto`-mode policy decision affect what applies |
| `PostToolCallPayload` | none | `call`, `result`/`error` |
| `PostApplyPayload` | none | `apply` — applying has already happened by the time this fires |
| `SessionEndPayload` | none | `session_id`, `status` — the outcome is already final |

`messages` mutation at `pre-model-call` is the one case where `transform` mode does real work: redaction, injecting an additional instruction, or similar content-level rewriting of what's about to be sent to the model.

### `INVALID_RESPONSE` handling

`HOOK_ERROR_CATEGORY_INVALID_RESPONSE` covers every shape mismatch this document defines: wrong oneof variant for the declared `mode`, a `transform` response whose `HookPayload` variant doesn't match the request's, a `transform` response that mutates a field the [mutable-field table](#per-point-transform-mutable-fields) doesn't list, and `HOOK_DECISION_UNSPECIFIED` on a `veto` response. It is handled exactly like `HOOK_ERROR_CATEGORY_TRANSFORM_FAILED`/`HOOK_ERROR_CATEGORY_VETO_FAILED` per [Subscriber error handling](#subscriber-error-handling) below, mode-appropriately: an invalid `observe` response is logged and dispatch continues (observe errors are never fatal to the chain); an invalid `transform` response aborts the chain and raises `hook_error`; an invalid `veto` response fails closed to `HOOK_DECISION_DENY`.

### Per-subscriber timeout

Per-subscriber timeout is a `ctx` deadline the kernel sets on the `DispatchHook` call itself (`default_hook_timeout_ms`, with a per-subscriber `agent.hcl` override — see [Timeout behavior](#timeout-behavior) below), not a field carried on `DispatchHookRequest`. This matches the "Context and deadlines" convention used identically across every other category's protocol: the deadline is transport-level, not application-level, so a subscriber honoring `ctx` cancellation promptly is what actually bounds the kernel's wall-clock wait.

## Dispatch order and payload flow

For a given hook point, the kernel MUST visit registered subscribers in a single pass, in declaration order (`agent.hcl` order), regardless of subscriber mode. There is no separate scheduling phase per mode — `observe`, `transform`, and `veto` subscribers are interleaved in whatever order they were declared, and each sees the payload as transformed by every subscriber before it in that order:

```go
HookDispatch(point, payload) -> (payload', decision):
  decision := allow   // only meaningful at veto-bearing hook points, e.g. plan-ready
  for subscriber in ordered_subscribers(point):
    outcome := Invoke(subscriber, payload, timeout = subscriber.timeout_ms ?? default_hook_timeout_ms)
    switch subscriber.mode:
      observe:
        // outcome.payload is discarded even if returned; errors/timeouts are logged
        // and do NOT alter payload or abort the chain
      transform:
        if outcome is error or timeout:
          abort chain, surface hook_error to the session
        payload := outcome.payload
      veto:
        if outcome is error or timeout:
          decision := deny            // fail-closed
          break
        if outcome.decision != allow:
          decision := outcome.decision
          break                        // explicit non-allow short-circuits remaining subscribers
  return payload, decision
```

## Subscriber error handling

- An `observe` subscriber MUST NOT be able to alter the payload or abort the chain on error; a raised error is logged (as an event on the state backend, `producer` = the failing subscriber) and dispatch continues. Observe mode exists for audit/logging — a broken logger MUST NOT be able to break the loop.
- A `transform` subscriber error (raised exception, malformed return value) MUST abort the remainder of that hook's chain and MUST surface as a structured `hook_error` event distinct from a tool error, visible to the frontend. The kernel MUST NOT silently fall back to the pre-transform payload and continue as if nothing happened — a `context-assemble` transform failure, for example, means the model is about to see an unintended context state, which is a correctness issue serious enough to warrant surfacing rather than swallowing.
- A `veto` subscriber error is treated identically to an explicit `deny` decision (fail-closed) — see [Timeout behavior](#timeout-behavior).

## Timeout behavior

The kernel MUST enforce a per-subscriber timeout at every hook invocation (`default_hook_timeout_ms`, kernel-configurable, with a per-subscriber override in `agent.hcl`). A subscriber that exceeds its timeout is treated as an error per the mode-specific handling above — critically, **fail-closed for veto**: a hanging policy subscriber at `plan-ready` MUST result in `deny`, not `allow`, because policy is the kernel-privileged veto subscriber ([`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category)) and its unavailability must not silently widen what gets auto-applied.

This is a deliberate divergence from designs that fail *open* on a review-timeout (inject an instruction telling the model not to assume the action is unsafe, leaving the decision otherwise unresolved) in exchange for less UX ambiguity. This kernel chooses fail-closed instead because `veto` mode is the terminal gate before a mutating action executes — the asymmetry between "block a turn" and "silently allow an unreviewed mutation" favors blocking. This choice is flagged in [Open questions](#open-questions) as worth revisiting if fail-closed proves too disruptive in practice.

## Parallelism within one hook point

Because `transform` subscribers depend on the prior subscriber's output and `veto` subscribers need ordered short-circuiting (to avoid running expensive downstream checks after an early deny), the kernel MUST dispatch `transform` and `veto` subscribers at a given hook point strictly sequentially, in declaration order.

`observe` subscribers, by construction, cannot affect payload or decision. The kernel MAY execute a maximal run of consecutive `observe`-mode subscribers concurrently with each other (they only need read access to the payload state at their declared position) as a latency optimization, but MUST NOT allow this to reorder or run ahead of neighboring `transform`/`veto` subscribers — an `observe` subscriber declared between two `transform` subscribers still sees exactly the payload state as of that point in the chain, and the `transform` subscribers on either side of it are unaffected by whether the `observe` call has completed.

A conforming kernel MUST instrument one hook point's whole dispatch as a single span covering the ordered subscriber chain, with a nested span per subscriber invocation — so concurrent `observe`-mode subscribers appear as sibling children in the resulting trace, matching the parallelism this section permits.

## Veto-mode subscription trust model

`HOOK_MODE_VETO` is open to **any** plugin declared in `agent.hcl` with a `hook{}` block at a veto-bearing point — it is not policy-exclusive. `agent.hcl` declaration *is* the operator's trust grant: an operator who writes a `hook { point = "plan-ready", mode = "veto" }` block naming a third-party plugin has explicitly opted that plugin into terminal-gate authority over applies, the same way declaring any resource-kind tool provider at all is already an implicit trust decision. There is no separate allowlist or first-party-only restriction layered on top of `agent.hcl` itself.

This does not diminish policy's own privileged position: the kernel-owned policy engine ([`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category)) is not a plugin at all and does not go through `HookSubscriberService` — it evaluates `plan-ready` directly and always runs, unconditionally, producing per-item `PlanDecision`s. A third-party `veto`-mode subscriber at `plan-ready` sits alongside policy in the same declaration-order chain and returns only the coarser `hook.v1.HookDecision` (`ALLOW`/`DENY`) over the whole payload — it cannot express `PlanDecision`'s per-item `PENDING`/`ALLOW`/`ASK`/`DENY` granularity, and it cannot override a `DENY` policy has already produced earlier in the chain (per [Dispatch order and payload flow](#dispatch-order-and-payload-flow), an explicit non-`allow` short-circuits the remaining subscribers at that point).

Third-party `veto` errors and timeouts still fail closed to `HOOK_DECISION_DENY`, identically to policy's own fail-closed behavior — [Timeout behavior](#timeout-behavior) above draws no distinction between a first-party and third-party veto subscriber's failure mode. A malfunctioning or slow third-party veto subscriber can only ever make the kernel more conservative (deny more), never less — it cannot widen what gets auto-applied by failing.

## Open questions

- **Veto-hook timeout fail-closed default.** Chosen over designs that fail open with explicit instructions because policy sits at the terminal mutation gate. Worth revisiting if fail-closed proves too disruptive to interactive UX in practice — there is no clear consensus among comparable systems here, only one adjacent precedent this design deliberately diverges from.
