# Settings (continued) & global config

This document covers the parts of `settings{}` not already detailed in [`blocks-reference.md#settings`](blocks-reference.md#settings) (which owns the block's overall shape and the full `observability{}` sub-block), plus the global, per-user config file.

## Retry defaults

```hcl
settings {
  retry {
    base_delay_ms  = 500
    backoff_factor = 2
    max_retries    = 5
  }
}
```

`retry{}` holds the kernel's canonical backoff configuration — the values [`../agent-loop/turn-algorithm.md`](../agent-loop/turn-algorithm.md) uses when retrying a `rate_limited`/`overloaded` model-provider error (see [`../model/conformance.md`](../model/conformance.md#error-taxonomy)). It is operator-overridable but ships with sensible defaults so a bare `agent.hcl` works without tuning before first use: `base_delay_ms = 500`, `backoff_factor = 2`, `max_retries = 5`. Like `observability{}`, this sub-block is **all-or-nothing** once declared at all — every one of its three attributes is required; there is no partially-specified `retry{}`.

## The `telemetry` switch

`telemetry` is the master on/off switch for the kernel's OTel-native tracing, metrics, and logs module. When `false`, the kernel MUST wire a discarding backend regardless of `observability{}`'s contents — no exporter is ever constructed, even if an `observability{}` sub-block is present. When `true` (or when `observability{}` is present at all, since its presence implies intent), the nested `observability{}` sub-block controls the OTel specifics, defaulting to OTLP/gRPC against a local collector, full sampling, and all three signals on when the sub-block itself is entirely absent. See [`blocks-reference.md#observability`](blocks-reference.md#observability) for the full field reference.

## Global config — `$XDG_CONFIG_HOME/agent/config.hcl`

The global, per-user, never-committed config file — see [`../architecture.md#xdg-layout`](../architecture.md#xdg-layout) for where it sits among the other XDG paths. It holds exactly two blocks and **MUST NOT** contain project-specific provider configuration — no auth lives here, ever; auth lives in `agent.hcl` via `env(...)` indirection (see [`blocks-reference.md#secrets-sensitive-and-env`](blocks-reference.md#secrets-sensitive-and-env)), because environment variables are inherently host-scoped already — a second, separate credential-storage layer would just be redundant.

```hcl
dev_overrides {
  anthropic = "/home/steven/code/provider-anthropic/provider-anthropic"
}

registry_mirror {
  default = "https://registry.internal.example.com"

  mirror {
    prefix = "github.com/agentco/"
    url    = "https://registry.internal.example.com/agentco"
    auth   = env("AGENTCO_MIRROR_TOKEN")
  }

  mirror {
    prefix = "github.com/some-private-org/"
    url    = "https://registry.internal.example.com/private"
    auth   = env("PRIVATE_MIRROR_TOKEN")
  }
}
```

### `dev_overrides`

Maps a `required_providers` local name to a local binary path; when present for a given name, the kernel MUST use that binary directly instead of resolving through the registry/version-constraint machinery — mirrors Terraform's `dev_overrides` mechanism exactly. Local names accepted here are whatever the project's `required_providers` declare — the global config file is decoded independently and has no visibility into which names are actually valid for a given project.

### `registry_mirror`

`default` is the fallback URL for any `source` prefix not otherwise matched. Zero or more `mirror { }` blocks each redirect a specific `source` prefix (matched by **longest-prefix-wins** — the same specificity philosophy the policy engine uses, see [`policy-dsl.md#conflict-detection`](policy-dsl.md#conflict-detection)) to a different URL, optionally with its own `auth` — supporting the mixed public/private registry sourcing a team pulling some providers from the public git-forge registry and others from an internal mirror actually needs. `auth`, like every other credential in this document, MUST be an `env(...)` expression — literal tokens are forbidden here exactly as they are in a `provider { }` block.

A mirror's `auth` field is optional — a mirror with no `auth` is legal, and is only validated when present, using the same secret-handling mechanism [`blocks-reference.md`](blocks-reference.md#secrets-sensitive-and-env) describes for a provider's sensitive attributes.

Native HCL `{ "key" = "value" }` object-constructor syntax evaluates to an Object type, not necessarily a Map — decoding of map-shaped attributes such as `checksums` (see [`lock-file.md`](lock-file.md)) accounts for this.

Loading the global config file logs only the file path, at `DEBUG` level — never a decoded value, and never the `auth` field's resolved contents.
