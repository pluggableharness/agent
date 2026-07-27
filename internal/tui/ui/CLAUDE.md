# internal/tui/ui — agent notes

## No literals at call sites

The point of this package is that padding comes from `theme.Space*` and colors from `theme.Tokens`. A `lipgloss.Color("#7aa2f7")` or a bare `Px(3)` anywhere in `shell` or `paint` defeats it. If a needed value is missing, add it to the scale in `theme` rather than inlining it here.

## Components must return exact dimensions

`Panel.Render` returns exactly `Height` lines of exactly `Width` cells, and `StatusLine.Render` exactly one line of `Width` cells. Callers stack them without measuring, so a component that returns a ragged block silently shifts everything below it. Tests assert this across a range of sizes including degenerate ones.

The subtle case: `strings.Split("", "\n")` returns one empty element, not zero. A panel with no interior (`Height == 2`) must skip its body loop outright rather than trusting an empty `FitBlock` to produce no rows.

## `ExpandTabs` is a correctness fix, not formatting

`lipgloss.Width("\t")` is 0 but a terminal advances to the next tab stop. Leaving a tab in content makes the painted row wider than the measured one, which overflows the pane and corrupts every row to its right. Expansion happens in `Fit` and at every text-bearing leaf in `paint`. Do not "simplify" it away.

## Status lines drop from the right, and never advertise empty fields

`StatusLine` lays segments left to right and drops from the right when they will not fit, so segment *order is a priority ranking*. A segment with no value and no `Fill` is omitted entirely — a status bar showing fields it has no data for teaches the operator to stop reading it.

Exactly one segment per line should set `Fill`; it receives whatever width is left after every fixed segment is placed, which is what makes the line reflow on resize. `MinWidth` must account for the segment's *whole* rendering — label and trailing text included — not just its bar, or the fill callback gets a width too small to use. A filling segment should also cap itself: handed the slack of a very wide terminal, an uncapped meter becomes a rule with a number marooned at the far end.

`Right` segments are fitted **after** the left group and only survive if the left group fits beside them whole. Reserving the right group's width first lets a secondary field evict a primary one, which inverts the ranking — there is a test named for exactly this.

`Meter` uses the same heavy-against-light stroke as everything else that shows fill. Do not make the boundary color-only: it is the channel that survives a monochrome terminal and does not depend on telling green from red.

## `Overlay` exists because Lip Gloss cannot do this

`Canvas.Compose` draws every layer at full canvas bounds and `Layer.Draw` ignores its own X/Y, so composing a pane over a frame erases the frame instead of sitting on it. Row-wise ANSI splicing is the working approach. `Overlay` also clips a too-wide block rather than widening the row, because an over-wide row wraps and shifts the whole frame.

## This package is pure domain

No `log/slog`, no `internal/telemetry`, no I/O — the pure-domain exemption in `.claude/rules/logging-telemetry.md` applies. It takes tokens and strings and returns strings.
