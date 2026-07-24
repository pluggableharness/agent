---
applyTo: "**/*.md"
---

# Markdown and specification conventions

The full rules live in `.claude/rules/markdown.md` and `docs/specifications/conventions.md`.

- GitHub Flavored Markdown only — no Pandoc, reST, or MkDocs/Sphinx directive syntax; callouts use GFM alerts (`> [!NOTE]`).
- Prose is never hard-wrapped: one unwrapped line per paragraph and per list item, so diffs show exactly the sentence that changed.
- Cross-references are relative file path plus GFM heading anchor, never section numbers. Before renaming any heading, grep `docs/specifications/` for its anchor — inbound links break silently. Keep heading text plain so slugs stay stable.
- Fix-forward editing: the docs describe the system as it currently is. Write corrections as current, unqualified truth — no strikethrough, no "previously this said" narrative, no corrections sections.
- RFC 2119 keywords (MUST/SHOULD/MAY) are conformance requirements, not emphasis — do not add, remove, or downgrade them casually.
- `github.com/agentco/...` in examples is a deliberately fictional placeholder org — do not "fix" it to a real location.
- New files under `docs/` must be added to the `mkdocs.yml` nav or `mkdocs build --strict` fails.
