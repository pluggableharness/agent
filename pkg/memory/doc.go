// Package memory is the hand-written, plugin-author-facing SDK for the
// memory provider category — plugins that persist knowledge across
// sessions and recall it into future ones
// (docs/specifications/memory/README.md). A memory provider plugin exposes
// nine MUST RPCs (GetCapabilities, Configure, Recall, Record, UpdateRecord,
// DeleteRecord, ListRecords, GetRecord, Describe) plus two optional
// surfaces: the ratification pattern (ApproveRecord/RejectRecord, MAY
// together, never alone — docs/specifications/memory/protocol.md#ratification-optional)
// and Render (docs/specifications/memory/protocol.md#render). All twelve
// RPCs are unary — nothing in this category streams
// (docs/specifications/memory/README.md#transport--lifecycle).
//
// # Domain types, not raw generated types
//
// A plugin author implements Provider (and, optionally,
// RatificationProvider and Renderer) against the domain types declared in
// this file — Type, Scope, RecordStatus, Record, Provenance,
// Capabilities, and the per-RPC request/result shapes — rather than the
// generated pkg/memory/proto/v1 wire types directly. convert.go translates
// between the two, both directions, so an author never has to reach for a
// protobuf-generated enum constant or build a []*contentv1.ContentBlock by
// hand for the text-only-in-v1 content this category carries
// (docs/specifications/memory/data-types.md#recallrequest--memoryrecord).
//
// server.go's Service adapts a Provider into the generated
// memoryv1.MemoryServiceServer, satisfying pkg/plugin.Service so a plugin
// author's main() can pass it straight to plugin.Config.Services.
//
// # The fixed taxonomy
//
// Type (user/feedback/project/reference) and Scope
// (session/project/global) are fixed at the protocol level, not
// provider-defined (docs/specifications/memory/taxonomy.md). A provider MAY
// support only a subset of either, declared exactly via
// Capabilities.SupportedTypes/SupportedScopes. Both fields are immutable on
// a Record once created — UpdateRecordRequest deliberately carries no way
// to set either, enforcing that MUST at the Go type level rather than by
// runtime validation alone (docs/specifications/memory/protocol.md#record-updaterecord-deleterecord-the-write-side).
//
// # Ratification is both-or-neither, enforced structurally
//
// RatificationProvider embeds Provider and adds ApproveRecord/RejectRecord.
// Because Go interface satisfaction is all-or-nothing, a Provider
// implementation either satisfies RatificationProvider in full or not at
// all — there is no way to "implement only one" and have server.go treat it
// as ratification-capable. NewService performs the type assertion once at
// construction and uses that result, not whatever a Provider's own
// Capabilities.RatificationSupported claims, as the authoritative signal
// wired into the outgoing GetCapabilities response and into ApproveRecord/
// RejectRecord routing — see server.go's NewService doc comment.
//
// # Token counting is a kernel callback, never a local heuristic
//
// MemoryRecord.Tokens MUST be computed via the kernel's CountTokens
// callback (docs/specifications/kernel-callbacks.md#counttokens), never a
// provider-local heuristic. CountTokens in this package is the obvious,
// hard-to-avoid call for that.
package memory

// ProtocolVersion is the version of the memory category's own protocol this
// SDK implements — the "v1" in pluggableharness.memory.v1.
//
// Deliberately NOT pkg/common.ProtocolVersion, which versions the
// go-plugin runtime contract shared by every category. The two move
// independently: see that constant's documentation for why.
const ProtocolVersion uint32 = 1
