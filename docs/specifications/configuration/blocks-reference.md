# Blocks reference

`agent.hcl` MUST consist only of these top-level block types — any other block label is a config-load-time error:

| Block | Purpose |
|---|---|
| `required_providers { ... }` | Which provider plugins this project uses, and version constraints |
| `provider "<name>" { ... }` | Instance configuration for a required provider |
| `policy "<name>" { ... }` | A named policy rule — see [`policy-dsl.md`](policy-dsl.md) |
| `agent_profile "<name>" { ... }` | A named session/sub-agent profile — see [`agent-profiles.md`](agent-profiles.md) |
| `hook "<point>" { ... }` | An explicit hook subscription for cross-cutting subscribers — see [`agent-profiles.md#explicit-hook-subscriptions`](agent-profiles.md#explicit-hook-subscriptions) |
| `settings { ... }` | Cross-cutting, non-provider-specific global options |

This is enforced as a closed schema against exactly these six block types — an unrecognized top-level block MUST be a decode-time error, not a silently-ignored extra.

## `required_providers`

```hcl
required_providers {
  anthropic = {
    source  = "github.com/agentco/provider-anthropic"
    version = "~> 1.2.3"
  }
  filesystem = {
    source  = "github.com/agentco/provider-filesystem"
    version = "~> 1.0"
  }
}
```

- `source` MUST be a git-forge address (`github.com/...` or `gitlab.com/...`) — see [`architecture.md#registry--distribution`](../architecture.md#registry--distribution).
- `version` MUST use the same constraint operators as Terraform: `=`, `!=`, `>`, `>=`, `<`, `<=`, `~>`.
- The block's local name (`anthropic`, `filesystem` above) is what `provider` blocks and an `agent_profile`'s `model`/`tools` reference. It need not match the plugin's own advertised name.
- A provider's **category** (model/tool/context/memory/frontend/widget) is never declared here — the kernel discovers it after loading the plugin, from its own `GetCapabilities`/`GetSchema` response.
- v1 supports exactly **one instance per `required_providers` entry** — there is no Terraform-style `alias` mechanism for running the same plugin twice with different config (e.g. two `filesystem` roots with different permissions). This is a confirmed v1 limitation, not an oversight — see [`conformance.md#open-questions`](conformance.md#open-questions).

Local names are whatever the operator chooses; each attribute's value MUST evaluate to an object or map carrying `source` and `version`.

## `provider "<name>" { ... }`

```hcl
provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}

provider "claude-md-reader" {
  token_budget = 4000
  paths        = ["CLAUDE.md", "**/CLAUDE.md"]
}
```

- Body fields are entirely provider-specific, decoded per the schema-to-`cty` bridge below — this document places no fixed shape on them beyond the secrets rule.
- **Reserved convention name:** `token_budget` (integer). A context or memory provider's token cap is not a separate mechanism — it's this ordinary, reserved-by-convention field in the provider's own config, decoded the same way as any other attribute. See [`context/data-types.md#budget-mechanics`](../context/data-types.md#budget-mechanics). A provider outside those categories simply doesn't declare `token_budget` in its schema, and the field is absent for it.
- `Configure` MUST reject with a structured error on missing required fields or an unresolvable `env(...)`, rather than deferring failure to first use.

A `provider{}` block's body is not decoded when `agent.hcl` is first loaded. A `ConfigSchema` only exists once the named plugin's subprocess is running and has answered `GetCapabilities`/`GetSchema`, so there is nothing to decode against at load time — a genuine chicken-and-egg constraint. The body is decoded later, once a schema is available — see the schema-to-`cty` bridge below.

### HCL single-line blocks take only one argument

```hcl
# Parse error — a single-line block body may hold only one argument.
primary { provider = "anthropic", id = "claude-opus-4-8" }

# Correct — multi-line.
primary {
  provider = "anthropic"
  id       = "claude-opus-4-8"
}
```

This is a general HCL syntax rule, not a project-specific convention. Any block with more than one attribute — such as the `primary`/`fallback` sub-blocks in a `model{}` block (see [`agent-profiles.md#model-routing`](agent-profiles.md#model-routing)) — MUST be written multi-line.

## The schema-to-`cty` bridge

```protobuf
ConfigSchema {
  attributes  []ConfigAttribute
}

ConfigAttribute {
  name               string
  type               enum { string, number, bool, list_string, list_number, map_string, object }
                      // a deliberately small subset of cty's type system
  required           bool
  sensitive          bool               // MUST — see "Secrets" below
  description        string
  object_attributes  []ConfigAttribute  // MUST be set (non-empty) iff type == object;
                                         // MUST be empty for every other type — see
                                         // "Nested object attributes" below
  default_json       string?            // MAY — a JSON-encoded default applied when this
                                         // attribute is optional and agent.hcl omits it —
                                         // see "Declared defaults" below
}
```

The kernel converts a `ConfigSchema` into an `hcldec` spec, decodes the matching `provider` block body into a `cty.Value` against it — resolving any `env(...)` calls during decoding — and marshals the result to the wire format `Configure` expects (JSON, carried as a `google.protobuf.Struct` on the wire).

### Nested object attributes

An attribute of type `object` carries its own nested schema in `object_attributes`, structurally identical to a top-level `ConfigSchema.attributes` list — each nested `ConfigAttribute` gets the same `required`/`sensitive`/`description` treatment the schema-to-cty bridge already applies at the top level, rather than accepting an unvalidated dynamic object. One level of nesting is the common case; deeper nesting is expressed the same way recursively — an entry in `object_attributes` MAY itself be `type == object` with its own populated `object_attributes`, with no depth cap enforced by the wire type itself. A provider author should still keep nesting shallow in practice: this schema exists to be validated and documented, and a deeply nested config block defeats both purposes.

`sensitive` and the `env(...)` shape-validation rule (see "Secrets" below) apply identically to a nested attribute — a secret buried inside an `object`-typed attribute is validated exactly as if it were a top-level attribute of the same type.

### Declared defaults

`default_json` supplies the value the schema-to-cty bridge uses when a `required = false` attribute's corresponding HCL expression is absent from the `provider` block body entirely. Absent `default_json` means "no default" — an omitted optional attribute decodes to that type's cty zero value (empty string, `0`, `false`, an empty list/map), exactly as it did before this field existed.

`default_json` is a JSON-encoded string rather than a typed field per `AttrType` (or a `google.protobuf.Struct`/`Value`) so the wire type stays cty-agnostic: the kernel's schema-to-cty bridge is the only thing that interprets it, parsing the JSON and converting the result to the `cty.Value` the attribute's declared `type` expects. Encoding rule: the JSON value's shape MUST match `type` under the same mapping the bridge already uses when decoding an actual HCL-supplied value (a JSON string for `string`, a JSON number for `number`, a JSON array of strings for `list_string`, a JSON object for `object` — matching that attribute's own `object_attributes` shape, recursively for nested `object`-typed defaults). A `default_json` that doesn't parse as JSON, or that parses but doesn't match `type`'s expected shape, MUST be rejected as a config-load-time error against the provider's own advertised schema — the same "misconfiguration is a load-time error" posture this document applies everywhere else.

`default_json` MUST NOT be set on an attribute with `sensitive = true` — a declared default is a literal value baked into the schema advertisement itself, which is exactly the literal-secret-value case "Secrets" below forbids regardless of where the literal appears.

## Secrets: `sensitive` and `env(...)`

Secrets are enforced at the **expression level**, not the value level — literal secret values are forbidden outright, not merely discouraged:

- For any attribute where `sensitive = true`, the corresponding expression in the `agent.hcl` body MUST be **exactly** a call to the built-in `env(name)` function — no string interpolation, no concatenation, no default-fallback expression wrapping it. This MUST be checked against the **unevaluated HCL expression's syntax**, not the resulting `cty.Value` — once evaluated to a plain string there is no way to tell whether it came from a literal or an env lookup, so enforcement has to happen before evaluation.
- A `sensitive = true` attribute whose expression is anything else (a literal string, a more complex expression) MUST be rejected as a config-load-time error, not a warning.
- `env(name)` MUST fail config-load fast (not defer to `Configure` time) if the named environment variable is unset. A silently-empty string reaching `Configure` is indistinguishable from "intentionally blank," which is the wrong failure mode for a missing credential.
- A plugin's own `Configure` MUST NOT echo a received sensitive value into any `Emit`'d event, `Render` output, log line, or error message — this is already required by every plugin category's own `protocol.md`; restated here because this is where the *schema* declares which fields are sensitive in the first place.

The same secret-handling mechanism is shared by every place `agent.hcl` (and the global config file) can carry a credential:

- Shape validation checks the expression's *shape* only, before anything is evaluated: the expression MUST be a call to `env` with exactly one argument, and that argument MUST itself be a literal string. This is a deliberately conservative reading of "exactly `env(name)`" — it rejects `env(some_var)` (a variable reference) even though that might look more flexible, because loosening it would reopen the exact expression complexity the spec forbids.
- The `env(name)` function itself enforces the fail-fast-if-unset behavior, but only when the expression is actually *evaluated* during decoding — not at the earlier, syntax-only validation stage.

Shape validation and fail-fast-if-unset are deliberately split into two separate stages rather than combined into one pass: checking whether the named variable is actually set would require evaluating the expression, which would defeat the point of validating its shape *before* evaluation. Validation therefore runs against the raw, undecoded body, before the schema-driven decode described above takes place.

The same secret-handling mechanism applies to `registry_mirror.mirror.auth` in the global config file (see [`settings-and-global.md#registry_mirror`](settings-and-global.md#registry_mirror)) — one mechanism, applied uniformly wherever a credential can appear.

The decode and secret-resolution path is deliberately unlogged: because `env(...)` resolution handles actual secret values in memory, even an entry/exit log line on that path is one careless future change away from interpolating a resolved credential into a log record.

## `settings{}`

```hcl
settings {
  default_frontend = "tui"
  log_level        = "info"      // trace | debug | info | warn | error
  telemetry        = false

  retry {
    base_delay_ms  = 500
    backoff_factor = 2
    max_retries    = 5
  }

  observability {
    endpoint           = "localhost:4317"
    protocol           = "grpc"
    sampling_ratio     = 1.0
    traces_enabled     = true
    metrics_enabled    = true
    logs_enabled       = true
    export_interval_ms = 10000
    service_name       = "pluggableharness-agent"
    resource_attrs     = { env = "prod" }
  }
}
```

`settings{}` is the home for cross-cutting, non-provider-specific, non-policy-shaped options — `default_frontend` names which `required_providers` entry the CLI attaches when more than one frontend provider is loaded, and `log_level` is exactly what it says. This block is intentionally small: a field that's really provider-specific belongs in that provider's own `provider{}` block, and a field that's really about approval/blocking behavior belongs in `policy{}`. `retry{}`'s canonical backoff defaults and `telemetry`'s master switch are covered in [`settings-and-global.md`](settings-and-global.md); the rest of this section covers `observability{}`.

### `observability{}`

The current, full shape of the OTel-specific sub-block that controls the kernel's tracing/metrics/logs export once `telemetry` is on (or once `observability{}` is present at all, since its presence implies intent):

| Field | Type | Required | Meaning |
|---|---|---|---|
| `endpoint` | string | yes | OTLP collector address, e.g. `"localhost:4317"` |
| `protocol` | string (`grpc` \| `http`) | yes | OTLP transport |
| `sampling_ratio` | number, 0–1 | yes | `ParentBased(TraceIDRatioBased)` sampler ratio |
| `traces_enabled` | bool | yes | gates the traces signal independently of `telemetry` and the other two signals |
| `metrics_enabled` | bool | yes | gates the metrics signal independently |
| `logs_enabled` | bool | yes | gates the logs signal independently |
| `export_interval_ms` | number | yes | metrics/logs `PeriodicReader` push cadence |
| `service_name` | string | yes | populates the OTel `Resource`'s `service.name` |
| `resource_attrs` | map(string) | **no** | extra static resource attributes, e.g. `{ env = "prod" }` |

`resource_attrs` is the **one** optional field in the sub-block — every other field is `Required: true`, matching this block's existing all-or- nothing convention for `retry{}` (see [`settings-and-global.md#retry-defaults`](settings-and-global.md#retry-defaults)). When `telemetry = false`, the kernel MUST wire a discarding backend regardless of `observability{}`'s contents — no exporter is ever constructed. `traces_enabled`/`metrics_enabled`/`logs_enabled` let an operator run with, say, metrics and logs on but tracing off.

`retry{}` and `observability{}` each receive their canonical defaults even when the enclosing `settings{}` block is entirely absent from `agent.hcl`, not only when `settings{}` is present but a specific sub-block is omitted — a config with no `settings{}` block at all still ends up with fully defaulted retry and observability behavior.
