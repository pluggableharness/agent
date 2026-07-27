# internal/tui/shell — agent notes

## The shell must never write to stdout or read stdin

As a `hashicorp/go-plugin` subprocess the shell's stdout carries the handshake and is piped into the host's logger. Painting there corrupts the handshake; reading stdin competes with the plugin transport. `cmd/tui` opens the controlling terminal and passes it as both `tea.WithInput` and `tea.WithOutput`. Never add a `fmt.Println` anywhere in this package, and never remove those program options.

## Bubble Tea v2 specifics that differ from v1

- The module path is `charm.land/bubbletea/v2`, not `github.com/charmbracelet/...`.
- `Model.View()` returns a `tea.View`, not a string, and **alt-screen is a property of that View** — there is no `tea.WithAltScreen()` program option. Every returned View sets `AltScreen`, including the empty one painted while quitting.
- Keys arrive as `tea.KeyPressMsg`. Its `String()` returns the literal text for printable keys and the keystroke name otherwise, which is why space arrives as `"space"` and is handled by its own case rather than by the printable check.
- Full-screen takeover is entirely `View` properties: `AltScreen`, `BackgroundColor`/`ForegroundColor`, `WindowTitle`, `Cursor`, and `MouseMode`. There are no matching program options.

## Takeover is only complete if the mouse is claimed

`View.MouseMode = tea.MouseModeCellMotion` is what makes the wheel scroll the transcript. Drop it and the wheel silently scrolls the terminal's buffer behind the alt screen — the gesture appears to work while doing something else entirely, which is worse than not handling it. `TestViewClaimsTheTerminal` asserts every takeover property; the cost of the mode is that drag-selection needs the terminal's shift+drag override.

Scroll offset tracks the tail while pinned (see `window`), so the first scroll away from the live edge starts where the operator is looking rather than jumping to the top.

## shift+enter needs key disambiguation

A bare terminal sends CR for both `enter` and `shift+enter`. Bubble Tea negotiates the Kitty keyboard protocol and `modifyOtherKeys` level 2 at startup, which makes them distinguishable where supported. `alt+enter` and `ctrl+j` are bound to the same action as fallbacks — `ctrl+j` is literally line feed and always works. Do not drop them.

## The cursor's row comes from Layout, never from local arithmetic

`Layout.ComposerTop()` is the single place that knows how the frame's bands stack. `cursorScreenPos` derives from it rather than re-deriving the sum, because the two versions already drifted once: the header grew from a single line into a bordered box, `frame()` accounted for it and the cursor did not, and the caret sat two rows above the text it belonged to.

`TestCursorLandsOnTheComposerRow` asserts against the *painted frame* — it finds the row containing the prompt and checks the cursor is on it — rather than against the arithmetic, which is what makes that class of mistake fail loudly instead of silently.

## Keep constants in sync with the design doc

`layout.go`'s breakpoints are documented in `docs/first-party/frontends/tui.md`. They are the same numbers stated twice; change both or the doc becomes a lie.

## Overlay modality is a correctness property, not styling

While an overlay is up it captures the keyboard entirely and the focus ring is suspended. `esc` explicitly does **not** resolve a pending decision — turning "go away" into an allow or a deny on the operator's behalf would be indefensible, and there is a test named for it. The protocol also requires overlay content be visually distinct from ambient content.

## Action nodes dispatch unchanged

`activate` passes `tool_name`, `args`, and `provider` through verbatim. The protocol states this as a MUST. Do not normalize, default, or reinterpret them on the way out.

## `regionKey` returning false is what makes layering work

A region handler that returns `false` lets the key fall through to the global layer. A handler that swallows everything would make `ctrl+c` unreachable from the composer. Keep the `default: return false` branches.

## This package is pure domain

No `log/slog`, no `internal/telemetry`, no I/O — the pure-domain exemption in `.claude/rules/logging-telemetry.md` applies. Logging belongs in `cmd/tui`, which is where the process boundary actually is.

## `demo.go` is a fixture with an expiry date

It exists only because the kernel-side attach path does not. Delete it when the real bridge lands rather than growing it into a second implementation.
