# internal/tui/ui

The shell's utility and component layer — the terminal equivalent of a utility-first CSS framework.

## Why it exists

Without a layer like this, every pane picks its own padding, its own border color, and its own idea of what "muted" means. The result is a collection of individually reasonable choices that does not look like one system. This package applies the three rules that make utility-first styling work:

- **Values come from a scale, never a literal.** Padding is `theme.Space1`..`Space4`; colors are `theme.Tokens` fields. A pane that wants more padding picks the next step; it does not invent `3`.
- **Utilities compose.** `Style` is a chainable builder where each method sets exactly one property, so a pane's appearance reads as a sentence where it is used rather than hiding in a named style somewhere else.
- **Components are compositions of utilities, not escapes from them.** `Panel` and `StatusLine` are built from the same builder any caller uses.

## What lives here

| Symbol | Role |
|---|---|
| `Style` | The chainable utility builder: `Fg`, `Bg`, `P`/`Px`/`Py`, `W`/`H`/`MaxW`, `Bold`, `Italic`, `Underline`, `Align` |
| `Panel` | A titled, bordered surface — content panes, header and footer alike. Returns exactly `Height` lines of exactly `Width` cells, with an optional `Caption` in the bottom border |
| `Badge` | A small filled label for status pills |
| `StatusLine` | A full-width row of labelled segments, one of which absorbs the slack |
| `Meter` | An inline fill bar, heavy against light stroke |
| `GradientMeter` | A fill bar whose color runs across a ramp along its length |
| `Fields` | A label/value list with values aligned into a column |
| `Fit` / `FitBlock` | Force a line or block to exact cell dimensions, ANSI-aware |
| `Clip` / `ClipLeft` | Truncate a line from either end; clip left to keep a path's tail |
| `Overlay` | Splice a block on top of a frame, preserving what surrounds it |
| `ExpandTabs` | Replace tabs with spaces before anything measures or wraps |

## Two cell-accuracy rules worth knowing

**Every component covers every cell it claims.** An uncovered cell shows the terminal's own background and breaks the illusion of a full-screen application, so `Panel` and `StatusLine` pad out to their full size rather than returning ragged lines.

**Tabs are expanded on the way in.** A tab measures as zero cells but a terminal advances to the next tab stop when it draws one, so unexpanded tabs paint wider than they measure — overflowing the pane and corrupting every row to the right. Producer content routinely contains tabs (Go source, diffs).

## Related

- `internal/tui/theme` — the token set and scales this package consumes.
- [`docs/first-party/frontends/tui.md`](../../../docs/first-party/frontends/tui.md) — the design system this implements.
