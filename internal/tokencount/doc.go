// Package tokencount implements the kernel's single canonical token-counting
// primitive, backing the KernelCallbackService.CountTokens RPC
// (docs/specifications/kernel-callbacks.md#counttokens).
//
// Count resolves an exact count when a model provider's own optional
// CountTokens RPC (docs/specifications/model/protocol.md#counttokens) is
// reachable and implemented, falling back to the one documented heuristic
// (docs/specifications/kernel-callbacks.md#the-fallback-heuristic,
// .claude/rules/determinism.md#the-fallback-token-heuristic) otherwise, per
// docs/specifications/kernel-callbacks.md#resolution-algorithm.
//
// There is exactly one fallback formula in this codebase — Fallback — and
// it MUST NOT gain a second, content-type-aware variant. See this
// package's own CLAUDE.md.
package tokencount
