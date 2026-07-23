# Hook dispatch

[`architecture.md`](../architecture.md#hook-dispatch-semantics) establishes the ordered-chain model (declaration order, three subscriber modes — `observe`/`transform`/`veto`) but leaves mechanics unspecified; this document is that mechanics layer. No surveyed harness exposes a generalized, third-party-pluggable hook-dispatch subsystem in this shape, so the following is this kernel's own design, informed by the closest analogous patterns where one exists.

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

## Open questions

- **Veto-hook timeout fail-closed default.** Chosen over designs that fail open with explicit instructions because policy sits at the terminal mutation gate. Worth revisiting if fail-closed proves too disruptive to interactive UX in practice — there is no clear consensus among comparable systems here, only one adjacent precedent this design deliberately diverges from.
- **Whether third-party plugins may register `veto`-mode hooks at all**, or whether `veto` is policy-exclusive. Carried forward from [`architecture.md`](../architecture.md#policy--first-party-not-a-plugin-category) — this is a plugin trust-model question that cross-harness comparison doesn't settle, and this document's dispatch mechanics apply equally either way once that's decided.
