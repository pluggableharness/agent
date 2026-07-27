# internal/tui/theme — agent notes

## Palette and Tokens are two layers on purpose

`Palette` is raw values named for what they are; `Tokens` names them by role. Only `tokens()` bridges the two. Do not let a `Palette` field leak outside this package, and do not add a color to `Tokens` by inlining a hex literal — add the value to `Palette` and map it.

Both built-in themes route through `New`, so a new derived style is added once and both get it. A theme constructed by hand somewhere else will drift.

## Unset style is not the same as `TEXT_STYLE_NORMAL`

`TextNode.style` is optional. A nil pointer means "frontend's own default" (`Theme.Default`); an explicit `TEXT_STYLE_NORMAL` is a producer deliberately asking for plain styling (`Theme.Normal`). The spec calls these out as distinct states, so they stay separate fields even though both built-ins render them identically. A test asserts they remain independently overridable.

## Unknown enum values must fall back, never panic or drop

`TextStyle` has a `default:` branch returning `Theme.Default`, for a value added to the enum after this build shipped. The protocol requires rendering text a frontend has no visual treatment for rather than dropping it.

## Content styles must never set a background

Lip Gloss ends every styled run with a full SGR reset, which clears any background its container had set. A text style carrying its own background paints a band that stops where the text stops and drops the rest of the row onto the terminal background — a visible patchwork across every pane. This was a real bug, not a hypothetical.

Surfaces are painted by the container that owns them; text contributes color only. `TestContentStylesSetNoBackground` enforces this across both themes — if it fails, do not "fix" the test.

The one deliberate exception is `ActionFocused`: a filled control is a single self-contained run whose reset lands at its own end, so it cannot bleed. `ui.Badge` is filled for the same reason.

Note that `GetBackground()` on an unset style returns a zero color value, not nil — compare against `lipgloss.NewStyle().GetBackground()`.

## Neutrals must stay grey, and dividers must stay visible

Three tests guard the palette and they are worth understanding before changing a hex value:

- `TestNeutralsAreNearlyGrey` — every neutral stays within a channel spread of 24. It measures absolute spread rather than HSV saturation because saturation is meaningless near black: a six-point spread at `#101216` is a quarter of the maximum channel and completely invisible.
- `TestDividerIsVisibleAgainstTheBackground` — asserts from both ends: the divider must be legible as a line (>2:1) *and* stay subordinate to the dimmest text.
- `TestTextContrastIsLegible` — text clears 7:1, subtle text 3:1.

`Divider` is deliberately a separate token from `Border`. A border frames a region and reads as structure when dim; an inline separator glyph needs more contrast. Do not collapse them.

## This package is pure domain

No `log/slog`, no `internal/telemetry`, no I/O — the pure-domain exemption in `.claude/rules/logging-telemetry.md` applies. It is 100%-covered; keep it there.
