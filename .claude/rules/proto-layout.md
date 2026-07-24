---
paths:
  - "**/*.proto"
---

# Protobuf file layout

`proto.md` governs syntax, typing, documentation, and versioning for what goes inside a proto file; this rule governs how a package's declarations are split across files within `api/pluggableharness/<category>/v1/`. It exists because `buf.yaml` sets `breaking: use: FILE` (see `proto.md`'s versioning section) — file layout becomes part of the frozen wire contract the moment the first `v*` tag is cut, so the split described here MUST be mechanical and repeatable rather than ad hoc, the same way `go-layout.md` fixes Go package shape before any Go file exists to match a glob against.

## The slot template

Every package directory uses a fixed set of role-named files, never the package-leaf basename:

| File | Holds | Present when |
|---|---|---|
| `service.proto` | The `service` block, nothing else | The package declares a service |
| `rpc_request.proto` | Every message in an rpc's input position, including empty ones (`message DescribeRequest {}`) | The package declares a service |
| `rpc_response.proto` | Every message in an rpc's **unary** return position | The service has at least one unary rpc |
| `events.proto` | Occurrence-shaped messages: anything flowing over a `stream` in either direction, plus oneof event envelopes and their variant payloads | The package has streamed rpcs or a standalone event/payload registry |
| `types.proto` | Domain messages and enums — capabilities, specs, records, refs, taxonomy enums | Almost every package |
| `errors.proto` | The category's `*Error` message and its `*ErrorCategory` enum | The package defines an error taxonomy |

A slot with nothing to hold is not created. A package with no service (e.g. `common`, `render`, `content`) collapses to a single `types.proto` — except a package whose entire purpose is a flat event/payload registry (e.g. `event.v1`), which uses `events.proto` instead of `types.proto` as its one file.

## Assignment is by role, never by name suffix

A message's slot is determined by where it appears on the wire, not by whether its name ends in `Request`/`Response`/`Event`. `pluggableharness.context.v1`'s `Contribute(ContextRequest) returns (ContextContribution)` puts `ContextRequest` in `rpc_request.proto` and `ContextContribution` in `rpc_response.proto` even though neither name carries the expected suffix. A message nested inside a response but never itself returned by an rpc (e.g. a per-item result embedded in a list response) belongs in `types.proto`, not `rpc_response.proto`.

A streamed return goes to `events.proto`, never `rpc_response.proto` — a server-streaming or bidirectional rpc's message flow is occurrence-shaped, not request/response-shaped. This applies uniformly to `stream` on either side of an rpc signature, per `grpc.md`'s streaming-shape table.

## Nested types and grouped declarations travel together

Nested messages, nested enums, and `reserved` statements always move with the message that owns them — they are never separated into a different file than their parent. A `oneof` wrapper and every one of its variant messages stay in the same file as a unit, even when that unit is large; splitting a oneof's variants away from its wrapper (or from each other) is never a valid cut, regardless of resulting file size.

## Exactly one package doc comment per package

`protoc-gen-go` copies the file-level comment block immediately above a `package` statement verbatim into the generated `.pb.go`'s package doc. With multiple files per proto package, only one file may carry that block, or multiple generated Go files end up claiming to be the package doc. The doc-comment file is `service.proto` for a service-bearing package, and the package's single collapsed file (`types.proto` or `events.proto`) for a service-free package — the proto analogue of a Go package's `doc.go`.

Every other file in the package gets a one-line purpose comment placed below its own `option go_package` line, as a comment detached from the `package` statement, so it never attaches to the package doc.

## Intra-package imports form a DAG

protoc requires an explicit `import` for any type referenced from another file, including a sibling file in the same package — there is no implicit same-package visibility as in Go. The intra-package import graph MUST stay acyclic; `buf build` rejects a cycle outright, the same hazard `proto.md` and `docs/specifications/model/data-types.md` already document at the cross-package level. Allowed direction, no back-edges:

```
types.proto ─┬─> errors.proto ─┬─> rpc_response.proto ─┐
             └─────────────────┴─> events.proto ───────┼─> service.proto
                                └─> rpc_request.proto ─┘
```

Each file imports only the specific sibling and cross-package files whose types it actually references — never a broader import for convenience. Cross-package imports name a specific file (e.g. `import "pluggableharness/schema/v1/types.proto";`), never a package as a whole. The existing import-block ordering convention holds per file: the `google/protobuf/*` well-known-types group first, then a single alphabetized `pluggableharness/*` group.

## The layout is frozen at the first release tag

Once `breaking: use: FILE` starts being enforced against a real `v*` tag, a declaration may be added to an existing slot file or to a brand-new file, but an existing declaration may never move from one file to another — that is a file-level break under `FILE` even when the wire format and generated Go API are unchanged. Do not "clean up" a slot file's contents after the first release; if a slot has grown unwieldy, that is a `v2` package decision, not a same-version file reshuffle.
