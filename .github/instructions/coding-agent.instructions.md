---
applyTo: "**"
excludeAgent: "code-review"
---

# Coding agent workflow

Trust these instructions and the other files in `.github/instructions/`; only search the repository when the information here is incomplete or found to be in error.

## Before pushing

Run the full validation gate listed in `repo.instructions.md`. In particular, `go mod tidy` and `gofmt -l -s .` must both be clean no-ops, and `go test -race -shuffle=on ./...` must pass — CI runs exactly these and all required checks must be green before a PR can merge.

## Pull request mechanics

- Work on a feature branch; `main` only accepts squash or rebase merges (linear history) behind required status checks, one approving review, and CODEOWNERS review.
- Commits are atomic — one logical change each, imperative subject line, refactors split from features. No generated-by or co-author trailers.
- A behavior change and its `docs/specifications/` update belong in the same PR.
- Never force-push, and never create, move, or delete `v*` tags — tag pushes trigger releases and are admin-only.

## Proto changes

Edit the `.proto` sources under `api/`, run `buf format -w`, regenerate with `GOBIN=$PWD/bin go install tool && PATH=$PWD/bin:$PATH buf generate` (generator versions are pinned by `go.mod` tool directives), and commit the regenerated `pkg/*/proto/v1/` output. Never edit generated files directly.

## New packages

A new `internal/` package must include its `README.md` and `CLAUDE.md` in the same commit, follow the interface-plus-`drivers/` layout described in `go.instructions.md`, and meet the 80% coverage floor.
