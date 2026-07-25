// Package cost implements the kernel-side pricing-tier resolution and
// cost-computation formula described in
// docs/specifications/model/protocol.md#cost-computation and
// docs/specifications/model/data-types.md#pricing.
//
// A model provider plugin reports raw token counts (input, output,
// cache-read, cache-write, reasoning) on every usage event; converting
// those counts into a dollar figure is a kernel responsibility, not the
// plugin's, because only the kernel holds the resolved ModelSpec.pricing
// at the moment the event is received. This package is that conversion:
// ValidatePricing rejects a malformed Pricing value at capability-load
// time (before any completion is ever billed against it), ResolveTier
// picks the single PricingTier that governs one usage event, and Compute
// applies the five-term cost formula against the resolved tier.
//
// This is pure domain logic: no I/O, no logging, no clock reads beyond
// the timestamp a caller passes in. Per .claude/rules/determinism.md, the
// dollar figure this package computes gets persisted into the state
// backend once and must reproduce byte-for-byte on replay — it is
// computed once, at usage-event time, using whichever plugin version was
// active at that moment, and never recomputed later against a newer
// Pricing value. A caller MUST resolve the tier and compute the cost
// immediately upon receiving each usage event and persist the result,
// never defer either step to read time.
package cost
