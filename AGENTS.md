# Agent notes

Orientation for AI coding agents working in this repository. [`CONTRIBUTING.md`](CONTRIBUTING.md) is the full contributor contract; [`docs/specifications/`](docs/specifications/README.md) is the source of truth for all protocol and kernel behavior — where code and spec disagree, the spec wins.

## Branch protection (enforced by GitHub rulesets, not convention)

- `main` rejects direct pushes unless the authenticated account holds the maintainer/admin ruleset bypass. Default to a feature branch and a PR; never assume a push to `main` will land.
- Force pushes (including `--force-with-lease`) and deletion of `main` are blocked for **everyone** — do not retry a rejected push with force variants.
- PRs merge only squash or rebase (`main` requires linear history), with one approving review, resolved review threads, and eleven required status checks green: the CI jobs, golangci-lint, gosec, govulncheck, dependency review, and CodeQL.
- Required checks are matched by **job name**. If you rename a job in `.github/workflows/`, the `protect-main` ruleset must be updated in the same change, or every subsequent merge waits on a check that no longer exists.
- `v*` tags are create/update/delete-restricted to admins — `release.yml` ships whatever a `v*` tag points at via GoReleaser. Never create or move a `v*` tag as part of unrelated work.

## Non-negotiable repo rules

- Compiled artifacts go to `bin/` — never the repo root, `/tmp`, or `$TMPDIR`. GoReleaser's `dist/` is the only exception, and only via `goreleaser` itself.
- `pkg/*/proto/v1/` is 100% generated — change `.proto` files under `api/` and run `buf generate`; CI fails on drift.
- All Markdown is GFM, one unwrapped line per paragraph, with callouts written as GitHub alerts (`> [!NOTE]`); the MkDocs site consumes the same files with GitHub-identical anchor slugs, so never use `!!! note` dialect or renumber-style cross-references.
- The local CI gate before any PR: `go build ./...`, `go vet ./...`, `gofmt -l -s .` (empty), `go test -race -shuffle=on ./...`, `golangci-lint run`; docs changes additionally build with `uv run --only-group docs mkdocs build --strict`.
