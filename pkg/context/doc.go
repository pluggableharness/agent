// Package context is the plugin-author-facing Go SDK for the context
// provider category described in
// docs/specifications/context/README.md,
// docs/specifications/context/protocol.md,
// docs/specifications/context/data-types.md, and
// docs/specifications/context/conformance.md.
//
// A context provider plugin hooks context-assemble and contributes text
// content to the prompt before each model call — a CLAUDE.md reader, an
// AGENTS.md reader, a git-status/file-tree summarizer, or a compactor that
// rewrites the assembled chain and/or the conversation history under
// budget pressure (docs/specifications/context/protocol.md#session-wide-conversation-compaction).
// This package gives a plugin author an idiomatic Provider interface to
// implement (context.go) plus the wiring that turns it into a real
// pluggableharness.context.v1.ContextService server (server.go): schema
// validation, error-taxonomy mapping to gRPC status codes (errors.go),
// domain/proto conversion (convert.go), and a GetCapabilities builder
// (capabilities.go).
//
// # Domain types vs. generated types
//
// [Capabilities], [Section], [Request], and
// [Contribution] are this package's own Go types, not the
// generated pkg/context/proto/v1 messages — convert.go translates between
// them at the server.go boundary. This is a deliberate departure from
// go-layout.md's general "exactly one Go representation of each wire
// message" rule for kernel-side client stubs: the value here is real, not
// cosmetic. [Section.Content] collapses the wire's
// []ContentBlock into a plain string, which is what "text-only in v1"
// (data-types.md#contextsection) actually means for an author — there is
// no way to accidentally construct a multi-block or non-text section
// through this type. Sub-messages that already carry their own SDK
// ownership — model.v1.ModelTarget, config.v1.ConfigSchema,
// common.v1.PromptExpansionSpec, common.v1.HookPoint,
// content.v1.Message — are passed through unwrapped; duplicating those
// here would just be a second, driftable copy of another package's type.
//
// # Import alias
//
// This package's own name, "context", collides with the standard
// library's "context" package that every Provider method and RPC handler
// also needs for its ctx context.Context parameter. That is not a
// conflict inside this package's own files — a package's declarations are
// unqualified within itself, so context.go freely imports stdlib
// "context" for context.Context. A CONSUMER that needs both packages in
// the same file MUST alias one of the two imports, e.g.:
//
//	import (
//		"context"
//
//		pluggablecontext "github.com/pluggableharness/agent/pkg/context"
//	)
//
// This package's type names ([Capabilities], [Request],
// [Section], [Contribution], [Error]) intentionally
// mirror the wire message names in pkg/context/proto/v1 and the spec text
// verbatim, at the cost of the usual no-package-stutter convention — a
// reader moving between this SDK, the generated stubs, and
// docs/specifications/context/ sees one consistent vocabulary throughout,
// which matters more here than avoiding "context.Request" reading
// redundant under an aliased import.
package context
