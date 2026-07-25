// Package callhash implements deterministic hashing and canonicalization for tool calls.
//
// It provides two core functions:
//   - [Call]: Computes the deterministic hash of one tool call for doom-loop detection,
//     per docs/specifications/agent-loop/turn-algorithm.md#doom-loop-detection.
//   - [Fields]: Canonicalizes the named key_fields' values for concurrency key formation,
//     per docs/specifications/tool/data-types.md#concurrencyspec.
//
// Both use an identical underlying canonical JSON encoding ([Canonical]) to ensure
// one call site computes the deterministic serialization, never two separate ones
// (docs/specifications/determinism.md).
package callhash
