# Frontend implementations — index

This directory contains first-party reference documentation for frontend providers — the plugin category that owns the display surface, paints [`RenderTree`](../../specifications/frontend/render-tree.md) transcript content, and turns operator input into kernel-callback calls.

> [!IMPORTANT]
> These are descriptive design documents, not the protocol spec itself: the frontend and widget provider protocols and the `RenderTree` intermediate representation live in [`docs/specifications/frontend/`](../../specifications/frontend/README.md), which remains the source of truth. A frontend's layout, focus model, and keymap are explicitly *not* protocol — the protocol carries no placement vocabulary at all — so everything documented here is one conforming implementation's choices, not a constraint on any other frontend.

## Implementations

| Frontend | Where it lives | Surface |
|---|---|---|
| Reference TUI shell | [pluggableharness/plugin-frontend-tui](https://github.com/pluggableharness/plugin-frontend-tui) | Full-screen terminal, Bubble Tea + Lip Gloss |

A frontend implementation is a plugin like any other, so its design documentation ships with it rather than here. This directory holds what is true across frontends; the per-implementation choices live in each implementation's own repository.

## Why these documents exist

The protocol deliberately defines *what* content arrives and stops there — it specifies no placement, no focus model, no keybinding schema, no resize semantics, and no scrollback behavior. Those are display concerns only a concrete surface can answer, and they have to be answered before anything that contributes content can be written: a plugin publishing a metadata block needs to know whether the frontend surfaces it at all, whether it can hold focus, and how its `ActionNode`s become reachable. Documenting each frontend's resolution of those gaps is what gives integrations a stable target to attach to.
