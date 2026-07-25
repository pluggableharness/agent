# internal/cost

Kernel-side pricing-tier resolution and cost-computation formula, per [`docs/specifications/model/protocol.md#cost-computation`](../../docs/specifications/model/protocol.md#cost-computation) and [`docs/specifications/model/data-types.md#pricing`](../../docs/specifications/model/data-types.md#pricing).

## What this package does

A model provider plugin reports raw token counts on every `usage` event
(input, output, cache-read, cache-write, reasoning). Turning those counts
into a persisted dollar figure is the kernel's job, not the plugin's — the
kernel is the only side that has both the counts and the resolved
`ModelSpec.pricing` at the moment the event arrives:

- `ValidatePricing` — rejects a malformed `Pricing` value at
  capability-load time: two tiers that overlap (both match some
  `(timestamp, input_tokens)` pair) or a gap (some pair matched by no
  tier at all). See its doc comment for the exact half-open interval
  semantics and the grid-probe detection method.
- `ResolveTier` — given a `Pricing`, a timestamp, and an input-token
  count, returns the single `PricingTier` that governs that usage event.
- `Compute` — applies the five-term cost formula to a `Usage` against an
  already-resolved `PricingTier`.

## How it fits in

The kernel calls these three functions in sequence whenever a
`StreamCompletion` usage event arrives: `ResolveTier` against the active
`ModelSpec.pricing` and the event's timestamp/`input_tokens`, then
`Compute` against the resolved tier and the full `Usage`. The resulting
`cost_usd` is persisted into the state backend event's payload
immediately — never recomputed later, per
[`.claude/rules/determinism.md`](../../.claude/rules/determinism.md)'s
replay-fidelity requirement. `ValidatePricing` runs once, when a model
provider's `GetCapabilities` response is first validated, before any
completion is ever billed against it.

## Relationship to `pkg/model/capabilities.go`

`pkg/model`'s `validatePricing` (unexported, plugin-author SDK side) also
checks `Pricing` for overlapping tiers, but it is documented there as
overlap-only — it does not detect a gap between two non-overlapping
tiers, a known, intentional limitation of that validator. This package's
`ValidatePricing` is a separate, kernel-side implementation that detects
both, because kernel-side cost computation is what actually gets
persisted and must not silently accept a `Pricing` value with an
unmatched region. Do not import or depend on `pkg/model`'s validator from
here, and do not "fix" its gap by importing this package's logic back
into `pkg/model` — the two stay independent, per the task that created
this package.
