# tokencount

The kernel's single canonical token-counting primitive, backing `KernelCallbackService.CountTokens` (`docs/specifications/kernel-callbacks.md#counttokens`).

## Overview

`Counter.Count` resolves a token count for a slice of `content.v1.ContentBlock`, optionally against a `model.v1.ModelRef` naming which provider/model to count exactly against:

1. No `model_ref` (nil, or an empty `provider`) — use the fallback heuristic.
2. `model_ref.provider` previously found to answer `codes.Unimplemented` for `CountTokens` — use the fallback, without a round trip (memoized for this `Counter`'s lifetime).
3. `model_ref.provider` not currently loaded (`ModelLookup.ModelClientByLocalName` misses) — use the fallback.
4. Otherwise, call that provider's own `CountTokens` RPC (`docs/specifications/model/protocol.md#counttokens`):
   - success — return its count, `exact: true`.
   - `codes.Unimplemented` — memoize the provider and use the fallback.
   - `codes.Canceled` / `codes.DeadlineExceeded` — use the fallback, without logging it as a failure (cancellation is normal control flow).
   - any other error — use the fallback, with a throttled `WARN` (not memoized, so a transient error doesn't permanently downgrade a provider that might succeed next time).

`Count` never returns an error — a counting primitive that could fail would turn every context/memory provider's `tokens` field computation into a failure path.

## The fallback heuristic

`Fallback(blocks)` computes `ceil(total_utf8_byte_length(text_of(blocks)) / 4)`, per `docs/specifications/kernel-callbacks.md#the-fallback-heuristic` and `.claude/rules/determinism.md#the-fallback-token-heuristic`. Non-text content blocks (tool calls, images, etc.) contribute nothing in v1.

This is the **only** fallback formula anywhere in this codebase — see this package's `CLAUDE.md`.

## `ModelLookup`

`Counter` doesn't know how model providers are registered — it declares the one-method `ModelLookup` interface it needs (`ModelClientByLocalName(name string) (modelv1.ModelServiceClient, bool)`) and a caller supplies an implementation backed by whatever plugin registry exists at that point in the kernel's build-out. `name` is the provider's `agent.hcl` local name, not its self-reported producer name.
