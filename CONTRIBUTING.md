# Contributing to PluggableHarness Agent

Thanks for helping build the harness. Bug reports, protocol refinements, and provider proposals are all welcome. Questions and ideas belong in [Discussions](https://github.com/orgs/pluggableharness/discussions); confirmed defects belong in [Issues](https://github.com/pluggableharness/agent/issues); security reports go through the process in [`SECURITY.md`](SECURITY.md) — never a public issue.

## The one rule that governs everything else

`docs/specifications/` is the source of truth. Code implements the spec, not the other way around. A PR that changes observable behavior updates the relevant spec document in the same PR, and RFC 2119 keywords (MUST/SHOULD/MAY) in those documents are load-bearing — read them precisely and write them deliberately. If your change exposes a conflict between spec and code, fix the code or raise the conflict; never silently bend the spec to match an implementation.

## Building a third-party provider?

You may not need a PR at all — that's the point of the architecture. Build against the protocol contracts in `docs/specifications/`, tag a semver release on your own repo, and it's installable from `agent.hcl` directly. Tag the repo with the `agent-harness-provider` topic so it's discoverable. Open a Discussion if the protocol is unclear — ambiguity you hit is a spec bug worth reporting.

## Development setup

- Go 1.26+ (the version in `go.mod` is authoritative).
- [`buf`](https://buf.build) for protobuf generation, [`golangci-lint`](https://golangci-lint.run) v2 for linting.
- Optional, for the full local gate: `gosec`, `govulncheck`, `goreleaser`.

## Before you open a PR

CI enforces all of this; save yourself the round-trip by running it locally:

```sh
go mod tidy            # must be a no-op
go build ./...
go vet ./...
gofmt -l -s .          # must print nothing
go test -race -shuffle=on -covermode=atomic ./...
golangci-lint run
```

Touched a `.proto` under `api/`? The protobuf gate also runs in CI:

```sh
buf lint
buf format -w                                      # CI enforces formatted protos
GOBIN=$PWD/bin go install tool                     # generators pinned via go.mod tool directives
PATH=$PWD/bin:$PATH buf generate                   # regenerate pkg/*/proto/v1 — commit the result
```

CI additionally runs `buf breaking` against the PR base branch — a wire-contract break fails the build by design; if it's intentional, say so explicitly in the PR.

Ground rules the gate can't fully check:

- **Compiled artifacts go to `bin/`, always.** Never compile to the repo root, `/tmp`, or `$TMPDIR` — `go build -o bin/<name>`, `go test -c -o bin/<name>.test`. GoReleaser's `dist/` is the only sanctioned exception, and only via `goreleaser` itself.
- **Never hand-edit `pkg/*/proto/v1/`** — it is 100% generated. Change the `.proto` under `api/` and run `buf generate` from the repo root.
- **Layout follows the driver pattern.** Interfaces live at `internal/<feature>/`, implementations under `internal/<feature>/drivers/<name>/`, and `cmd/` stays thin wiring. Every package under `internal/` ships a `README.md` and `CLAUDE.md` in the same commit that creates it.
- **Atomic commits** — one logical change each, imperative subject line; split refactors from features.

## Documentation PRs

`docs/specifications/` has two hard editorial rules, defined in [`docs/specifications/conventions.md`](docs/specifications/conventions.md):

- Cross-references are relative path + heading anchor (`[cost computation](provider/protocol.md#cost-computation)`), never section numbers. Before renaming a heading, grep the tree for its anchor — inbound links break silently.
- Fix-forward: the docs describe the system as it is. Corrections are written as current, unqualified truth — no strikethrough, no "previously this said" narrative.

All Markdown in this repo is GitHub Flavored Markdown with one unwrapped line per paragraph — no hard-wrapping at a fixed column.

## Review

Every PR needs a passing CI run and a review from a code owner (see `.github/CODEOWNERS`). Spec changes get the closest scrutiny — expect discussion on MUST-level wording; that's the contract every provider author builds against.

## Branch protection & merging

`main` is governed by a repository ruleset ([Rules → Rulesets](https://github.com/pluggableharness/agent/settings/rules), not classic branch protection), so the mechanics below are enforced by GitHub, not convention:

- **No direct pushes.** Changes land through a PR from a feature branch; only maintainers and admins carry a ruleset bypass. Force pushes and branch deletion are blocked for everyone, bypass included.
- **The PR gate**: one approving review, all review threads resolved, and stale approvals are dismissed when new commits are pushed.
- **Squash or rebase merges only.** `main` requires linear history, so merge commits are rejected — the merge-method picker simply won't offer one.
- **Required status checks**: the CI jobs (build & vet, gofmt, the protobuf gate, tests on all three platforms), golangci-lint, gosec, govulncheck, dependency review, and CodeQL must all pass before merge. These are matched by job name — renaming a workflow job requires updating the ruleset in the same change, or merges will wait forever on a check that no longer reports.
- **Release tags are locked.** Creating, moving, or deleting `v*` tags is restricted to admins, because `release.yml` hands whatever a `v*` tag points at straight to GoReleaser.

The docs deploy workflow is path-filtered and therefore deliberately **not** a required check — a required check that never runs would deadlock the PR.
