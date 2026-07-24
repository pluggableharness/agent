// Package content provides the hand-written, ergonomic builder layer over
// the generated pluggableharness.content.v1 types in ./proto/v1 — the
// canonical content-block schema that every model-provider adapter and the
// frontend protocol exchange in place of a plain string.
//
// docs/specifications/model/data-types.md's "Canonical message &
// content-block schema" section is the primary owner of this shape: a
// Message carries `repeated content.v1.ContentBlock content`, never a bare
// string, and a ContentBlock is a typed oneof over exactly seven variants —
// text, tool_use, tool_result, image, thinking, redacted_thinking, and
// document. docs/specifications/frontend/frontend-protocol.md's
// ClientEvent.UserMessage note makes the same point from the frontend
// side: UserMessage.content is `repeated pluggableharness.content.v1.ContentBlock`,
// and field 1 — the message's original bare `text` field — is `reserved`
// and MUST NOT be reused, precisely so a frontend has an entry point for
// non-text input (e.g. a pasted image) that a plain string never could
// represent.
//
// This package exists because the generated oneof-of-message shape is
// verbose to construct by hand at every call site — a plugin author
// wanting to emit a single text block would otherwise write out
// &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text:
// &contentv1.TextBlock{Text: "..."}}} themselves. The functions in
// blocks.go do that wrapping once, and options.go adds a functional
// option for the one variant — DocumentBlock's optional filename — that
// needs one.
package content
