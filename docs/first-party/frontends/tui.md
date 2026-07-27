# The reference TUI shell

The first-party frontend provider: a full-screen terminal shell that paints [`RenderTree`](../../specifications/frontend/render-tree.md) content into the six-region vocabulary and turns operator input into [`ClientEvent`](../../specifications/frontend/frontend-protocol.md#client-events)s.

> [!IMPORTANT]
> This document is descriptive, not normative. The protocol contract — what any frontend MUST implement — is [`frontend-protocol.md`](../../specifications/frontend/frontend-protocol.md) and [`render-tree.md`](../../specifications/frontend/render-tree.md). The layout, focus model, and keymap described here are *this* shell's choices, one conforming instantiation of the abstract region vocabulary, exactly as [`examples.md#the-reference-tui`](../../specifications/frontend/examples.md#the-reference-tui) frames it. A second frontend is free to resolve every one of them differently.

## Why a shell framework exists at all

The protocol defines *what* content arrives and *where* it is placed, and deliberately stops there. It defines no focus model, no keybinding schema, no terminal-resize semantics, and no scrollback behavior — [`conformance.md`](../../specifications/frontend/conformance.md) is explicit that the region vocabulary was designed against one reference implementation plus a thought experiment. Those four gaps are not oversights to be pushed back into the protocol; they are display concerns that only a concrete surface can answer. This document answers them for the terminal, so that widget authors and future integrations have stable, named places to attach to.

The ordering matters: the shell's regions, focus ring, and keymap layers must exist before widget plugins are written, because a widget contributing to `sidebar` needs to know whether `sidebar` can hold focus, whether its `ActionNode`s are reachable from the keyboard, and what happens to it in a narrow terminal.

## Process shape — who owns the terminal

A frontend is a `hashicorp/go-plugin` subprocess, per [`plugin-runtime`](../../specifications/frontend/README.md#transport--lifecycle): the kernel is the gRPC *client*, the shell is the *server*, and the kernel calls `Attach` on it. That inverts the usual intuition — the process painting the screen is the child, not the parent — and it creates the single most load-bearing constraint in this design.

**go-plugin owns the subprocess's standard streams.** The handshake line is written to the plugin's `stdout`, and after handshake the host pipes the plugin's `stdout`/`stderr` into its own logger. A TUI that renders to `stdout` therefore corrupts the handshake, and one that reads `stdin` competes with the plugin transport.

The shell resolves this by never touching the standard streams for display: it opens the controlling terminal directly and hands that file to Bubble Tea as both input and output.

```go
tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
// ...
prog := tea.NewProgram(model,
    tea.WithInput(tty),
    tea.WithOutput(tty),
    tea.WithContext(ctx),
)
```

Note there is no `tea.WithAltScreen()`: in Bubble Tea v2 the alt screen is a property of the `View` the model returns, not a program option.

Consequences that follow from this and are not negotiable:

- `stdout` and `stderr` remain go-plugin's. Nothing in the shell may `fmt.Println`. Diagnostics go through `slog` (which the host collects from `stderr` as structured plugin logs) or the kernel's `Log` callback — never to the painted surface.
- The shell requires a controlling terminal. Launched without one (CI, a daemonized kernel, a piped session), opening `/dev/tty` fails and the shell MUST degrade to a non-painting mode rather than crash the kernel that spawned it — it reports `FRONTEND_ERROR_CATEGORY_UNKNOWN` at `Configure` time and declines to attach.
- Windows uses `CONIN$`/`CONOUT$` in place of `/dev/tty`; this is the one genuinely platform-forked file in the tree, isolated behind a build-tagged `openTTY()` so nothing else needs to care.

### Owning the whole screen

Painting to the right file is only half of taking over a terminal. The shell also claims the surface itself, through properties of the `View` it returns each frame:

| Property | Effect |
|---|---|
| `AltScreen` | Full-screen takeover, leaving the user's scrollback untouched on exit |
| `BackgroundColor` / `ForegroundColor` | Sets the terminal's own default colors to the theme's, so nothing shows through as "not part of the app" |
| `WindowTitle` | Names the session in the terminal's title bar |
| `Cursor` | Places the **real** terminal cursor in the composer, colored and blinking, and hides it whenever the composer does not own the keyboard |
| `MouseMode` | Claims the mouse, so the wheel scrolls the transcript instead of the terminal's own scrollback behind the alt screen |

Claiming the mouse is what closes the last gap in the takeover. Without it the wheel still appears to work — but it is scrolling the terminal's buffer *behind* the alt screen, not the conversation. The cost is that drag-selection now needs the terminal's own override, which is shift+drag in most of them.

The cursor point is worth stating plainly: the composer draws no caret glyph of its own. It reports its buffer position, the shell maps that onto absolute screen coordinates, and Bubble Tea places the actual cursor there — so it behaves exactly like the cursor in every other terminal application rather than being a drawn approximation of one.

The last piece is the invariant that ties the frame together: **every frame is exactly `Height` rows of exactly `Width` cells**. An uncovered cell shows the terminal's own background and breaks the illusion; a row wider than the terminal wraps and shifts everything beneath it. This is asserted by test across a range of sizes down to 20×6, because it is otherwise only visible by eye.

## The design system

Before the layout, the vocabulary it is built from. Without a constrained token set, every pane picks its own padding, its own border color, and its own idea of what "muted" means, and the result is a collection of individually reasonable choices that does not look like one system. The shell therefore borrows the structure of a utility-first CSS framework, in two layers.

**Layer one: raw palette.** `theme.Palette` is the literal color values, named for what they are — a ten-step neutral ramp from app background to strongest text, plus six intent hues. Nothing outside the theme package references it.

**Layer two: semantic tokens.** `theme.Tokens` names every color by its *role*, and it is the only color vocabulary the rest of the shell sees:

| Group | Tokens |
|---|---|
| Surfaces | `Background` (the application surface), `BackgroundPanel`, `BackgroundElement` |
| Text | `Text`, `TextMuted`, `TextSubtle`, `OnAccent` |
| Borders | `BorderSubtle`, `Border`, `BorderActive` |
| Intents | `Primary`, `Accent`, `Success`, `Warning`, `Danger`, `Info` |
| Diff | `DiffAdded`, `DiffRemoved`, `DiffContext`, `DiffHunkHeader` |

The non-color half matters just as much: spacing is a scale (`Space0`..`Space4`, plus `Gutter`), and borders are a preset set. A pane that wants more padding picks the next step; it does not invent `3`.

### One surface, and why

**What makes the UI read as paneled is borders, titles, and spacing — not competing background colors.** The shell paints a single application surface, set once via the Bubble Tea `View`, and content styles carry a foreground and nothing else.

This is a correctness rule before it is a taste one. Lip Gloss terminates every styled run with a full SGR reset, and a reset inside a container clears the background that container set. A text style carrying its own background therefore paints a band that stops exactly where the text stops, dropping the rest of the row back to the terminal's background:

```
ESC[48;2;21;25;34m                          <- container opens its background
  ESC[38;2;158;206;106;…m styled ESC[m      <- inner run ends with a FULL reset
  "   PADDING"                              <- so this lands on the terminal background
ESC[m
```

Filling broad regions is therefore unreliable in a way no amount of care at the call site fixes, and the visible result is a patchwork of bands at differing widths across every pane. `BackgroundPanel` and `BackgroundElement` remain in the token set, but they are for genuinely filled, self-contained controls — a status badge, a focused button — where the run opens and closes its own fill and cannot bleed. A test asserts that no content style sets a background.

**Utilities and components.** `internal/tui/ui` is the terminal analogue of utility classes: `Style` is a chainable builder where each method sets exactly one property, so a pane's appearance reads as a sentence where it is used — `ui.New().Bg(t.C.BackgroundPanel).Fg(t.C.TextMuted).Px(theme.Space1)`. `Panel`, `StatusLine`, and `Badge` are compositions of those utilities, not escapes from them.

Two cell-accuracy rules fall out of this and are load-bearing rather than cosmetic. Every component covers **every cell it claims**, because an uncovered cell shows the terminal's own background and breaks the illusion of a full-screen application. And **tabs are expanded to spaces on the way in**: a tab measures as zero cells but a terminal advances to the next tab stop when it draws one, so an unexpanded tab in producer content paints wider than it measures, overflows its pane, and corrupts every row to its right.

## Screen layout

Six abstract regions map onto terminal geometry as follows. This is the wide layout, at or above 100 columns:

```
 pluggableharness  session-01ABC                     claude-opus-5  ready    <- top_bar
 ╭─ conversation ────────────────────────────────╮ ╭─ git ──────────────╮
 │ Reference TUI shell                           │ │ branch: main       │
 │ ▾ read_file(internal/tui/shell/model.go)      │ │ 3 modified         │    <- sidebar:
 │   func (m *Model) View() tea.View {           │ │ [ Review diff ]    │       one panel
 │ @@ -12,3 +12,4 @@                             │ ╰────────────────────╯       per widget
 │ -    return Layout{}                          │ ╭─ context ──────────╮
 │ +    l := Layout{Width: width}                │ │ 42% of budget      │
 │ [ Compact context ]                           │ ╰────────────────────╯
 ╰───────────────────────────────────────────────╯
 ╭─ message ───────────────────────────────────────────────────────────╮     <- input_bar
 │ › ask anything, or / for commands                                   │
 ╰─────────────────────────────────────────────────────────────────────╯
 enter send · alt+enter newline · tab focus · ctrl+c interrupt   input       <- hotkey_hints

overlay: a centered pane composited over the frame, on the element surface
         with an active border — never inline, and never blanking what is
         behind it. See "Overlay is modal" below.
```

Every region is a panel or a bar, and each carries a title in its top border rather than on a row of its own — that buys back a line of content per pane, which is what makes a stack of small side panels affordable.

A panel may also carry a **caption** in its bottom border, against the right corner. It is for the second thing a panel often has to say about itself: not what it is, but how it is currently configured. Splitting the two across the diagonal gives each a corner to own, so neither has to be found inside a long run of text. The caption is never clipped — an abbreviated model name names no model — so it renders whole or drops out entirely on a narrow terminal.

**The sidebar is one panel per contributing producer, not one column of concatenated text.** This is the affordance widget authors design against: a titled panel makes it obvious which plugin contributed what, and lets a single widget be focused and act on without its neighbors coming along.

Layout is solved top-down in a fixed order so it stays deterministic: `top_bar` takes 1 row, the composer takes its measured content height (clamped to 6) plus its border, `hotkey_hints` takes 1 row, and the body receives every row left over. Horizontally, a one-cell gutter runs down each screen edge, `sidebar` is measured next (clamped to `[26, 38]` columns and never more than 40% of the terminal), and the main panel takes the remainder. `Layout` exposes only *outer* boxes; interior sizes come from its `Inner` helpers so no caller open-codes the border-and-padding arithmetic.

### Responsive degradation

The protocol's graceful-fallback rule ([`render-tree.md:136`](../../specifications/frontend/render-tree.md)) says placement is a hint the frontend may reinterpret, never a mandate. This shell reinterprets in a fixed, documented order as space runs out, so behavior is predictable rather than emergent:

| Constraint | Response |
|---|---|
| width `< 100` | `sidebar` leaves the layout and becomes a toggleable pane (`ctrl+b`). Its content is not dropped — it is reachable on demand. |
| width `< 64` | `sidebar` is unavailable entirely; its content folds into `main_chat`, per the spec's "fold it into another region" allowance. |
| height `< 12` | `hotkey_hints` is dropped first (it is a reminder, not content). |
| height `< 10` | `top_bar` is dropped; `main_chat` and `input_bar` are the last two regions standing. |
| any | `input_bar` and `main_chat` are never dropped. A shell that cannot show input or output is not a shell. |

Dropping a region is logged once per transition at `debug`, never per frame, and never surfaces as `region_unsupported` to the kernel — the region is supported, the terminal is merely small, and the protocol's own open question notes those two conditions currently share one error category. The shell therefore reports neither and just adapts.

## The content store

Regions do not hold a tree; they hold an ordered set of *placements*, because multiple producers may target one region and coexistence — not exclusivity — is the documented default.

```go
type producerKey struct{ category, name string } // server-derived identity
type placement struct {
    producer producerKey
    priority int32
    ranked   bool   // false == "unset", which sorts after every ranked entry
    seq      uint64 // kernel sequence; the sole tiebreak
    tree     *renderv1.RenderTree
}
```

Two placement behaviors, selected by `PlacedContent.replace`:

- **Append** (`main_chat`'s default): the placement is added to the region's transcript and never rewrites prior entries. This is the conversation flow.
- **Replace**: the placement supersedes *that producer's* prior entry in that region, leaving other producers' entries untouched. This is how a status widget updates without evicting its neighbors.

Ordering is `(ranked, priority, seq)` ascending — unset priority sorts last, and `seq` is the only tiebreak. **Wall-clock time is never an input to ordering, and the store is never iterated as a map**, both of which the repository's determinism rules require. The practical effect is that two shells replaying the same session paint identical frames.

Rendered output is derived state: it is recomputed from the store, never persisted, never cached to disk.

### Streaming text

`stream_delta` is the fast path and never round-trips through `Render`. The store keeps a live buffer keyed by `target_id`; consecutive deltas append to it and it paints as ordinary `main_chat` content. When the corresponding finished `render` arrives, it *replaces* the buffer rather than appending beside it — otherwise every streamed message would appear twice. Backfill never contains deltas, so the replay path exercises none of this.

## Focus — the first gap the protocol leaves open

Focus is a shell concept; the protocol has no notion of it. The model here is deliberately small.

**Focus targets are regions, not nodes.** The ring is `input_bar → main_chat → sidebar → input_bar`, cycled with `tab` / `shift+tab`. `input_bar` holds focus at startup, because typing is the overwhelmingly common intent and a shell that requires a keystroke before it accepts text is hostile.

**Within a focused region, an action cursor selects among that region's `ActionNode`s.** The protocol requires every `ActionNode` be interactive and that activation dispatch `action_trigger` with `tool_name`/`args`/`provider` unchanged. This shell satisfies that by assigning each actionable node a stable index in paint order; `↑`/`↓` move the cursor within the focused region and `enter` activates. Regions with no actionable nodes are skipped by the focus ring entirely, so `tab` never lands somewhere inert.

**Overlay is modal and exclusive.** When overlay content exists, it takes focus unconditionally, the focus ring is suspended, and the previously focused region is restored when the overlay clears. This is what makes a plan-approval prompt un-missable, and it is the shell's answer to the protocol's requirement that overlay content be *visually distinct* from ambient content — here it is also behaviorally distinct.

## Agents

The shell carries an active **agent** — the profile a turn will run under — and `shift+tab` cycles it, which is the convention operators arrive with from other harnesses. The demo roster is three entries:

| Agent | Tone | Meaning |
|---|---|---|
| `Code` | `primary` | Build and edit — the ordinary mode |
| `Plan` | `warning` | Read-only; nothing gets applied |
| `Chat` | `info` | Conversation only |

**An agent is data, not code.** `Agent` is a name plus a *tone*, and the roster is expected to come from configuration — an `agent_profile` block naming a color — so nothing in the shell may assume the three built-ins exist. `WithAgents` replaces the roster wholesale; an empty one keeps the defaults, because the shell always needs something to display as active.

**The color is a role, not a value.** Config says `color = "warning"`, `theme.ToneByName` resolves it, and the active theme decides what amber actually is. That is what keeps a custom theme able to recolor agents along with everything else, and it is why agents never name a hex value.

The active agent is visible in three places, so the current mode is never a guess: a filled badge in the top bar, the composer's title, and the composer's border and prompt caret, both drawn in the agent's tone. The agent's tone stops at the title — the model caption in the opposite corner stays neutral, because the model is not what the agent switch recolors. Color alone is never the only signal — the name appears twice in text.

> [!NOTE]
> **This selection is local state, and that is a protocol gap rather than a design choice.** The frontend protocol's `ClientEvent` set has no agent-profile variant, and a session's profile is fixed when the session is created — so there is currently no way to tell the kernel that the operator switched. The shell emits an `AgentSelected` action for the bridge to interpret, most plausibly as the profile for the *next* session or as a direct-invoke slash command. Resolving it properly means either a new `ClientEvent` variant or an explicit statement that agent switching applies at session-creation time only. That belongs in [`frontend-protocol.md`](../../specifications/frontend/frontend-protocol.md), not here.

Cycling is a global binding, so it works from any focused region; it does not disturb focus, and `shift+tab` no longer moves focus backward. The focus ring is at most three entries, so cycling forward with `tab` reaches everything and a backward binding was not worth the key. `KeyMap.PrevFocus` keeps its field, unbound, for a future configuration.

## Where session data lives

Session data is placed by **how fast it changes**, not by what kind of thing it is. That single rule decides the whole layout:

| Volatility | Data | Home |
|---|---|---|
| Where the work is | directory, repository | Header box |
| Session identity | session, run state | Header box, right |
| Who you are talking to | agent | Composer title |
| What is behind it | model, thinking, effort | Composer caption, bottom right |
| Detail, consulted occasionally | token split, lines read and changed | Sidebar — `usage` panel |
| **Volatile, changes every turn** | context, cache rate, cost, elapsed | One line under the composer |

The reasoning is about attention rather than tidiness. Space beside the input box is the most-looked-at real estate on the screen, so it goes to the things that actually move. A field that is identical on turn one and turn fifty has not earned a place there — it belongs in the periphery, which is exactly where the empty space was.

Two placements deserve their reasons stated. **The agent and the model both live on the composer, but in opposite corners.** Both belong beside the input — switching agent is what changes the model behind it, so separating them across the screen would make one change appear in two distant places. But they answer different questions and move at different rates: the agent is who you are talking to and changes on a keystroke, while the model is what is behind it and changes rarely. Run together in one title they became a four-part string in which neither was findable, and the agent's color bled onto settings it does not own. The agent takes the title, where the eye already goes for a panel's name; the model takes the caption, quiet until looked for. **Version-control detail is absent from shell chrome entirely:** a git widget already contributes branch and PR as ordinary sidebar content, and carrying them in the status bar too would give the operator two sources for one truth. Only the directory and repository — which the shell is told at startup and which never change — sit in the top bar.

```
 ╭─ pluggableharness ───────────────────────────────────────────────────────────────────╮
 │  ~/code/aiagent  │  pluggableharness/agent          session-01DEMO  [ready]          │
 ╰──────────────────────────────────────────────────────────────────────────────────────╯
 ╭─ conversation ──────────────────────────────────╮ ╭─ usage ───────────────────────────╮
 │                                                 │ │ out     9.1k                      │
 │                    (empty space lives up here)  │ │ in      165.2k                    │
 │                                                 │ │ lines   4.8k read +612 -148       │
 │ Reference TUI shell                             │ ╰───────────────────────────────────╯
 │ ▾ read_file(internal/tui/shell/model.go)        │ ╭─ git ─────────────────────────────╮
 │ @@ -12,3 +12,4 @@                               │ │ feat/tui-shell                    │
 │ -    return Layout{}                            │ │ 3 modified                        │
 │ +    l := Layout{Width: width}                  │ │ pr #11                            │
 │ [ Compact context ]                             │ │ [ Review diff ]                   │
 │ Streaming text arrives token by token.          │ ╰───────────────────────────────────╯
 ╰─────────────────────────────────────────────────╯
 ╭─ Code  ·  claude-opus-5  ·  extended  ·  high ───────────────────────────────────────╮
 │ › ask anything, or / for commands                                                    │
 ╰──────────────────────────────────────────────────────────────────────────────────────╯
 context ━━━━━━━━──────────── 51.2k / 200k  26%   │   cache 89%   │   cost $0.42   │   elapsed 22m00s
 ╭─ keys ───────────────────────────────────────────────────────────────────────────────╮
 │  enter send · shift+enter newline · shift+tab agent · tab focus            input     │
 ╰──────────────────────────────────────────────────────────────────────────────────────╯
```

### Header and footer are boxes

They are bordered panels, in the same visual language as everything between them, each carrying a title — the product name above, `keys` below.

A background tint was tried first and does not work. At the contrast levels a dark theme lives at, a tinted row reads as a slightly-off *content* row rather than as a frame; the eye needs an edge, not a shade. A box gives it one for the cost of two rows. Those rows are real, so the header and footer are dropped outright on a short terminal rather than degrading to unboxed lines — one visual language is worth more than one extra row of transcript.

The status line deliberately stays **unboxed** between the composer and the footer. It is what the two boxes are separating; boxing it too would leave three stacked frames and nothing to separate.

### The transcript grows upward

Content shorter than the viewport is pushed to the **bottom** of its panel, not left at the top. Every chat interface works this way, and the reason is the same here: the newest message belongs next to where the operator is typing. Top-anchoring instead strands the last message a screen away from the input and puts the empty space *between* the two things you are looking at — which is the worst possible place for it. Anchored to the bottom, the void sits above the conversation where it costs nothing.

### The sidebar is the session dashboard

Panels stack from the top: the shell's own `workspace` and `usage` first, then whatever widgets have contributed. They are rendered identically on purpose — a widget author looking at the sidebar should see one visual language, not shell chrome sitting apart from plugin content.

A panel with no data is not rendered at all. An empty titled box is worse than no box.

### The status line

One row, and only what changes during a turn. **The context meter is the whole left side and grows into whatever the right group leaves** — cache, cost, and elapsed are pinned right, and the meter absorbs everything between. Context is alone on the left for a reason: a line drops left segments right-to-left, so anything beside it would survive at its expense, and context is the field worth keeping longest.

The absolute figures ride immediately after the bar (`51.2k / 200k  26%`) rather than at the far edge. That placement is what lets the bar grow without the earlier failure mode, where a long meter ended in a percentage marooned halfway across the screen.

**The bar is a gradient, not a single color.** It runs green through amber to red along its length, with the consumed run in full color and the remainder in a muted blend of the same gradient. A uniformly-amber bar tells you the current state; a gradient shows you the whole scale and where on it you sit. It remains a heavy stroke against a light one, so the measurement still reads on a monochrome terminal and never depends on telling green from red.

The blend happens in **Oklab**, not in sRGB. Interpolating sRGB channels is the obvious implementation and it looks wrong: green to red passes through a muddy olive, the midpoint is darker than either end, and the result bands visibly because equal numeric steps are not equal perceptual steps.

**The meter never blinks out while a terminal is resized.** It used to: the right-hand group was all-or-nothing, so a single column of width could make a field affordable and take the meter from drawable to below-its-minimum in one step. The right group now sheds one field at a time, and the segment reserves room for a usable bar rather than only for its text.

Each line has a left and a right group so it spans its width. The right group sheds fields from its own right end until the left fits beside it whole — reserving its width first would let a secondary field evict a primary one, inverting the ranking that segment order expresses. A line with an empty left group still renders its right: the two are independent.

When a filling segment has taken every spare cell, the space between the two groups is exactly one separator wide, because that is what was reserved for it — so the separator is drawn there. Leaving it blank produced a conspicuous hole with no divider to explain it. Without a filling segment the gap is genuine slack pushing the right group to the edge, and a divider stranded in the middle of it would only look lost.

Key hints drop **whole bindings** from the end rather than being cut to width: a hint truncated mid-word reads as a rendering fault and tells the operator nothing.

### Paths clip from the left

The directory in the top bar keeps its tail. Given too little room, `…/aiagent/internal/tui` tells you where you are and `/home/steven/code/…` does not — so `ui.ClipLeft` cuts from whichever end preserves the part that identifies the thing.

### Measuring context against the right number

The denominator is `UsageUpdate.effective_ceiling`, not the model's raw `context_window`. The ceiling is what remains after the kernel reserves room for expected output and tool schemas, and the protocol names that pair as the one "a context-budget indicator divides to get pressure". Dividing by the raw window would understate pressure — you would read 70% while the next turn was already at risk of not fitting.

**Unknown is not zero.** Before the first `UsageUpdate` the context segment is absent, and the same holds for a ceiling the kernel has not resolved. A meter confidently reading 0% before any turn has run is a lie that looks like a fact.

Past 85% the hint line replaces the focus label with a plain warning. It names no command: compaction here is automatic — a context provider declaring `compactor: true` receives the conversation history and returns a rewritten one on its own initiative ([`context/protocol.md#session-wide-conversation-compaction`](../../specifications/context/protocol.md#session-wide-conversation-compaction)) — so there is no operator-invoked `/compact` to point at.

### What the protocol actually supplies

Only some of this has a wire source. The rest arrives as shell messages, which is deliberate: the shell performs no I/O, so anything it cannot be *told* it cannot know.

| Field | Source |
|---|---|
| model, session, status | `ServerEvent` session lifecycle |
| context, cost, token split, cache rate | `UsageUpdate`, including the per-turn `model.v1.Usage` |
| effort, session length | `ModelSpec.thinking` and `SessionInfo.started_at` exist, but no frontend event carries them; the bridge resolves and passes them through |
| directory, repository | **No protocol source, by design.** There is no workspace concept in the wire contracts and none should be invented for a status bar; `cmd/tui` supplies these |
| branch, subtree, PR | Not rendered by the shell at all — a git widget contributes them as ordinary sidebar content, so there is one source for one truth |
| lines read and changed | **No protocol source.** No event aggregates per-tool line counts; a tool provider knows them, so a widget or a kernel-side rollup is the path |

Session length arrives pre-computed rather than as a start time, because the model never reads the clock — whatever drives the shell decides how often it ticks.

## Keymap layers — the second gap## Keymap layers — the second gap## Keymap layers — the second gap

Bindings resolve in three layers, highest first: **overlay → focused region → global**. A layer that handles a key stops propagation. `hotkey_hints` renders the currently active layer's bindings, which is what makes the region meaningful rather than a static legend.

| Layer | Binding | Action |
|---|---|---|
| global | `ctrl+c` | First press sends `interrupt`; second within 2s quits. Interrupting cascades to the whole sub-agent tree. |
| global | `ctrl+d` | Quit (only on an empty `input_bar`). |
| global | `tab` | Cycle focus. |
| global | `shift+tab` | Cycle the active agent. |
| global | `ctrl+b` | Toggle `sidebar` when narrow. |
| overlay | `y` / `n` | Allow / deny the focused plan item. |
| overlay | `e` | Edit arguments — opens the `corrected_input` editor. |
| overlay | `a` | Allow with `SESSION` scope. |
| overlay | `esc` | Dismiss where dismissal is meaningful; never silently resolves a pending decision. |
| main_chat | `↑`/`↓`, `pgup`/`pgdn`, `home`/`end` | Scroll; `end` re-pins to the live tail. |
| main_chat | `enter` | Activate the action under the cursor. |
| input_bar | `enter` | Submit as `user_message`. |
| input_bar | `shift+enter` | Insert a newline instead of submitting; the composer grows with it, up to six rows. |
| input_bar | `alt+enter`, `ctrl+j` | The same, for terminals that cannot disambiguate shift+enter. |
| input_bar | `↑`/`↓` | Prompt history, when the cursor is on the first/last line. |

Plan decisions default to `PLAN_DECISION_SCOPE_ONCE`, which the protocol names as the scope a frontend SHOULD send absent explicit operator intent. `SESSION` and `ALWAYS` require the distinct keystrokes above — they are never inferred.

There is no protocol-level keybinding registration, so a widget cannot claim a key. Widgets expose affordances as `ActionNode`s and reach the keyboard through the action cursor. This is a deliberate limitation: it keeps the keymap total and conflict-free, at the cost of widgets not being able to bind accelerators.

## Painting a RenderTree

The painter is a pure function from `*renderv1.RenderNode` plus a width to a styled string. It holds no terminal state, which is what lets the whole node vocabulary be tested headlessly on every CI platform including Windows.

| Node | Treatment |
|---|---|
| `TextNode` | Styled per `TextStyle`; unset means the theme's default, distinct from an explicit `normal`. |
| `CodeBlockNode` | Bordered block, language label when set. No syntax highlighting in the skeleton. |
| `DiffNode` | Hunk headers dim, `+` green, `-` red, context plain. |
| `TableNode` | Column-aligned; flat string cells only, as the protocol defines it. |
| `LinkNode` | Label plus a dimmed URL, so the target stays visible in any terminal. |
| `ListNode` | Bulleted or numbered; recurses. |
| `GroupNode` | Transparent — no border, no indent, no label. Adding chrome here would violate the node's stated meaning. |
| `CollapsibleNode` | Summary line with a disclosure marker, honoring `collapsed_by_default`; expandable via the action cursor. |
| `SubSessionNode` | A one-line pointer to the child session with its summary — never inlined. |
| `ActionNode` | A button-styled affordance, highlighted when it is under the cursor. |

**Unknown node types are the interesting case.** The protocol requires a frontend to render gracefully any variant added after it shipped, and `pkg/frontend`'s existing `FallbackText` already implements exactly that traversal. The painter delegates to it rather than reimplementing the fallback, and a `render_failed` on one node degrades that subtree to fallback text — it never crashes the process, which the error taxonomy states as a MUST.

## Theme

A small token set, not a general theming engine: one `Theme` struct mapping each `TextStyle` plus the shell's own chrome roles (border, focused border, cursor, backdrop, region title) to a `lipgloss.Style`. Two built-ins, dark and light, selected from the terminal's detected background with an explicit config override. Lip Gloss v2 degrades color automatically down to 16-color and monochrome terminals, so the tokens are authored once in truecolor.

Keeping this a token table rather than per-call styling is what allows a later config-driven theme without touching the painter.

## Wiring the kernel stream to Bubble Tea

Two event loops meet here, and the bridge between them is the whole integration:

- **Inbound**: the `Attach` stream's `ServerEvent`s are read on their own goroutine, translated one-to-one into `tea.Msg` values, and delivered with `Program.Send`. No kernel type reaches the Bubble Tea model unconverted.
- **Outbound**: operator actions become `tea.Cmd`s that write `ClientEvent`s to a buffered channel; a single writer goroutine drains it into the stream, preserving arrival order — which matters because the kernel processes `ClientEvent`s in arrival order per session and resolves decisions first-response-wins.

The shell must handle a decision it did not win: a second response to an already-resolved item is rejected with an `invalid_client_event`-category error specifically so the UI can show "already decided elsewhere" instead of appearing to hang. The overlay renders that outcome rather than swallowing it.

Because `Attach` is connection-scoped and multiplexes sessions by `session_id`, the shell keeps one store per attached session and paints the focused one, with `session_tree_update` driving a sub-session indicator in `top_bar`.

## Package layout

```
cmd/tui/                 thin entrypoint: flag parsing, TTY open, program wiring
internal/tui/theme/      the design tokens: palette -> semantic Tokens, the
                         spacing scale, and the border presets
internal/tui/ui/         the utility layer: the chainable Style builder plus
                         Panel, StatusLine, and Badge built from it
internal/tui/paint/      RenderTree -> styled string (pure, headless-testable)
internal/tui/region/     the placement store, ordering, streaming buffers
internal/tui/shell/      the Bubble Tea model: layout, focus, keymap, compose,
                         the EventSource seam, and a scripted demo source
```

The dependency direction is one-way and worth keeping that way: `theme` knows nothing, `ui` consumes `theme`, `paint` consumes both, and `shell` composes all of them. A color or a spacing value introduced at a call site in `shell` — rather than added to the scale in `theme` — is the failure mode this layering exists to prevent.

`EventSource` is declared in `shell` rather than in a package of its own, because that is where it is consumed and the shell needs exactly one method of it — the house rule is to define an interface as narrowly as its consumer needs, at the consumer. When the real gRPC bridge arrives it becomes a second implementation beside the demo one; if a third ever appears, that is the point to promote the seam to the interface/driver layout, not before.

Each package carries the `README.md` + `CLAUDE.md` pair the layout rules require. All four are pure — no I/O, no terminal, no logging — which puts them under the pure-domain exemption for instrumentation and is what lets the whole shell be tested by calling `Update` directly, with no TTY and no kernel. Logging lives in `cmd/tui`, which is where the process boundary actually is.

### A note on shift+enter

A bare terminal cannot distinguish `shift+enter` from `enter`: both are carriage return. Bubble Tea negotiates key disambiguation at startup (the Kitty keyboard protocol, plus `modifyOtherKeys` level 2), which makes the distinction available on terminals that support it — and most modern ones do.

Because that negotiation can fail, `alt+enter` and `ctrl+j` are bound to the same action. `ctrl+j` is literally line feed and works everywhere, so there is always a way to insert a newline no matter what the terminal supports.

## Deliberately deferred

- **Syntax highlighting** in `CodeBlockNode` — a real dependency decision, not skeleton work.
- **Telling the kernel which agent is active.** See the note under "Agents": the protocol has no client event for it. The shell tracks and displays the selection; conveying it needs a protocol answer first.
- **The kernel-side attach path.** No `internal/` code launches a frontend plugin and drives its `Attach` stream yet; `cmd/agent` is non-interactive and `internal/interactive/drivers` still flags its frontend-backed driver as pending. Until that lands, `drivers/fake` is what makes the shell runnable, and it is a test fixture rather than a shipping path.
- **Widget hosting.** The shell renders widget-contributed `RenderTree`s like any other producer's, so nothing widget-specific is needed here — but no widget plugin exists to contribute yet.
