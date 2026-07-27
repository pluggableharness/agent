// Package ui is the shell's utility and component layer — the terminal
// equivalent of a utility-first CSS framework.
//
// It exists for the reason Tailwind exists: without it, every pane picks its
// own padding, its own border color, and its own idea of what "muted" means,
// and the interface drifts into a collection of individually reasonable
// choices that do not look like one system. The rules here are the same ones
// that make that approach work:
//
//   - Values come from a scale, never from a literal. Padding is
//     theme.Space1..Space4; colors are theme.Tokens fields. A call site that
//     wants "a bit more padding" picks the next step, it does not invent 3.
//   - Utilities compose. Style is a chainable builder where each method sets
//     exactly one property, so a pane's appearance reads as a sentence at the
//     point of use rather than hiding in a named style elsewhere.
//   - Components are compositions of utilities, not escapes from them.
//     Panel and StatusLine are built from the same Style builder any caller uses.
//
// Everything here is pure: it takes tokens and strings and returns strings.
// No terminal, no I/O, no global state.
package ui
