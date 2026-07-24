// Package tool implements the hand-written, plugin-author-facing Go SDK
// for the tool provider category — file I/O, shell execution, search, web
// access, task tracking, and sub-agent spawning
// (docs/specifications/tool/README.md). It sits directly on top of the
// generated pkg/tool/proto/v1 stubs (toolv1) and the shared foundation
// packages (pkg/plugin, pkg/config, pkg/schema, pkg/render): a plugin
// author implements Provider, builds a *Service with NewService, and
// passes it to plugin.Config.Services before calling plugin.Serve.
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
