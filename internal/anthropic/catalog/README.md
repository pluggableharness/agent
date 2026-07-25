# internal/anthropic/catalog

The Anthropic model roster — one [`model.Spec`](../../../pkg/model/model.go) per model the provider plugin can serve, with the context window, output ceiling, capability flags, reasoning controls, and pricing the kernel needs to route to it and to bill for it.

Pure data. No I/O, no network, no vendor call. [`protocol.md#getcapabilities`](../../../docs/specifications/model/protocol.md#getcapabilities) requires `GetCapabilities` to be cheap to call repeatedly and to avoid a vendor round trip, so the roster is a compiled-in table refreshed by editing this package, not by querying `/v1/models` at runtime.

## The roster

Eight models, newest generation first:

| Model | Context | Max output | Input $/MTok | Output $/MTok | Reasoning control |
|---|---|---|---|---|---|
| `claude-fable-5` | 1M | 128k | 10.00 | 50.00 | effort ladder, cannot be disabled |
| `claude-opus-5` | 1M | 128k | 5.00 | 25.00 | effort ladder |
| `claude-opus-4-8` | 1M | 128k | 5.00 | 25.00 | effort ladder |
| `claude-opus-4-7` | 1M | 128k | 5.00 | 25.00 | effort ladder |
| `claude-opus-4-6` | 1M | 128k | 5.00 | 25.00 | effort ladder (no `xhigh`) |
| `claude-sonnet-5` | 1M | 128k | 2.00 → 3.00 | 10.00 → 15.00 | effort ladder |
| `claude-sonnet-4-6` | 1M | 128k | 3.00 | 15.00 | effort ladder (no `xhigh`) |
| `claude-haiku-4-5` | 200k | 64k | 1.00 | 5.00 | token budget |

Claude Sonnet 5's two rates are the introductory price (through 2026-08-31) and the standard price that follows — modeled as two adjacent, half-open [`PricingTier`](../../../docs/specifications/model/data-types.md#pricing) windows rather than a single figure, so a session run on either side of the cutover replays showing the rate it actually paid.

## Two deliberate omissions

- **Claude Mythos 5** shares Fable 5's specs and pricing exactly, but access is invitation-only through Project Glasswing. Advertising it would make it a routing candidate that fails at request time for nearly every operator.
- **Claude Opus 4.1** and everything older is deprecated or retired. A deprecated model in the roster is a fallback candidate that stops working on a date nobody is watching for.

## Why the pricing figures are treated as load-bearing

The kernel computes `cost_usd` from `Pricing` at the instant each `usage` event arrives and **persists the dollar figure**, per [`protocol.md#cost-computation`](../../../docs/specifications/model/protocol.md#cost-computation). Nothing recomputes it later. A mistyped rate here is therefore a permanently wrong `cost_ledger` row in every session that ran against it — a correctness bug under [`determinism.md`](../../../.claude/rules/determinism.md), not a display issue.

`catalog_test.go` guards against transcription errors rather than restating the table: Anthropic publishes cache and batch rates as fixed multipliers of the base rate (cache write 1.25x input, cache read 0.1x input, batch 0.5x both directions), so the tests assert those ratios hold. A mistyped digit breaks a ratio even when the number still looks plausible on its own.

## Updating the roster

See [`CLAUDE.md`](CLAUDE.md) for the procedure and the rule about where the numbers may come from.
