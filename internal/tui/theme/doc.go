// Package theme owns the reference TUI shell's style tokens: the mapping from
// the protocol's TextStyle vocabulary
// (docs/specifications/frontend/render-tree.md) plus the shell's own chrome
// roles onto concrete Lip Gloss styles.
//
// The package is deliberately a token table rather than a styling engine. Every
// visual decision the shell makes resolves to one of the fields on Theme, so a
// future config-driven theme can be added by constructing a different Theme
// without touching the painter. Nothing here performs I/O or inspects the
// terminal; profile downsampling for 16-color and monochrome terminals is Lip
// Gloss's job, so tokens are authored once in truecolor.
package theme
