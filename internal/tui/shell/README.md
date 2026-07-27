# internal/tui/shell

The reference TUI's frame: layout, focus, keymap, and the composition of every region into one painted view.

## What lives here

| File | Owns |
|---|---|
| `layout.go` | Geometry: which regions fit, how much room each gets, and the fixed order they are dropped in as space runs out |
| `focus.go` | The focus ring and its cycling rules |
| `keymap.go` | Bindings, the three-layer precedence stack, and the hint line those layers generate |
| `agent.go` | The selectable agent roster and the ring `shift+tab` cycles |
| `format.go` | Status-bar value formatting: tokens, cost, duration, percentages |
| `input.go` | The composer buffer: editing, multi-line, and prompt history |
| `event.go` | The message vocabulary and the `EventSource` seam |
| `model.go` | The Bubble Tea model that ties them together |
| `demo.go` | A scripted source that runs the shell without a kernel |

## The four gaps this package fills

The frontend protocol defines what content arrives and where it is placed, and deliberately stops there. It specifies no focus model, no keybinding schema, no resize semantics, and no scrollback behavior. This package answers all four for the terminal:

- **Focus** targets regions, not nodes. Within a focused region an action cursor selects among reachable elements. Overlay is modal and captures the keyboard outright.
- **Keybindings** resolve overlay → focused region → global, with a handled key stopping propagation.
- **Resize** drops regions in a documented order — sidebar, then hints, then top bar — with `main_chat` and `input_bar` never dropped.
- **Scrollback** pins to the live tail unless the operator scrolls away from it, and the mouse wheel is claimed so it scrolls the transcript rather than the terminal behind the alt screen.

Session data is placed by **how fast it changes**: where the work is (directory, repository) in the top bar, settings that move with the agent (agent, model, thinking, effort) in the composer title, detail (`usage`) in a sidebar panel the shell contributes itself, and only volatile state — context, cache, cost, elapsed — on the single line beneath the composer. Version-control detail is left to a git widget rather than duplicated in shell chrome. Space beside the input is the most-looked-at part of the screen and goes to what actually moves.

The transcript is bottom-anchored for the same reason: the newest message belongs next to the composer, with empty space above rather than between them.

Only some of these fields have a protocol source; the rest arrive as messages (`WorkspaceMsg`, `EditStatsMsg`) precisely because the shell performs no I/O and so cannot discover them. See `format.go` for rendering rules and the design doc for the full provenance table.

It also carries the active **agent** (`Code`/`Plan`/`Chat` by default), cycled with `shift+tab`. An agent is a name plus a theme *tone*, so a roster can come from configuration without naming colors; see `agent.go` and the design doc.

The operator-facing version of these decisions is [`docs/first-party/frontends/tui.md`](../../../docs/first-party/frontends/tui.md). The constants here are what that document describes; changing one means changing both.

## Testability

`Model` performs no I/O and never touches a terminal. An `EventSource` translates whatever it is attached to into the message vocabulary in `event.go`, and operator actions leave through an emitter callback. Every behavior — key routing, focus cycling, responsive dropping, overlay modality — is exercised by calling `Update` directly, with no TTY and no kernel.

`EventSource` is declared here rather than beside an implementation because this is where it is consumed and the shell needs exactly one method of it.

## Not built yet

No kernel-side code launches a frontend plugin and drives its `Attach` stream, so `DemoSource` is currently the only source. It is a fixture, not a shipping path.
