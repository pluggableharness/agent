---
paths:
  - "internal/**"
---

# Per-package documentation

Every package directory under `internal/` MUST contain both a `README.md`
and a `CLAUDE.md`, alongside its `.go` files.

- `README.md` — human-facing: what the package does, why it exists, how it
  fits into the surrounding feature (see `go-layout.md` for the
  interface/driver shape most `internal/` packages follow).
- `CLAUDE.md` — agent-facing: conventions, gotchas, or constraints specific
  to this package that aren't already covered by a repo-wide rule in
  `.claude/rules/`. If there's nothing package-specific to say, keep it
  short rather than omitting it.

This applies per package directory, not per feature — e.g. both
`internal/memory/` and each driver under `internal/memory/drivers/<name>/`
get their own pair, since each is its own package.

New packages should add both files in the same commit that creates the
package, not as a follow-up.
