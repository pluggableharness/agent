# Frontend implementations — index

This directory contains first-party reference documentation for frontend providers — the plugin category that owns the display surface, paints [`RenderTree`](../../specifications/frontend/render-tree.md) content into the region vocabulary, and turns operator input into `ClientEvent`s.

> [!IMPORTANT]
> These are descriptive design documents, not the protocol spec itself: the frontend and widget provider protocols, the `RenderTree` intermediate representation, and the region/placement model live in [`docs/specifications/frontend/`](../../specifications/frontend/README.md), which remains the source of truth. A frontend's layout, focus model, and keymap are explicitly *not* protocol — the spec's own reference-TUI table is labelled non-normative — so everything documented here is one conforming implementation's choices, not a constraint on any other frontend.

## Implementations

| Frontend | Document | Surface |
|---|---|---|
| Reference TUI shell | [tui.md](tui.md) | Full-screen terminal, Bubble Tea + Lip Gloss |

## Why these documents exist

The protocol deliberately defines *what* content arrives and *where* it is placed, and stops there — it specifies no focus model, no keybinding schema, no resize semantics, and no scrollback behavior. Those are display concerns that only a concrete surface can answer, and they must be answered before widget plugins are written: a widget contributing to `sidebar` needs to know whether that region can hold focus and how its `ActionNode`s become reachable from the keyboard. Documenting each frontend's resolution of those gaps is what gives integrations a stable target to attach to.
