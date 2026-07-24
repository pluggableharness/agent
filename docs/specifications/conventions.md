# Conventions

How to read and how to write every file under `docs/specifications/`.

## Status

`docs/specifications/` is the authoritative source of truth for this project's plugin protocols and kernel contracts.

## Requirement keywords

> [!IMPORTANT]
> MUST / SHOULD / MAY / MUST NOT are used in the RFC 2119 sense throughout. A MUST is a conformance requirement a plugin author cannot skip and still claim to implement the category; a SHOULD is a strong default a plugin author needs a real reason to deviate from; a MAY is genuine, unconstrained latitude. These keywords are load-bearing — treat them as precisely as the wire contracts and kernel behavior they govern.

## Cross-references — anchors only, never section numbers

**Every cross-reference is a relative file path plus a Markdown heading anchor**, e.g. `[cost computation](model/protocol.md#cost-computation)`. A heading anchor survives reordering of sections; only a heading *rename* breaks it, which is both rarer and easy to catch by grepping for the anchor text across the tree.

When linking to a heading, use GitHub-flavored anchor rules: lowercase, spaces to hyphens, punctuation stripped (`## Cost computation` → `#cost-computation`).

## Code blocks

- **HCL** — real `agent.hcl` syntax, used throughout `configuration/`.
- **Protobuf-shaped schema definitions** — most data-type definitions are shown as `message`-like blocks, tagged ```` ```protobuf ```` for consistent, readable syntax highlighting, since the wire types they describe are proto-shaped.
- **SQL** — real `CREATE TABLE` DDL in `state-backend.md`, matching the actual schema.
- **Go** — snippets illustrating kernel-side algorithms and control flow, tagged ```` ```go ```` where the notation is genuinely Go-flavored (`:=` assignment, `for`/`if`/`switch`).
- **Plain text** — worked request/response sequences, arithmetic formulas, and other illustrative notation that isn't valid syntax in any specific language, tagged ```` ```text ```` rather than forcing a misleading language tag.

## Placeholder conventions

`github.com/agentco/...` is the fictional placeholder org used for provider source addresses in every example — not a real location, just a consistent stand-in.

## Directory shape

Each plugin-category directory (`model/`, `tool/`, `context/`, `memory/`, `frontend/`, `slashcommand/`) follows the same five-file template:

- `README.md` — overview and transport & lifecycle.
- `protocol.md` — every RPC in the category, request/response shape, MUST/SHOULD/MAY behavior.
- `data-types.md` — the category's data schemas (capability structs, error enums, request/response payloads not already covered in `protocol.md`).
- `examples.md` — worked examples: HCL config, wire-shaped snippets, a walked-through call sequence.
- `conformance.md` — the error taxonomy and the MUST/SHOULD/MAY summary matrix, plus any genuinely open questions.

Categories with a distinctive extra concern get one additional file (`tool/reference-catalog.md`, `memory/taxonomy.md`, `frontend/render-tree.md`, `frontend/widget-protocol.md`) rather than overloading one of the five above.

The two kernel-owned (non-plugin) specs, `kernel-callbacks.md` and `state-backend.md`, and the two larger kernel-behavior specs, `agent-loop/` and `configuration/`, don't follow this exact template — see their own `README.md`/opening section for how each is organized.
