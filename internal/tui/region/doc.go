// Package region is the reference TUI's local layout store: which pane holds
// which RenderTree, stream buffers for live token deltas, and producer-scoped
// replace semantics.
//
// Region values here are local UI chrome (main chat, sidebar chrome, top bar,
// …) — not a wire enum. The protocol retired placement regions; transcript
// content is always main chat, and other chrome is driven by SessionState and
// MetadataBlock surfaces. Priority and sequence still arbitrate coexistence
// when multiple producers contribute to the same local pane.
package region
