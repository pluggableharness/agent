package cost

import modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

// Compute applies docs/specifications/model/protocol.md#cost-computation's
// five-term formula to usage u against the already-resolved tier t.
// Callers get t from ResolveTier; Compute itself does no tier resolution
// and performs no validation of t or u — a nil t or nil u is treated as
// all-zero rates/counts (the generated proto getters this function reads
// through are nil-safe), yielding a cost of 0.
//
// The five terms are a plain sum — per
// docs/specifications/model/data-types.md#pricing and
// protocol.md#cost-computation, cache-read/cache-write/reasoning tokens
// are never double-counted inside input_tokens/output_tokens as vendors
// report them, so there is nothing to subtract. reasoning_tokens is
// billed at the OUTPUT rate as its own term — it is never folded into
// output_tokens.
func Compute(t *modelv1.PricingTier, u *modelv1.Usage) float64 {
	const perMillion = 1e6

	inputCost := float64(u.GetInputTokens()) * t.GetInputPerMtok() / perMillion
	outputCost := float64(u.GetOutputTokens()) * t.GetOutputPerMtok() / perMillion
	cacheWriteCost := float64(u.GetCacheWriteTokens()) * t.GetCacheWritePerMtok() / perMillion
	cacheReadCost := float64(u.GetCacheReadTokens()) * t.GetCacheReadPerMtok() / perMillion
	reasoningCost := float64(u.GetReasoningTokens()) * t.GetOutputPerMtok() / perMillion

	return inputCost + outputCost + cacheWriteCost + cacheReadCost + reasoningCost
}
