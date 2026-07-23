# PluggableHarness Agent

An AI coding harness built as a Go microkernel: the kernel owns plugin lifecycle, the plan/apply policy gate, config, and the session log — everything opinionated (models, tools, context, memory, frontends, widgets) is an out-of-process gRPC plugin. Repo: [github.com/pluggableharness/agent](https://github.com/pluggableharness/agent). This file orients a fresh session; it repeats nothing from global instructions or `.claude/rules/`.

## Source of truth

`docs/specifications/*` is authoritative for everything it covers. RFC 2119 keywords (MUST/SHOULD/MAY) are load-bearing, not stylistic. Start at `docs/specifications/README.md`; `conventions.md` there governs how the tree is read and cross-referenced. Where code and spec disagree, the spec wins — fix the code or surface the conflict; never silently bend the spec toward the code.

## Repo map

| Path | Contents |
|---|---|
| `docs/specifications/` | The protocol contracts: one directory per plugin category (`provider/` = model, `tool/`, `context/`, `memory/`, `frontend/` incl. widgets) plus kernel behavior (`agent-loop/`, `configuration/`, `kernel-callbacks.md`, `state-backend.md`) |
| `docs/first-party/` | Separate first-party catalog (tools, model providers). **Not** the tool *protocol* — that's `docs/specifications/tool/` |
| `api/` | `.proto` sources, buf module root (`buf.yaml`, `buf.gen.yaml`) |
| `internal/` | Kernel-side implementation — config, registry, policy, agentprofile, pluginruntime, kernelcallback, telemetry, log, hclsecret, producer |
| `pkg/` | Plugin-author surface: hand-written SDK per category + `pkg/*/proto/v1/` generated stubs |
| `.claude/rules/` | Path-scoped rules that auto-load — Go style/layout/testing/docs, proto, gRPC, plugin runtime, logging/telemetry, determinism, markdown |

## Current state

Kernel-side packages in `internal/` and the `pkg/` SDK are real, tested Go. There is no `cmd/` binary yet, and most plugin categories exist only as spec. Implementation is spec-first: before writing code, confirm the relevant spec exists and is settled; if it has open questions bearing on the task, raise them instead of coding against an assumption. Don't start new implementation work without being asked.

## Toolchain, testing, and CI

`.github/workflows/` defines the full gate; run the same tools locally before considering a change done:

| Workflow | Tools it runs | Local equivalent |
|---|---|---|
| `ci.yml` | Tidy check (`go mod tidy` must be a no-op), build, vet, `gofmt -s` check, race+shuffle tests with atomic coverage on Linux/macOS/Windows, and the protobuf gate: `buf lint`, `buf format` check, `buf breaking` vs base on PRs, generated-code drift check | `go build ./...` · `go vet ./...` · `gofmt -l -s .` · `go test -race -shuffle=on -covermode=atomic ./...` · `buf lint` · `buf format --diff --exit-code` |
| `lint.yml` | golangci-lint v2, full-repo (config: `.golangci.yml`) | `golangci-lint run` |
| `security.yml` | gosec with `-exclude-generated` (SARIF → code scanning), govulncheck, dependency-review on PRs | `gosec -exclude-generated ./...` · `govulncheck ./...` |
| `codeql.yml` | CodeQL Go analysis (push/PR to main + weekly) | CI-only |
| `scorecard.yml` | OpenSSF Scorecard (main + weekly) | CI-only |
| `release.yml` | Verify job (build + race tests at the tag) gating GoReleaser v2 (config: `.goreleaser.yaml`) on `v*.*.*` tags | `goreleaser release --snapshot --clean` to smoke-test |

Everything in the CI and Lint columns passes today; keep it that way. Dependabot watches `go.mod` and the workflow actions. Protos: change `.proto` under `api/`, then `buf format -w` and regenerate with `GOBIN=$PWD/bin go install tool && PATH=$PWD/bin:$PATH buf generate` — generator versions are pinned by `go.mod` `tool` directives; never hand-edit anything under `pkg/*/proto/v1/` (100% derived output, and CI fails on drift).

## Branch protection — GitHub rulesets, not convention

`main` and `v*` tags are governed by repository rulesets (Rules → Rulesets, not classic branch protection); [`CONTRIBUTING.md`](CONTRIBUTING.md) documents the contributor-facing mechanics. What matters operationally here:

- Direct pushes to `main` land only via the maintainer/admin bypass the operator holds; force pushes and `main` deletion are blocked for everyone, bypass included — never retry a rejected push with a force variant.
- PRs merge squash/rebase only (linear history) behind eleven required status checks: the CI jobs, golangci-lint, gosec, govulncheck, dependency review, and CodeQL. Checks are matched by **job name** — renaming a workflow job means updating the `protect-main` ruleset in the same change, or merges wait forever on a check that no longer reports. The path-filtered Docs workflow is deliberately not required.
- `v*` tag creation/update/deletion is admin-only: `release.yml` hands whatever a `v*` tag points at to GoReleaser, so never touch `v*` tags as a side effect of other work.

## Build output — bin/ only, no exceptions

Every compiled artifact goes to the repo's `bin/` directory (gitignored for exactly this purpose):

- **Never compile to the repo root.** No `go build -o agent`, no bare `go test -c` dropping `*.test` files where you stand. Always `go build -o bin/<name>` and `go test -c -o bin/<name>.test`.
- **Never compile to `/tmp` or any equivalent** (`$TMPDIR`, `os.TempDir()`, per-job tmp dirs) on any system. Scratch binaries live in `bin/` too.
- GoReleaser's `dist/` output is the one sanctioned non-`bin/` artifact path, and only via `goreleaser` itself.

## Editing docs/specifications

- Cross-references are relative path + heading anchor, never section numbers. Before renaming any heading, `grep -rn '#its-anchor' docs/specifications/` — inbound anchors break silently.
- Fix-forward: when one document reveals a gap in another, write the corrected behavior into the target as current, unqualified truth. No "corrections needed" sections, no strikethrough, no history narrative — the docs describe the system as it is.
- `github.com/agentco/...` in examples is a deliberately fictional placeholder org, not a real location.

## Rules discipline

`.claude/rules/*.md` are authoritative for how code and prose are written here and load automatically by path. Never restate their content in a `CLAUDE.md` (this one included) — extend the rule file instead, and keep per-package `CLAUDE.md` files to what's genuinely package-specific.
