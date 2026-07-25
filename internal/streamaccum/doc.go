// Package streamaccum implements steps 3-4 of the kernel's RunTurn
// algorithm — docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm's
// `message := accumulate(stream)` — turning a model provider's
// StreamCompletion event sequence into one canonical content-block Message.
//
// The wire vocabulary an Accumulator consumes is the StreamEvent oneof
// described in full at docs/specifications/model/data-types.md#streamevent;
// docs/specifications/model/examples.md#a-full-streamcompletion-event-sequence
// is a worked event sequence and this package's primary test fixture
// source. The Message/ContentBlock shape an Accumulator produces is the
// canonical content-block form described at
// docs/specifications/architecture.md#canonical-message--tool-schema-format.
//
// This is pure domain logic: no I/O, no clock reads, no logging. Per
// .claude/rules/logging-telemetry.md's pure-domain exemption, it MUST NOT
// import log/slog or internal/telemetry — a caller logs or spans around a
// call into this package, never this package itself. An Accumulator is not
// safe for concurrent use: a model provider streams StreamEvent values from
// a single goroutine, in order, and Observe MUST be called in that same
// order.
//
// This package does no vendor-specific decoding. A ThinkingBlock's
// signature and a RedactedThinkingBlock's data are stored and returned as
// the raw bytes the wire carried — never base64-decoded, never assumed to
// be any particular text encoding. That translation, if any vendor's wire
// format needs one, is a model-provider adapter's job, not the kernel's;
// see this package's CLAUDE.md for why that boundary matters.
package streamaccum
