# Go project layout

Applies to the whole repository once implementation starts. Loaded
unconditionally (no `paths:` scope) because it governs where files go before
any file exists to match a glob against.

## Module shape

This is a plugin-host monorepo, not a single binary. `go.mod` declares module `github.com/pluggableharness/agent`. The top-level layout:

```
cmd/                    thin entrypoints only — flag/env parsing, wiring, then
                         call into internal/. No business logic in cmd/.
  agent/                 the kernel binary
  <plugin>/              one entrypoint per reference plugin (e.g. anthropic/, ripgrep/)
internal/                all real logic. Never imported outside this module.
  <feature>/             one package per bounded concern (see "Interfaces" below)
api/                     .proto sources — buf's module root (see buf.yaml).
  pluggableharness/<category>/v1/*.proto   one directory per category per protocol version
pkg/                     first-class, third-party-consumable Go integration —
                         the only thing a plugin author needs to import.
  <category>/             pkg/model/, pkg/tool/, pkg/context/, pkg/memory/,
                         pkg/frontend/, pkg/widget/, pkg/slashcommand/,
                         pkg/hook/ (the cross-category HookSubscriberService),
                         plus pkg/kernel/ for the kernel-callback service
                         (docs/specifications/kernel-callbacks.md)
    *.go                   hand-written plugin-author SDK: domain types and
                         the author-facing interface(s), converted to/from
                         the generated wire types at the package boundary
                         (see "The pkg/ vs internal/ boundary" below)
    proto/v1/*.pb.go        buf-generated message + gRPC stubs. Never
                         hand-edited — see proto.md and plugin-runtime.md.
  plugin/                 the shared plugin-subprocess serving layer every
                         category SDK builds on — handshake, multi-service
                         muxing, the lazy kernel-callback handle, error
                         helpers. No proto/ subtree of its own.
  render/, config/, schema/, content/   shared, cross-category builder
                         packages (RenderTree nodes, ConfigSchema,
                         the tool/model JSON-Schema subset, ContentBlock)
                         that every category SDK composes rather than
                         reimplementing per category.
  sse/                    shared vendor-neutral plumbing: SSE frame
                         decoding, which every model provider needs and
                         none of them should rewrite. Not a builder and
                         not tied to one category — the test for belonging
                         here is that a third-party plugin author would
                         otherwise copy it out of another provider.
docs/specifications/    protocol contracts (already exists, authoritative)
```

Nothing generated lives at the repo root. `pkg/<category>/` is deliberately
split in two: the `proto/v1/` subtree is 100% derived (`buf generate`
output), while the sibling `.go` files in `pkg/<category>/` are hand-written
and are where most plugin authors actually spend their time — the
plugin-author-facing SDK, not a pass-through to the generated stubs. See
"The pkg/ vs internal/ boundary" below for exactly what that SDK layer is
and isn't allowed to do.

## The pkg/ vs internal/ boundary

`pkg/<category>/`'s hand-written `.go` files (never `proto/v1/`) and
`internal/` sit on opposite sides of a deliberate asymmetry in how many Go
representations a wire message gets:

- **`pkg/<category>/` MAY define its own domain types** — plain Go structs
  and enums shaped for how a plugin author actually thinks about the
  category (e.g. `tool.Call`, `tool.Result`, `model.Spec`), converted
  to/from the generated `pkg/<category>/proto/v1` message at the package
  boundary (conventionally in a `convert.go`). This is a deliberate
  ergonomics choice for the third-party-facing SDK: an author writing a
  plugin should not have to hand-assemble `structpb.Struct` literals or
  navigate a `oneof` wrapper type to implement one RPC. The conversion
  layer is the SDK's job, not the author's.
- **`internal/` MUST consume the same `pkg/<category>/proto/v1` generated
  types the wire actually carries, directly** — never a second, parallel
  internal type that gets translated to and from the generated one. The
  kernel-side client stub (the interface/driver-pattern code described
  above) imports `pkg/<category>/proto/v1` (and, where convenient, the
  `pkg/<category>` SDK wrapper) exactly as a third-party plugin author does
  on the other end of the connection. There is exactly one Go
  representation of each wire message on the kernel side — this is
  unchanged and remains load-bearing for `internal/`.

A `pkg/<category>` domain type is real Go, not a wire type in disguise, but
it stays a *thin* wrapper in spirit: no business logic lives in `convert.go`
beyond validating the invariants the category's own spec states as MUST
(`internal/`'s domain logic — policy, plan/apply, cost — is not
duplicated here). If a `pkg/<category>` type starts accumulating behavior
beyond "shape the RPC ergonomically and validate what the spec requires,"
that behavior belongs in `internal/`, not the SDK.

`cmd/` binaries MUST stay thin: parse config, construct dependencies via
`internal/` constructors, call `Run`. If a `cmd/` file grows past simple
wiring, the logic belongs in `internal/`.

Package-per-directory: no `internal/util`, `internal/common`, or `internal/helpers`
junk-drawer packages. If code doesn't belong to a specific feature, it belongs
in a narrowly-named package that says what it does.

## Interfaces: the driver pattern

Every pluggable concern (each of the seven provider categories, plus internal
swappable backends like the memory store) follows the same shape:

```
internal/<feature>/
  <feature>.go           the interface(s) + shared types, doc.go-style package doc
  drivers/
    <name>/               one implementation per driver
      <name>.go
      <name>_test.go
    drivers.go            selector: name -> constructor, used by cmd/ wiring
```

Rules:

- The interface lives at `internal/<feature>/`, never inside a driver package.
  Driver packages depend on the parent package's interface, not the reverse.
- Each driver is a leaf package under `drivers/<name>/` — no driver imports
  another driver.
- `drivers/drivers.go` is the only place that knows the full set of driver
  names; it exposes a `New(name string, cfg ...) (<Feature>, error)` selector
  (or an explicit registry map) that `cmd/` wiring calls. Nothing else
  switches on driver name.
- A test-only fake driver (e.g. `drivers/fake/`) is expected and encouraged —
  it is what makes the rest of the codebase testable against the interface
  instead of a concrete backend. See `go-testing.md`.

Example: the memory provider's storage abstraction (markdown vs sqlite vs
vector — backend-agnostic by design, see `docs/specifications/memory/README.md`)
is `internal/memory/` (interface) with
`internal/memory/drivers/{markdown,sqlite,vector}/`.

This applies to internal swappable components. The seven *plugin* categories
themselves (model, tool, context, memory, frontend, widget, slashcommand) are
out-of-process via `hashicorp/go-plugin` — see `plugin-runtime.md` — but the kernel-side code
that talks to them (the client stub, the registry, the cache) still follows
this same interface/driver shape internally.

The kernel-side client stub imports the same `pkg/<category>/proto/v1`
generated types (and, where convenient, the `pkg/<category>` SDK wrapper)
that a third-party plugin author imports on the other end of the connection
— see "The pkg/ vs internal/ boundary" above for the full rule and the one
place a second Go representation *is* allowed (the plugin-author-facing
domain types inside `pkg/<category>` itself, never `internal/`).
