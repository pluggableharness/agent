// Package slashcommand implements the hand-written, plugin-author-facing
// Go SDK for the slash-command provider category — a plugin that declares
// and directly executes one or more slash commands in its own right
// (/deploy, /release-notes, ...), rather than merely expanding a static
// prompt template (docs/specifications/slashcommand/README.md). It sits
// directly on top of the generated pkg/slashcommand/proto/v1 stubs
// (slashcommandv1) and the shared foundation packages (pkg/plugin,
// pkg/config, pkg/schema, pkg/render): a plugin author implements
// Provider, builds a *Service with NewService, and passes it to
// plugin.Config.Services before calling plugin.Serve.
//
// See docs/specifications/slashcommand/protocol.md for the six RPCs this
// package wires up (GetCapabilities, Configure, Invoke, Render, Preview,
// Describe — the first two plus Invoke and Describe MUST be implemented by
// every provider; Render and Preview MAY),
// docs/specifications/slashcommand/data-types.md for the
// Spec / Call / Event shapes, and
// docs/specifications/slashcommand/conformance.md for the reused error
// taxonomy and the full MUST/SHOULD/MAY summary matrix this package
// enforces where it can.
//
// # Types reused verbatim from pkg/tool
//
// docs/specifications/slashcommand/data-types.md#reused-toolv1-types
// mandates that this category reuse six pluggableharness.tool.v1 types
// VERBATIM, with no parallel redeclaration under
// pluggableharness.slashcommand.v1: ToolKind, RiskClass, ConcurrencySpec,
// ToolResult, ToolError, and OutputStream — their Go-domain-level
// counterparts used throughout this package's code are tool.Kind,
// tool.RiskClass, tool.ConcurrencySpec, tool.Result, tool.Error, and
// tool.OutputStream, imported directly from
// github.com/pluggableharness/agent/pkg/tool. This is not incidental
// convergence: a direct-invoke command flows through the identical
// plan/apply gate a tool call does, so it needs the identical
// classification vocabulary. On the wire, the generated
// SlashCommandSpec.kind/risk/concurrency fields and
// SlashCommandEvent.result/error/OutputChunk.stream fields are typed
// directly as the pluggableharness.tool.v1 message/enum — see
// pkg/slashcommand/proto/v1/types.pb.go and events.pb.go — not as any
// slashcommand.v1-local equivalent.
//
// A future reader MUST NOT add a "SlashCommandKind", "SlashCommandRisk",
// "SlashCommandResult", "SlashCommandError", or similar type to this
// package out of habit — extend pkg/tool instead if one of the six ever
// needs to change shape, and update every consumer (including this
// package) in lockstep.
package slashcommand
