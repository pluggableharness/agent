---
applyTo: "**"
excludeAgent: "cloud-agent"
---

# Code review priorities

When reviewing a pull request, flag these first, in this order:

1. Code/spec divergence: observable behavior changed without a matching `docs/specifications/` edit in the same PR, or code that contradicts what the spec mandates (the spec wins).
2. Hand edits to generated code under `pkg/*/proto/v1/` — these must instead be `.proto` changes under `api/` plus regeneration.
3. Violations of the hard Go rules: `return nil, nil`, `init()` functions, global mutable state, logging an error and also returning it, `time.Now()` used for ordering instead of the persisted `sequence`, serialized output depending on map iteration order, secrets in log output, gRPC errors surfaced as `codes.Unknown`.
4. New or changed logic in `internal/` without accompanying tests, or changes that would drop a package below the 80% coverage floor; tests using mock frameworks or testify instead of stdlib `testing` with hand-written fakes.
5. Docs regressions: renamed headings that break inbound relative-path anchors, hard-wrapped prose, non-GFM syntax, or history-narrative edits instead of fix-forward corrections.

Do not flag: style inside generated files, formatting that `gofmt -s` or `buf format` would fix (CI gates those), or commit-message wording.
