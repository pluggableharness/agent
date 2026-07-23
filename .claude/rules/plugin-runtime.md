---
paths:
  - "**/*.go"
---

# Plugin runtime (`hashicorp/go-plugin`) conventions

Every one of the six plugin categories is a subprocess speaking gRPC over
`hashicorp/go-plugin`. This file covers the lifecycle/runtime half; `grpc.md`
covers RPC shape and `proto.md` covers wire typing.

## Handshake

- Every plugin category uses the same `plugin.HandshakeConfig`: a shared
  magic cookie key/value and a `ProtocolVersion` field. Do not give
  categories different cookies — the uniform handshake is what lets the
  kernel reject a mismatched-protocol plugin before ever calling into it.
- `ProtocolVersion` is bumped only on a breaking wire change (see `proto.md`'s
  `buf breaking` rule) — bumping it and shipping a `v1`→`v2` proto package
  bump happen together, never independently.
- The kernel-side plugin client always checks the negotiated protocol
  version before issuing the first category RPC; a mismatch is a startup
  error, not a runtime error discovered on first call.

## Subprocess lifecycle

- Plugin processes are launched with `exec.CommandContext` under a context
  the kernel controls, so killing the kernel's context reliably kills the
  subprocess tree — no orphaned plugin processes.
- Graceful shutdown: on session end or plugin unload, the kernel calls the
  plugin's shutdown path (go-plugin's `Kill()` after allowing in-flight RPCs
  to finish, not a bare `SIGKILL` as the first move). `SIGKILL`/hard-kill is
  a timeout escalation, not the default path — same "don't reach for `-9`
  first" principle as any other process management.
- A plugin crash (subprocess exit, broken pipe) surfaces to the kernel as
  the `process_crashed` error category (`docs/specifications/tool/conformance.md`'s `ToolErrorCategory`,
  mapped to `codes.Unavailable` per `grpc.md`) — the kernel does not treat a
  crashed plugin as a silent hang; it fails the in-flight call promptly.

## Generated code hygiene

- Nothing under any `pkg/<category>/proto/` directory is ever hand-edited.
  If generated output looks wrong, the fix is in `api/` (the source) or the
  `buf` config, followed by regenerating — never a manual patch to a
  `.pb.go` file.
- Regeneration is `buf generate` from the repo root, driven by `buf.gen.yaml`
  and `buf.yaml`. Don't claim a `buf generate` (or `buf lint`/`buf breaking`)
  was run unless it actually was (per the project `CLAUDE.md` "no fabricated
  commands" rule, which this inherits).
- Every `pkg/<category>/proto/` directory is excluded from the coverage
  floor (`go-testing.md`) and from `golangci-lint` (`go-style.md`) — it's
  derived output, not authored code. The hand-written SDK files that sit
  alongside it in `pkg/<category>/` (outside `proto/`) are **not** exempt —
  ordinary coverage and lint rules apply to them same as anything in `internal/`.
- Generated code is committed to the repository (not gitignored) so a plugin
  author consuming `pkg/<category>` via the module proxy doesn't need `buf`
  installed at all — but a CI check (once CI exists) diffs freshly-generated
  output against the committed tree to catch drift.
