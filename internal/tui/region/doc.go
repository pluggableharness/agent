// Package region owns the reference TUI shell's content store: the per-region
// set of placements contributed by every producer, and the ordering rule that
// decides what paints first.
//
// The store models the protocol's coexistence default
// (docs/specifications/frontend/render-tree.md): a region is not a
// single-writer slot, so several producers may target one region and the
// frontend arbitrates by priority rather than evicting. PlacedContent.replace
// supersedes only the placements of the producer that sent it, never another
// producer's.
//
// Ordering is (ranked, priority, sequence) ascending, with unset priority
// sorting after every ranked entry and sequence as the sole tiebreak. Wall
// clock is never an input and regions are held in a fixed-length array rather
// than a map, so paint order cannot vary with Go's map iteration — both
// required by .claude/rules/determinism.md. The practical consequence is that
// two shells replaying one session compose identical frames.
//
// Nothing in this package performs I/O or touches a terminal, so the whole
// ordering contract is testable headlessly.
package region
