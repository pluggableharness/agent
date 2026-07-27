# internal/tui/region

The reference TUI shell's content store: the per-region set of placements every producer has contributed, and the ordering rule that decides what paints first.

## What lives here

- `Store` — one session's placed content, indexed by region.
- `Placement` — one producer's contribution: its tree, its priority (and whether priority was set at all), and the kernel sequence it arrived with.
- `Stream` — an in-progress streamed text block, correlated by `target_id`.
- `Normalize` — folds `REGION_UNSPECIFIED` and any unrecognized region value onto `REGION_MAIN_CHAT`.

## The model

A region is **not** a single-writer slot. The protocol's default is coexistence: several producers may target one region, and the frontend arbitrates by priority rather than evicting. `PlacedContent.replace` therefore supersedes only the placements of the producer that sent it, never another producer's.

Ordering is `(ranked, priority, sequence)` ascending:

- A placement with priority set sorts ahead of every placement without one.
- Unset priority means "declaration order", which is `sequence` order.
- `sequence` is the only tiebreak.

## Determinism

Wall clock is never an input to ordering, and regions live in a fixed-length array rather than a map, so paint order cannot vary with Go's map iteration. Both are required by [`.claude/rules/determinism.md`](../../../.claude/rules/determinism.md). The consequence that matters: two shells replaying one session compose identical frames.

Rendered output is derived state — recomputed from this store, never persisted, never cached to disk.

## Related

- [`docs/specifications/frontend/render-tree.md`](../../../docs/specifications/frontend/render-tree.md) — the `Region`/`PlacedContent` vocabulary.
- [`docs/first-party/frontends/tui.md`](../../../docs/first-party/frontends/tui.md) — how the shell lays these regions out.
