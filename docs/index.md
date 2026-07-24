---
hide:
  - navigation
  - toc
---

# PluggableHarness Agent

<div class="ph-hero" markdown>

The AI coding harness you never have to fork. A small Go microkernel owns plugin lifecycle, the plan/apply policy gate, configuration, and the session log — everything opinionated lives in out-of-process gRPC plugins.

</div>

> [!NOTE]
> New here? Read in order: [conventions](specifications/conventions.md) (how these documents are written and cross-referenced), then the [glossary](specifications/glossary.md), then the [architecture](specifications/architecture.md) narrative. Everything else branches from there.

## Specifications

The authoritative protocol contracts. RFC 2119 keywords are load-bearing; where anything else disagrees with these documents, these documents win.

<div class="ph-grid" markdown>

<div markdown>
[Architecture](specifications/architecture.md)

Microkernel philosophy, the six plugin categories, Emit → Render → Paint, transport, registry, policy.
</div>

<div markdown>
[Agent loop](specifications/agent-loop/README.md)

The kernel's turn algorithm, hook dispatch, plan/apply gate, sub-agents, and error recovery.
</div>

<div markdown>
[Configuration](specifications/configuration/README.md)

`agent.hcl`, the policy DSL, agent profiles, settings and global config, the lock file.
</div>

<div markdown>
[Model provider](specifications/model/README.md)

The LLM vendor plugin protocol — capabilities, streaming completion, token counting, pricing.
</div>

<div markdown>
[Tool provider](specifications/tool/README.md)

Tool plugins: resource, data_source, and interactive kinds, risk classes, and the reference catalog.
</div>

<div markdown>
[Context provider](specifications/context/README.md)

Pre-model-call prompt injection: protocol, data types, and conformance.
</div>

<div markdown>
[Memory provider](specifications/memory/README.md)

Cross-session persistence and recall, plus the record taxonomy.
</div>

<div markdown>
[Frontend & widget](specifications/frontend/README.md)

Frontend and widget plugin protocols and the shared render tree IR.
</div>

<div markdown>
[Kernel callbacks](specifications/kernel-callbacks.md)

The plugin → kernel direction: `RunSession`, `CountTokens`, `Emit`, `Log`. See also the [state backend](specifications/state-backend.md).
</div>

</div>

## First-party catalog

Descriptive reference reports on the providers and tool capabilities the project ships or studies first-party — not protocol specs.

<div class="ph-grid" markdown>

<div markdown>
[Model providers](first-party/providers/README.md)

Sourced capability data for Anthropic, OpenAI, Google, and xAI: rosters, reasoning, caching, wire formats.
</div>

<div markdown>
[Tools](first-party/tools/README.md)

Twenty-two capability reports, from file I/O and search through browser automation, MCP, and sub-agent spawning.
</div>

</div>

## Sixty-second `agent.hcl`

One file declares the whole harness — providers are versioned plugins, and policy gates every tool call:

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

provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}

provider "filesystem" {
  roots = ["."]
}

policy "auto_approve_reads" {
  match  = { kind = "data_source" }
  action = "allow"
}

policy "gate_filesystem_writes" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "ask"
}

agent_profile "default" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-opus-4-8"
    }
  }

  tools = ["filesystem.*"]
}
```

The full worked example, including fallback models and every block, lives in [configuration examples](specifications/configuration/examples.md).
