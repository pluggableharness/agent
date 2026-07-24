// Package render is the plugin-author-facing builder layer over the
// generated pluggableharness.render.v1 message types
// (pkg/render/proto/v1), the wire shape defined in
// docs/specifications/frontend/render-tree.md.
//
// # Emit -> Render -> Paint
//
// A plugin category (model, tool, context, memory — and their frontend/
// widget consumers) that wants to show something beyond plain text
// implements the Emit->Render->Paint pipeline: the plugin Emits an opaque
// payload at the point an event happens
// (docs/specifications/kernel-callbacks.md#emit), the kernel later calls
// that category's optional Render RPC to turn the stored payload into a
// display-agnostic pluggableharness.render.v1.RenderTree
// (docs/specifications/frontend/render-tree.md#rendertree), and the
// kernel's active frontend Paints that tree without ever knowing the
// payload's original shape. This package is for the middle stage: it
// gives a Render implementation a fluent way to build the RenderTree/
// RenderNode values that stage returns, instead of hand-assembling the
// generated types' nested oneof wrappers by hand.
//
// # What this package is not
//
// Per .claude/rules/go-layout.md's "exactly one Go representation of each
// wire message" rule, this package does not define a second, parallel Go
// type for RenderNode or RenderTree. Every builder function here returns
// the generated *renderv1.RenderNode or *renderv1.RenderTree directly —
// this is a set of pure constructor functions over the generated types,
// not a domain model that gets converted to them.
//
// # Node types
//
// One builder per node type
// (docs/specifications/frontend/render-tree.md#node-types): Text/
// TextStyled, Code, Diff (plus Hunk and the DiffLine* helpers), Table,
// Link, List, Group, Collapsible, SubSession, and Action. Group, List, and
// Collapsible are the three recursive node types — see
// docs/specifications/frontend/render-tree.md#rendertree ("Recursion
// happens via ListNode.items, GroupNode.children, and
// CollapsibleNode.children").
//
// # Schema versioning
//
// A plugin's Render implementation MUST branch on the RenderRequest's
// schema_version rather than sniffing the payload's shape, and MUST keep
// decoding every schema_version it has ever emitted
// (docs/specifications/frontend/render-tree.md#schema-versioning).
// VersionRegistry in version.go is a small dispatch helper that makes
// that the path of least resistance: register one VersionedRenderer per
// schema_version a plugin has ever shipped, then call Render with the
// schema_version threaded through from the RenderRequest.
package render
