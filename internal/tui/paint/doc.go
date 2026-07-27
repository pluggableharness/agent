// Package paint renders a RenderTree into styled terminal text.
//
// The painter is a pure function of (node, theme, options): it holds no
// terminal state, opens no files, and never consults the clock. That is what
// lets the entire node vocabulary — including the fallback behavior for node
// types this build does not recognize — be tested headlessly on every CI
// platform, Windows included.
//
// Two protocol obligations shape the implementation. First, a frontend MUST
// render every node type gracefully, including a variant added to the enum
// after the frontend shipped, rather than erroring or dropping content; the
// painter delegates that case to pkg/frontend.FallbackText rather than
// reimplementing the traversal. Second, a render failure on one node MUST NOT
// crash the frontend process, so the painter degrades a bad subtree to
// fallback text and keeps going. Both rules are stated in
// docs/specifications/frontend/render-tree.md and the error taxonomy in
// docs/specifications/frontend/frontend-protocol.md.
package paint
