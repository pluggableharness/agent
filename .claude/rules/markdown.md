---
paths:
  - "**/*.md"
---

# Markdown standard

## Dialect

GitHub Flavored Markdown (GFM) is the only supported dialect — every `.md`
file in this repo, public or internal, is authored and reviewed as GFM.
Use GFM syntax directly: fenced code blocks with a language tag, tables,
task lists, strikethrough (`~~text~~`), autolinks. Do not reach for another
dialect's extensions (Pandoc attributes, reST-style directives, MkDocs/
Sphinx-specific admonition syntax) even where they'd render similarly —
this repo has exactly one target renderer (GitHub) and one grammar to
reason about.

This is not just a style preference: `docs/specifications/conventions.md`'s
cross-reference scheme depends on GitHub's specific heading-to-anchor slug
algorithm (lowercase, punctuation stripped, spaces to hyphens, no
collapsing of consecutive hyphens). That algorithm is a GFM rendering
behavior, not part of CommonMark proper — writing anything other than GFM
risks anchors that resolve on one renderer and silently break on another.

## No hard-wrapping — one line per paragraph

MUST NOT hard-wrap prose at a fixed column width (72, 80, 100 — any of
them). Write each paragraph, each list item, and each blockquote as a
single unwrapped line, however long that makes it; let the reader's editor
or GitHub's own renderer soft-wrap for display. This applies uniformly —
don't hard-wrap "just this one file" because it looks tidier in a terminal.

Why this is a hard rule, not a preference: a hard-wrapped paragraph reflows
in its entirety the moment a single word changes anywhere in it, so a
one-word edit produces a multi-line diff — noise in code review and `git
blame` that has nothing to do with what actually changed. A single
unwrapped line per paragraph means a diff shows exactly the sentence that
changed, nothing else.

**Exceptions** — cases where a literal line break is part of the content,
not a styling choice, and hard-wrapping doesn't apply:

- Fenced code blocks (```` ``` ````) — preserve the exact source content,
  including any line breaks that belong to the quoted code/config/output.
- Tables — GFM tables are genuinely one row per line; that's the syntax,
  not a wrapping decision.
- Reference-style link/footnote definitions.
- Content deliberately quoting another document's actual formatting (e.g.
  reproducing a real `.proto` or HCL snippet verbatim).

## Headings

Keep heading text plain (no inline code spans, links, or emphasis wrapping
the entire heading) wherever another file might need to link to it by
anchor — a heading that's just `` `Foo` `` slugs differently than plain
`Foo`, and a future editor changing how a heading is styled can silently
break every inbound anchor link. If a heading must reference a symbol or
file name, keep the surrounding words plain and accept the resulting
slug rather than fighting it.

## Second renderer: MkDocs Material

`docs/` is also built into the documentation site (mkdocs.yml at the repo
root, deployed to docs.pluggableharness.ai). This does NOT relax the
GFM-only rule — the site is configured to consume plain GFM:

- Anchor slugs are GitHub-identical (`pymdownx.slugs.slugify` with
  `case: lower` preserves GitHub's no-hyphen-collapsing behavior), so the
  conventions.md cross-reference scheme works unchanged on both renderers.
  Never switch the site to a different slugifier.
- Callouts use GFM alert syntax (`> [!NOTE]`, `> [!IMPORTANT]`, etc.) —
  valid GFM that renders on GitHub AND as Material admonitions on the
  site. MkDocs/Sphinx `!!! note` directive syntax remains banned.
- YAML front matter is permitted only on site-only pages that have no
  GitHub-reading audience: `docs/index.md` and `docs/first-party/index.md`.
  Spec and catalog files stay front-matter-free.
- `mkdocs build --strict` treats broken links, broken anchors, and files
  missing from the mkdocs.yml nav as failures — adding a new doc file
  means adding it to the nav.
