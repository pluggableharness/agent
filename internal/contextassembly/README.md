# internal/contextassembly

Implements step 1 of `RunTurn` ([`docs/specifications/agent-loop/turn-algorithm.md`](../../docs/specifications/agent-loop/turn-algorithm.md)): running every loaded context provider's `ContextService.Contribute` RPC, in `agent.hcl` declaration order, to build the turn's assembled prompt context.

## What it does

`Assembler.Assemble` calls each `providercatalog.ContextHandle`'s `Contribute` in order (`ContextHandle.Position`), threading the accumulated `[]ContextSection` chain from one provider's request to the next, exactly as [`context/protocol.md#contribute-the-context-assemble-rpc`](../../docs/specifications/context/protocol.md#contribute-the-context-assemble-rpc) requires. After each provider's call it:

- Enforces the own-section-only scope rule for a non-compactor: if a provider's response mutates, reorders, or drops a section it doesn't own, the entire response is discarded and the chain reverts to what it was before that call ([`context/data-types.md#ordering--chaining`](../../docs/specifications/context/data-types.md#ordering--chaining)).
- Recomputes each of the provider's own section(s)' token count via `internal/tokencount.Counter` — never trusting a provider-reported value — and drops any section that exceeds the provider's resolved token budget, or that carries a non-text content block ([`context/data-types.md#budget-mechanics`](../../docs/specifications/context/data-types.md#budget-mechanics), [`#contextsection`](../../docs/specifications/context/data-types.md#contextsection)). Either rejection is per-section, never a whole-turn failure.
- Persists one `context_contribution` event per provider whose contribution survives validation ([`state-backend.md#the-kind-enum`](../../docs/specifications/state-backend.md#the-kind-enum)).
- Tracks a compactor's `rewritten_history`, returning it in `Result` for the caller to swap in before the next model call ([`context/protocol.md#session-wide-conversation-compaction`](../../docs/specifications/context/protocol.md#session-wide-conversation-compaction)).

A genuine RPC-level failure from a provider's `Contribute` call aborts the rest of the chain and returns an error — this is deliberately different from the two isolated, per-provider conditions above, mirroring [`agent-loop/hook-dispatch.md`](../../docs/specifications/agent-loop/hook-dispatch.md)'s transform-mode failure handling: a context-assemble failure means the model is about to see an unintended context state, serious enough to surface rather than swallow.

## What it is not

**`context-assemble` is not a `hook.v1` `HookSubscriberService` dispatch.** It stays on the context category's own `ContextService.Contribute` RPC, which already carries the full `ContextSection` chain as a typed request/response. This package never imports `internal/hookdispatch`, and never will — see this package's `CLAUDE.md` for the full reasoning.

## Layout

A single concrete `Assembler` type, not the interface/driver family shape most `internal/` packages follow — there is exactly one way to run a context-assemble firing, and nothing here is swappable per `go-layout.md`'s driver pattern (mirrors `internal/tokencount`'s own single-`Counter` shape).
