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
                         pkg/frontend/, pkg/widget/, plus pkg/kernel/ for the
                         kernel-callback service (docs/specifications/kernel-callbacks.md)
    *.go                   hand-written ergonomic SDK: the thin, idiomatic Go
                         surface most plugin authors actually consume
    proto/v1/*.pb.go        buf-generated message + gRPC stubs. Never
                         hand-edited — see proto.md and plugin-runtime.md.
docs/specifications/    protocol contracts (already exists, authoritative)
```

Nothing generated lives at the repo root. `pkg/<category>/` is deliberately
split in two: the `proto/v1/` subtree is 100% derived (`buf generate`
output), while the sibling `.go` files in `pkg/<category>/` are hand-written
and are where most plugin authors actually spend their time — a thin,
idiomatic wrapper over the generated stubs, not the stubs themselves.

`cmd/` binaries MUST stay thin: parse config, construct dependencies via
`internal/` constructors, call `Run`. If a `cmd/` file grows past simple
wiring, the logic belongs in `internal/`.

Package-per-directory: no `internal/util`, `internal/common`, or `internal/helpers`
junk-drawer packages. If code doesn't belong to a specific feature, it belongs
in a narrowly-named package that says what it does.

## Interfaces: the driver pattern

Every pluggable concern (each of the six provider categories, plus internal
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

This applies to internal swappable components. The six *plugin* categories
themselves (model, tool, context, memory, frontend, widget) are out-of-process
via `hashicorp/go-plugin` — see `plugin-runtime.md` — but the kernel-side code
that talks to them (the client stub, the registry, the cache) still follows
this same interface/driver shape internally.

The kernel-side client stub imports the same `pkg/<category>/proto/v1`
generated types (and, where convenient, the `pkg/<category>` SDK wrapper)
that a third-party plugin author imports on the other end of the connection.
There is exactly one Go representation of each wire message — the kernel
does not maintain a second, parallel internal type that gets translated to
and from the generated one.
