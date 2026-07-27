// Package model is the hand-written, ergonomic SDK a model (LLM vendor)
// provider plugin author builds against, sitting on top of the generated
// pluggableharness.model.v1 types in ./proto/v1. It implements the model
// provider protocol described in docs/specifications/model/README.md,
// docs/specifications/model/protocol.md, docs/specifications/model/data-types.md,
// and docs/specifications/model/conformance.md.
//
// # Shape
//
// A plugin author implements Provider — Capabilities, Configure, and
// StreamCompletion, the three MUST RPCs
// (docs/specifications/model/conformance.md's summary matrix) — and
// optionally TokenCounter and Renderer for the SHOULD/MAY RPCs CountTokens
// and Render (docs/specifications/model/protocol.md#counttokens,
// docs/specifications/model/protocol.md#render). NewService adapts a
// Provider into the generated modelv1.ModelServiceServer, implementing
// Describe itself from a plugin.Identity (docs/specifications/model/protocol.md#describe)
// so no author code is needed for that RPC.
//
// # Domain types vs. generated types
//
// model.go defines Go-idiomatic domain types — Capabilities, Spec,
// ThinkingSpec, CachingSpec, Pricing, PricingTier, Usage — for the values a
// plugin author actually constructs by hand (typically a small, hardcoded
// model list built once at process start,
// docs/specifications/model/protocol.md#getcapabilities's "ship a built-in
// list" guidance). These trade the generated types' pointer-heavy optional
// fields (e.g. PricingTier.CacheWritePerMtok *float64) for plain Go
// zero-value-friendly fields wherever the wire's presence/absence
// distinction still needs to survive (time.Time via a pointer, not
// timestamppb.Timestamp; float64 via a pointer only where "unset" is
// itself meaningful). convert.go translates between these domain types and
// their generated modelv1 counterparts in both directions.
//
// StreamCompletionRequest and its nested types (Message, ToolDeclaration,
// GenerationParams, CacheBreakpoint, ...) are deliberately NOT mirrored
// into a parallel domain shape — Provider.StreamCompletion takes the
// generated *modelv1.StreamCompletionRequest directly. That message is
// already the canonical wire/domain shape
// (docs/specifications/model/data-types.md's "Canonical message &
// content-block schema": "the state backend's source of truth, independent
// of any one vendor's wire format"), an adapter reads every nested field
// to build its vendor's own request regardless of any intermediate shape,
// and mirroring it would only be a lossy, purely duplicative copy.
//
// StreamEvent is likewise not mirrored as a struct an author constructs
// and returns; stream.go's Sink is the domain-friendly StreamEvent
// surface instead — one method per variant (TextDelta, ThinkingDelta,
// ToolCallStart, ...), so an author never touches modelv1.StreamEvent's
// oneof directly, and Sink enforces the "exactly one terminal event"
// invariant (docs/specifications/model/data-types.md#streamevent)
// mechanically rather than by convention.
//
// # Errors
//
// errors.go's Error is the domain shape of the structured error
// taxonomy every failure crossing this plugin boundary MUST classify into
// (docs/specifications/model/conformance.md#error-taxonomy). Every
// RPC-boundary error in this package's server.go goes through
// pkg/plugin.StatusError, never a bare gRPC status.
package model

// ProtocolVersion is the version of the model category's own protocol this
// SDK implements — the "v1" in pluggableharness.model.v1.
//
// Deliberately NOT pkg/common.ProtocolVersion, which versions the
// go-plugin runtime contract shared by every category. The two move
// independently: see that constant's documentation for why.
const ProtocolVersion uint32 = 1
