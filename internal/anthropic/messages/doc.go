// Package messages is the Anthropic wire adapter: the only place in this
// repository that knows Anthropic's own JSON format.
//
// It translates in both directions. Outbound, a canonical
// modelv1.StreamCompletionRequest becomes an Anthropic request body —
// assembled-context sections become the top-level system array, canonical
// content blocks become Anthropic content blocks, the restricted
// schema.v1 subset becomes a tool's input_schema, and the kernel's
// cache breakpoints become vendor cache_control markers. Inbound,
// Anthropic's server-sent events become model.Sink calls.
//
// The vendor JSON types in types.go describe Anthropic's schema, not a
// second Go representation of a PluggableHarness wire message —
// docs/specifications/architecture.md#canonical-message--tool-schema-format
// assigns each adapter exactly this translation job, and doing it needs
// both shapes present in Go. See CLAUDE.md before concluding otherwise.
//
// Two invariants in this package are load-bearing and non-obvious, and
// CLAUDE.md explains both at length:
//
//   - Anything derived from protobuf that reaches the wire is serialized
//     with encoding/json over native Go values, never protojson.
//     Anthropic's prompt cache is a byte-exact prefix match, and
//     protojson deliberately emits non-deterministic whitespace, so a
//     single use of it silently disables caching for the rest of any
//     conversation containing a tool call.
//   - Thinking signatures and redacted-thinking payloads are carried as
//     the literal bytes of the vendor's base64 text and are never decoded
//     or re-encoded. A re-encoding that differs in padding or alphabet
//     fails the vendor's integrity check, which rejects the whole
//     conversation on the next turn.
//
// This package computes no cost, places no cache breakpoints, and
// performs no retries — all three are the kernel's, per
// docs/specifications/model/protocol.md and .claude/rules/grpc.md.
package messages
