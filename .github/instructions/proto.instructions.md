---
applyTo: "**/*.proto"
---

# Protobuf conventions

The full rules live in `.claude/rules/proto.md`, `.claude/rules/proto-layout.md`, and `plugin-runtime.md`.

- `proto3` syntax; package `pluggableharness.<category>.v1`; `go_package` is fully module-qualified and matched by a `module=` opt in `buf.gen.yaml`.
- A package's declarations are split across role-named files (`service.proto`, `rpc_request.proto`, `rpc_response.proto`, `events.proto`, `types.proto`, `errors.proto`) per `.claude/rules/proto-layout.md`'s slot template — never one file per package leaf, and never a name-suffix guess at which slot a message belongs in.
- Strong typing throughout: every enum has a `<NAME>_UNSPECIFIED = 0` value; no `google.protobuf.Any` and no loose string maps — the opaque frontend render payload is the one deliberate carve-out; bounded domains are enums, identifiers are typed messages.
- Every message, field, rpc, and enum carries a doc comment.
- `buf lint` and `buf breaking` must pass. Wire-breaking changes never mutate `v1` in place — they ship as a new `vN` package, and removed field numbers are `reserved`.
- After any `.proto` change: `buf format -w`, then regenerate with `GOBIN=$PWD/bin go install tool && PATH=$PWD/bin:$PATH buf generate`, and commit the regenerated `pkg/*/proto/v1/` output — CI diffs for drift. Generated files are never edited by hand.
