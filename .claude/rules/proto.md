---
paths:
  - "**/*.proto"
---

# Protobuf standard

Every plugin category's wire contract is defined here before it's defined in
Go. `api/` is the source of truth for the RPC shape; `pkg/<category>/proto/`
is derived and never hand-edited (see `plugin-runtime.md`).

## Syntax and packaging

- `syntax = "proto3";` always.
- Package per category, per version: `package pluggableharness.<category>.v1;` — `pluggableharness.model.v1`, `pluggableharness.tool.v1`, `pluggableharness.memory.v1`, `pluggableharness.context.v1`, `pluggableharness.frontend.v1`, `pluggableharness.widget.v1`, `pluggableharness.slashcommand.v1`, `pluggableharness.kernel.v1` (the kernel-callback service). A package's files live under `api/pluggableharness/<category>/v1/`, buf's module root; how its declarations are split across files within that directory is `proto-layout.md`'s concern, not this file's.
- `option go_package = "github.com/pluggableharness/agent/pkg/<category>/proto/v1;<category>v1";` on every file — explicit, never inferred, **always** the full module-qualified path (`github.com/pluggableharness/agent/...`). This *is* required, not optional: `protoc-gen-go` embeds `go_package`'s path verbatim into every cross-file Go `import` statement it generates, so any package imported by another proto (which is every shared package in this repo) needs the real importable path or the generated code fails to compile. Getting this backwards — omitting the module prefix on the theory that `out: .` already means repo-root — was an actual bug caught during Wave B integration: it compiled fine for a standalone, never-imported file, then broke the moment a second file imported it, producing `import "pkg/config/proto/v1"` instead of `import "github.com/pluggableharness/agent/pkg/config/proto/v1"`.
- `buf.gen.yaml`'s Go plugins run with `out: .` **and** `opt: module=github.com/pluggableharness/agent`. This `module` option is what reconciles the full-path `go_package` above with landing output at the intended repo-root-relative `pkg/<category>/proto/v1/` instead of a redundant nested `./github.com/pluggableharness/agent/pkg/.../` — it tells `protoc-gen-go` to keep the full path for generated import statements but strip that same prefix when computing where to *write* the file relative to `out`. This is why `api/`'s tree (which mirrors the full dotted package name, `api/pluggableharness/...`) and `pkg/`'s tree (which doesn't) intentionally look different — see `go-layout.md`. Do not drop the `module` opt from `buf.gen.yaml` to "simplify" it — that's the line that makes the split possible.

## Strong typing — no ambiguous types

This is the point of writing these rules down: the wire contract must be as
strongly typed as the Go code that implements it.

- Every `enum` has an explicit `_UNSPECIFIED = 0` zero value. A caller that
  forgets to set an enum field gets a named "unspecified" state, never a
  silently-valid-looking value.
- No `google.protobuf.Any` for anything the spec can name a concrete type
  for. If a field's shape varies by category or plugin, model it as a
  `oneof` of named messages, not `Any` or a `bytes` blob — with **two
  explicit, spec-documented exceptions**, and no third to be added by
  analogy without its own spec-level justification:
  1. The emit/render payload itself.
     `docs/specifications/model/protocol.md` and `docs/specifications/frontend/render-tree.md`
     define the Emit→Render→Paint payload as deliberately opaque (kernel and other
     plugins don't interpret it) — that field stays `bytes`, and only that field.
  2. The event-bus publish payload (`kernel.v1.PublishRequest.payload` /
     `BusEvent.payload`). `docs/specifications/event-bus.md` and
     `docs/specifications/kernel-callbacks.md#publish` define it as opaque for
     the same underlying reason as #1 — a third-party plugin's own event
     shape can't be named by the kernel's proto ahead of time — carried
     alongside `payload_type`/`schema_version` so a *subscriber* can decode
     it even though the kernel itself never does.
- No untyped `map<string, string>` or `map<string, bytes>` standing in for a
  structured payload. A `map<string, string>` is acceptable only for genuine
  open-ended key/value data (e.g. HTTP-style headers, `metric.v1.MetricRecord.attributes`)
  — never as a substitute for a message with named fields.
- **`google.protobuf.Struct` is the sanctioned way to carry a genuinely
  dynamic, per-call-site attribute/value set that a fixed message shape
  can't name in advance** — distinct from the `Any`/untyped-`bytes` ban
  above, which targets a field standing in for a payload the spec *could*
  name concretely but didn't. `Struct` is reached for only when the set of
  keys is inherently open-ended by design, not merely inconvenient to
  enumerate: `log.v1.LogEntry.fields` (mirrors `slog.Attr`'s open key/value
  model), `config.v1`'s `ConfigureRequest.config` and `kernel.v1.GetConfigResult.config`
  (already-decoded `agent.hcl` values, whose shape is the *provider's* schema,
  not the kernel's to name), and `trace.v1.Span`/`SpanEvent`'s `attributes`
  (an OTel span's attribute set, open-ended per call site by the same
  reasoning as `log.v1.LogEntry.fields`) are the precedents. A field whose
  keys are actually fixed and enumerable belongs in a real message instead.
- Every field that has a natural bounded domain (status, kind, risk class,
  error category) is an `enum`, not a `string`. `docs/specifications/tool/conformance.md`'s
  `ToolErrorCategory`, and `docs/specifications/tool/data-types.md`'s `RiskClass`
  and `kind` (`resource`/`data_source`/`interactive`), are enums on the wire, not strings.
- IDs are typed by context (`session_id`, `plugin_version`, `trace_id` as
  distinct `string` fields with a documented format, e.g. ULID) — never a
  single generic `id` field reused across message types.
- `optional` is used explicitly for genuinely-optional scalar fields where
  presence must be distinguishable from zero-value; don't reach for it out of
  habit on fields that are always set.

## Documentation

- Every `message`, every field, every `rpc`, and every `enum` value has a
  `//` comment directly above it. A field comment states the field's meaning
  and, where relevant, its constraint (units, range, "empty means default").
  This mirrors the godoc-on-every-exported-symbol rule in `go-style.md` — the
  proto is the contract, so it gets the same documentation bar as the code
  that implements it.
- Every `service` has a comment naming which `docs/specifications/` document
  it implements, e.g. `// ModelService implements the model provider
  protocol described in docs/specifications/model/protocol.md.`

## Versioning — no breaking changes

- `buf lint` runs clean on every change, using the default + well-known-types
  rule set.
- `buf breaking`, compared against the last released tag, runs clean for any
  `v1` (or later-released) package. A released version's wire contract is
  permanent — this is what makes the "supersedes" replay model in
  `docs/specifications/architecture.md` and `docs/specifications/state-backend.md`
  work: an old session must always be decodable by a client built against a
  newer proto, because the field numbers and types of a released message
  never change.
- A breaking change is shipped as a new package version (`pluggableharness.model.v2`), never as an edit to `v1`. It lands at `api/pluggableharness/model/v2/` and generates into `pkg/model/proto/v2/`, alongside — never replacing — `v1`. The old `v1` service stays defined and generated as long as any retained session was produced by a `v1` plugin.
- Field numbers are never reused, even for removed fields — `reserved N;`
  and `reserved "field_name";` on removal.
