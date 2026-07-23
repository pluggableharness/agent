# Examples

## A full worked `agent.hcl`

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
  ripgrep = {
    source  = "github.com/agentco/provider-ripgrep"
    version = "~> 0.4"
  }
}

provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}

provider "filesystem" {
  roots = ["."]
}

provider "ripgrep" {}

policy "auto_approve_reads" {
  match  = { kind = "data_source" }
  action = "allow"
}

policy "gate_filesystem_writes" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "ask"
}

policy "block_high_risk" {
  match  = { risk = "critical" }
  action = "deny"
}

agent_profile "default" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-opus-4-8"
    }
    fallback {
      provider = "anthropic"
      id       = "claude-sonnet-5"
    }
  }

  tools = [
    "filesystem.*",
    "ripgrep.*",
  ]

  max_turns        = 200
  max_cost_usd     = 5.00
  max_wall_clock_s = 3600
}

agent_profile "code-reviewer" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-sonnet-5"
    }
    fallback {
      provider = "anthropic"
      id       = "claude-haiku-4-5"
    }
  }

  tools = [
    "filesystem.read_file",
    "ripgrep.*",
  ]

  slash_commands = ["compact"]

  max_depth                = 1
  max_concurrent_subagents = 4
  max_turns                = 40
}

settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true

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
    resource_attrs     = { env = "dev" }
  }
}
```

This exercises every top-level block type in one file — see [`blocks-reference.md`](blocks-reference.md) for `required_providers`, `provider`, and `settings`; [`policy-dsl.md`](policy-dsl.md) for `policy`; and [`agent-profiles.md`](agent-profiles.md) for `agent_profile`. Note the `model{}` sub-blocks are written multi-line — `primary { provider = "x", id = "y" }` would be a parse error; see [`blocks-reference.md#hcl-single-line-blocks-take-only-one-argument`](blocks-reference.md#hcl-single-line-blocks-take-only-one-argument).

## A worked policy-conflict example

```hcl
policy "gate_writes" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "ask"
}

policy "gate_network_writes" {
  match  = { provider = "http-client", kind = "resource" }
  action = "deny"
}
```

Both rules specify exactly two `match` fields (`provider`, `kind`), so both have the identical specificity tuple `(false, true, false, true)` (`provider` and `kind` set; `tool_name` and `risk` not). Under the conflict rule (see [`policy-dsl.md#conflict-detection`](policy-dsl.md#conflict-detection)), that alone is not enough to flag a conflict — the check also compares every field both rules specify. Here, `kind` agrees (`resource` on both) but `provider` disagrees (`"filesystem"` vs. `"http-client"`) — a field both rules specify, with different values — so the rules do not conflict: a real call has exactly one `provider`, so these two rules can never both match the same call. This configuration is accepted.

### A non-conflicting, same-tuple pair

The case the corrected rule exists specifically to allow through:

```hcl
policy "allow_read_file" {
  match  = { tool_name = "read_file" }
  action = "allow"
}

policy "deny_write_file" {
  match  = { tool_name = "write_file" }
  action = "deny"
}
```

Both specify only `tool_name`, so both share specificity tuple `(true, false, false, false)`. Specificity-tuple equality alone does not make these two rules conflict: the one field both rules specify, `tool_name`, holds *different* values (`"read_file"` vs. `"write_file"`) — a real call can only ever have one `tool_name`, so these two rules can never both match the same call, and are not in conflict. Contrast this with an actual conflict:

```hcl
policy "a" {
  match  = { tool_name = "read_file" }
  action = "allow"
}

policy "b" {
  match  = { tool_name = "read_file" }
  action = "deny"
}
```

Same tuple, and the one shared field (`tool_name`) holds the *same* value (`"read_file"`) with disagreeing actions — this configuration MUST be rejected as a conflict, naming both `"a"` and `"b"`.

## A worked lock file

```hcl
lock_file_version = 1

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums = {
    "linux_amd64"  = "sha256:1a2b3c4d5e6f7089abcdef0123456789abcdef0123456789abcdef012345678"
    "linux_arm64"  = "sha256:4d5e6f7089abcdef0123456789abcdef0123456789abcdef0123456789abcd12"
    "darwin_amd64" = "sha256:7a8b9c0123456789abcdef0123456789abcdef0123456789abcdef012345678"
    "darwin_arm64" = "sha256:0d1e2f3456789abcdef0123456789abcdef0123456789abcdef01234567890a"
  }
}

provider "filesystem" {
  source      = "github.com/agentco/provider-filesystem"
  version     = "1.0.2"
  resolved_at = "2026-07-20T09:12:00Z"
  checksums = {
    "linux_amd64"  = "sha256:9f8e7d6c5b4a3928170695a4b3c2d1e0f9e8d7c6b5a4938271605f4e3d2c1b0"
    "linux_arm64"  = "sha256:2c1b0a9f8e7d6c5b4a3928170695a4b3c2d1e0f9e8d7c6b5a49382716059f8e"
    "darwin_amd64" = "sha256:5b4a3928170695a4b3c2d1e0f9e8d7c6b5a4938271605f4e3d2c1b09f8e7d6c"
    "darwin_arm64" = "sha256:8271605f4e3d2c1b09f8e7d6c5b4a3928170695a4b3c2d1e0f9e8d7c6b5a493"
  }
}
```

See [`lock-file.md`](lock-file.md) for the version pre-check and checksum verification this file's loading and use involves.
