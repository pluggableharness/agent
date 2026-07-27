// Package tool implements the hand-written, plugin-author-facing Go SDK
// for the tool provider category — file I/O, shell execution, search, web
// access, task tracking, and sub-agent spawning
// (docs/specifications/tool/README.md). It sits directly on top of the
// generated pkg/tool/proto/v1 stubs (toolv1) and the shared foundation
// packages (pkg/plugin, pkg/config, pkg/schema, pkg/render).
//
// # The unit of implementation is one Tool
//
// One plugin process serves as many operations as it likes — this is what
// the wire contract already describes, with GetSchemaResponse.tools
// repeated and every ToolCall naming which of them to run. This package
// mirrors that directly: a plugin author writes one Tool per operation,
// each owning its own Schema and Invoke, and a Provider that returns them:
//
//	type FileRead struct{ root string }
//
//	func (t *FileRead) Schema() (*tool.Schema, error) { ... }
//	func (t *FileRead) Invoke(ctx context.Context, c *tool.Call, s *tool.Stream) error { ... }
//
//	func (p *fsProvider) Tools() []tool.Tool {
//		return []tool.Tool{&FileRead{p.root}, &FileWrite{p.root}, &FileDelete{p.root}}
//	}
//
// The author then builds a *Service with NewService and passes it to
// plugin.Config.Services before calling plugin.Serve. Service owns the
// name-keyed dispatch from an incoming Call to the Tool that serves it, so
// no implementation in this package's care ever switches on Call.ToolName,
// and a tool's declared schema cannot drift from the code behind it.
//
// Provider keeps only what is genuinely plugin-wide: the tool set,
// Configure, and the optional ConfigSchemaProvider, SlashCommandProvider,
// HookPointProvider, and Renderer. Previewer, by contrast, is per Tool —
// see its documentation in tool.go, and Renderer's, for why the two
// optional render-side interfaces sit at different levels.
//
// See docs/specifications/tool/protocol.md for the six RPCs this package
// wires up (GetSchema, Configure, Invoke, Render, Preview, Describe — the
// first three plus Describe MUST be implemented by every provider; Render
// and Preview MAY), docs/specifications/tool/data-types.md for the
// Schema / Call / Event / Result / ConcurrencySpec shapes,
// and docs/specifications/tool/conformance.md for the ErrorCategory
// taxonomy and the full MUST/SHOULD/MAY summary matrix this package
// enforces where it can.
//
// # Types shared verbatim with pkg/slashcommand
//
// docs/specifications/slashcommand/data-types.md mandates that the
// slashcommand category reuse six of this package's types VERBATIM, with
// no parallel redeclaration: Kind, RiskClass, ConcurrencySpec,
// Result, Error, and OutputStream. All six are declared at this
// package's top level specifically so pkg/slashcommand can import and use
// them directly, and none of the six embeds anything Call-specific (no
// tool_name, no call ID, nothing that wouldn't make equal sense reused for
// a slash command's own direct-invoke result). A future reader MUST NOT
// rename, reshape, or fork these six types into pkg/slashcommand or any
// other package — extend this package instead if their shape ever needs
// to change, and update every consumer in lockstep.
package tool

// ProtocolVersion is the version of the tool category's own protocol this
// SDK implements — the "v1" in pluggableharness.tool.v1.
//
// Deliberately NOT pkg/common.ProtocolVersion, which versions the
// go-plugin runtime contract shared by every category. The two move
// independently: see that constant's documentation for why.
const ProtocolVersion uint32 = 1
