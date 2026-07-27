# internal/tui/theme

The shell's design tokens: colors, the spacing scale, and the border presets. This is the bottom of the UI dependency chain — it imports nothing from the rest of the shell, and everything else consumes it.

## Two color layers

The split is the same one any design system makes, and it is the reason a theme can be swapped without touching a line of painting code.

- **`Palette`** — the raw values, named for what they are: a ten-step neutral ramp from app background to strongest text, plus six intent hues. Nothing outside this package references a `Palette` field.
- **`Tokens`** — the semantic layer, naming every color by its *role*. This is the only color vocabulary the rest of the shell sees.

`tokens()` is the single function that decides which ramp step means "panel" or "muted text", so both built-in themes stay structurally identical and only their colors differ.

### The token set

| Group | Tokens | Why three |
|---|---|---|
| Surfaces | `Background`, `BackgroundPanel`, `BackgroundElement` | The application surface, plus two fills reserved for self-contained controls (see below) |
| Text | `Text`, `TextMuted`, `TextSubtle`, `OnAccent` | Primary, secondary, and de-emphasized, plus text on a filled accent |
| Lines | `BorderSubtle`, `Border`, `BorderActive`, `Divider` | Quiet edges, ordinary pane edges, focus — and inline separators, which need more contrast than a border |
| Intents | `Primary`, `Accent`, `Success`, `Warning`, `Danger`, `Info` | |
| Diff | `DiffAdded`, `DiffRemoved`, `DiffContext`, `DiffHunkHeader` | |

## Neutrals are actually neutral

The ramp keeps only a hint of cool. An earlier version carried a third of its value in blue — `#4a5570` is periwinkle, not grey — and at low luminance that cast is the first thing the eye notices, so borders and separators stopped reading as quiet structure and started reading as dark blue lines. `TestNeutralsAreNearlyGrey` holds every neutral to a channel spread of 24 or less.

That test measures **absolute channel spread, not HSV saturation**, and the distinction matters: near black a six-point spread is a quarter of the maximum channel yet completely imperceptible, so saturation flags colors that look perfectly neutral while catching nothing that matters.

## A divider is not a border

`Divider` exists separately from `Border` because the two have different jobs. A border frames a region and reads as structure even when dim; a lone `│` between two fields sits among text and needs more contrast to register at all. Sharing one token left the separators effectively invisible — 1.2:1 against the background.

Current contrast against the background: divider 2.8:1, border 1.9:1, subtle text 4.8:1, text 12.6:1. The divider is visible as a line while staying subordinate to the dimmest text, which is what `TestDividerIsVisibleAgainstTheBackground` asserts from both ends.

## Tone — naming a role, not a color

`Tone` is the selector configuration uses when it wants to say "this thing is amber" without naming a hex value: `color = "warning"` resolves through `ToneByName` and then `Theme.Tone`, so a custom theme recolors everything that referenced it. The agent roster in `internal/tui/shell` is the first consumer.

## Ramps and blending

`Ramp` is an ordered list of tone *roles* a gauge interpolates across — `{ToneSuccess, ToneWarning, ToneDanger}` by default, expressible in config as `["success", "warning", "danger"]`. `Ramp.At` resolves a position from 0 to 1, interpolating between the two stops it falls between.

`Mix` blends two colors, and it is how the theme derives a shade instead of hardcoding one: `Theme.Muted` is a token mixed toward `Background`, and an intermediate ramp hue is one stop mixed toward the next. Both stay expressed in terms of tokens, so a custom theme recolors them too. This is the sanctioned way to produce a color that is not itself a token — never a literal at a call site.

## The non-color half

Spacing is a scale (`Space0`..`Space4`) plus `Gutter`, the breathing room between the screen edge and the outermost pane. Borders are presets (`BorderNone`, `BorderNormal`, `BorderRounded`, `BorderThick`). Call sites pick a step; they do not invent a number.

## One surface

What makes the UI read as paneled is borders, titles, and spacing — not competing background colors. The shell paints a single application surface, set once on the Bubble Tea `View`.

That is a correctness rule before a stylistic one: Lip Gloss ends every styled run with a full SGR reset, which clears whatever background its container set, so a broadly-filled region only stays filled until the first styled run inside it ends. `BackgroundPanel` and `BackgroundElement` therefore exist for genuinely filled, self-contained controls — a badge, a focused button — not for regions.

## Derived styles

`Theme` also carries the Lip Gloss styles the painter uses, derived from the tokens in `New` so the two can never disagree. **Content styles set a foreground and never a background**, for the reason above; `ActionFocused` is the one deliberate exception.

`TextStyle` maps the protocol's `TextStyle` enum onto those styles, including the two cases the spec distinguishes (unset versus explicit `NORMAL`) and the graceful fallback for a value added to the enum after this build shipped.

## Related

- `internal/tui/ui` — the utility and component layer built on these tokens.
- [`docs/first-party/frontends/tui.md`](../../../docs/first-party/frontends/tui.md) — the design system in prose.
