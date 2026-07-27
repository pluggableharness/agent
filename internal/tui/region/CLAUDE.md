# internal/tui/region — agent notes

## Never iterate a map to produce paint order

Regions are held in a fixed-length array indexed by the enum, and `Contents` sorts a copied slice. Introducing a `map[Region][]Placement` and ranging over it would reintroduce exactly the nondeterminism `.claude/rules/determinism.md` forbids, and the failure mode is a frame that reorders between runs rather than a test failure.

## `replace` is producer-scoped, not region-scoped

`Place` with `replace: true` deletes only that producer's prior entries. A change that clears the whole region would break widget coexistence — two widgets sharing the sidebar would evict each other. There is a test named for this; if it fails, the semantics regressed.

## Unset priority is not zero

`Placement.Ranked` carries whether `PlacedContent.priority` was present. A zero priority is a *ranked* placement that sorts first; an absent priority sorts last. Collapsing these into `int32(0)` silently reorders every unranked producer to the front.

## `Store` is deliberately not safe for concurrent use

The shell owns one per session and mutates it only from the Bubble Tea update goroutine. That single-goroutine ownership is what makes the absence of locking correct. If something ever needs to write from another goroutine, route it through a `tea.Msg` rather than adding a mutex here.

## This package is pure domain

No `log/slog`, no `internal/telemetry`, no I/O — the pure-domain exemption in `.claude/rules/logging-telemetry.md` applies. It is 100%-covered; keep it there.
