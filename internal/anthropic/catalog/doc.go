// Package catalog is the Anthropic model roster: one model.Spec per model
// the provider plugin can serve, including the pricing the kernel uses to
// compute and persist cost_usd
// (docs/specifications/model/protocol.md#cost-computation).
//
// It is pure data with no I/O. GetCapabilities MUST be cheap to call
// repeatedly and MUST NOT require a network call to the vendor
// (docs/specifications/model/protocol.md#getcapabilities), so the roster
// is a compiled-in table rather than a live query against the vendor's
// /v1/models endpoint.
//
// Every figure here is transcribed from Anthropic's own published
// documentation on the date recorded in the sourcedOn constant. Cost
// figures in particular are load-bearing: the kernel computes cost_usd
// from Pricing at the moment each usage event arrives and persists the
// dollar amount forever, so a wrong rate here is a permanent, silently
// incorrect ledger row, not a display bug — see
// .claude/rules/determinism.md and this package's CLAUDE.md.
package catalog
