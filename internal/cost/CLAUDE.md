# internal/cost — agent notes

- **Pure domain, no exceptions.** This package MUST NOT import `log/slog`
  or `internal/telemetry` — it is the `logging-telemetry.md` exemption
  category (I/O-free, deterministic, ~95%+ covered). The caller logs
  around it; nothing in here does.
- **Half-open interval semantics are the whole ballgame.** A `PricingTier`
  matches `(timestamp, inputTokens)` when `effective_from <= timestamp <
  effective_until` AND `input_tokens_from <= inputTokens <
  input_tokens_until`, each bound independently nilable meaning
  "unbounded on that side." This is documented in full on
  `ValidatePricing`'s doc comment in `pricing.go` — read it before
  touching `tierMatches`, the sampler, or the breakpoint collectors.
- **"Exactly one tier matches" is read literally, over the entire plane**
  — not just wherever tiers happen to be declared. A `Pricing` whose
  tiers don't extend to `nil`/unbounded on every outer edge (earliest
  `effective_from`, latest `effective_until`, lowest `input_tokens_from`,
  highest `input_tokens_until`) has a gap at that edge, and
  `ValidatePricing` will correctly reject it. When writing a fixture that
  is meant to isolate an *overlap* check, deliberately leave the outer
  edges unbounded (`nil`) so an unrelated edge gap doesn't fire first and
  mask the intended assertion — `pricing_test.go`'s
  `TestValidatePricingOverlapRejected` is the worked example of getting
  this right, and its history (see git blame) is a worked example of
  getting it wrong first.
- **Gap AND overlap detection via one grid-probe algorithm.** Because
  every tier is an axis-aligned rectangle in the `(time, input_tokens)`
  plane, the match-count function only changes value at a tier's own
  declared bounds. Collecting every distinct bound in each dimension and
  probing one representative point per resulting grid cell (including
  the two unbounded outer cells per dimension) is sufficient to catch any
  overlap or gap that could exist anywhere on the plane — not just
  probing the points themselves. `sampleTimePoints`/`sampleTokenPoints`
  build that per-dimension sample set; `ValidatePricing` takes the
  product. Don't replace this with a pairwise-overlap-only check (that's
  exactly `pkg/model`'s documented gap — see this package's `README.md`)
  and don't reach for a more general computational-geometry library; the
  grid-probe approach is exact for axis-aligned rectangles and stays
  within stdlib.
- **`ResolveTier` does not enforce "exactly one match."** If a `Pricing`
  slipped past `ValidatePricing` (or was never validated) and has
  multiple tiers matching the same point, `ResolveTier` deterministically
  returns the first match in declared order rather than erroring — there
  is no `ErrMultipleTiersMatch` in this package's API. `ValidatePricing`
  is the enforcement point; `ResolveTier` just needs to never panic and
  never silently guess when there's truly no match (`ErrNoMatchingTier`).
- **`reasoning_tokens` is billed at the OUTPUT rate, as its own term.**
  `Compute` in `compute.go` has five terms, not four with reasoning
  folded into output — this is easy to get wrong by "simplifying" the sum
  to `(output_tokens + reasoning_tokens) * output_per_mtok`, which
  happens to produce the identical number but obscures that
  `reasoning_tokens` is a structurally distinct, never-double-counted
  counter per `docs/specifications/model/data-types.md#streamevent`. Keep
  the five terms textually separate, matching the formula in
  `protocol.md#cost-computation`.
