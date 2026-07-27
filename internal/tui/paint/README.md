# internal/tui/paint

Renders a `RenderTree` into styled terminal text, and enumerates the keyboard-reachable elements inside one.

## What lives here

- `Painter` — one method per node type, dispatching off the `RenderNode` oneof. Pure: no terminal state, no I/O, no clock.
- `Opts` — the per-paint state the shell owns: width, which action is under the cursor, and which collapsible paths are toggled open.
- `Targets` / `TargetsAt` — the focusable elements in a tree, in the same order the painter emits them, so a cursor index and a rendered node always name the same element.

## Node treatments

| Node | Treatment |
|---|---|
| `TextNode` | Styled per `TextStyle`; unset resolves to the theme default |
| `CodeBlockNode` | Indented block with an optional language label |
| `DiffNode` | Dim hunk headers, `+`/`-` gutters, truncated rather than wrapped |
| `TableNode` | Column-aligned, truncated rather than wrapped |
| `LinkNode` | Label plus dimmed URL |
| `ListNode` | Bulleted or numbered, with hanging indent |
| `GroupNode` | Transparent — no border, indent, or label |
| `CollapsibleNode` | Disclosure marker honoring `collapsed_by_default` |
| `SubSessionNode` | A one-line pointer, never inlined |
| `ActionNode` | Button-styled, highlighted under the cursor |

Diffs and tables truncate instead of wrapping because wrapping destroys the column alignment those node types exist to convey.

## Why it is pure

Every protocol obligation about rendering is testable without a terminal: graceful fallback for unrecognized node types, styles with no visual distinction still showing their text, and a bad node degrading rather than crashing the process. Keeping the painter free of terminal state is what lets the whole node vocabulary be covered on every CI platform, Windows included.

## Related

- [`docs/specifications/frontend/render-tree.md`](../../../docs/specifications/frontend/render-tree.md) — the node vocabulary and the graceful-fallback rule.
- `pkg/frontend.FallbackText` — the fallback traversal this package delegates to rather than reimplementing.
