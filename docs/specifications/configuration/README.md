# Configuration — `agent.hcl`

Covers the project-level configuration file (`agent.hcl`), the schema-to-`cty` bridge every provider's `Configure` RPC relies on, the policy DSL, agent profiles, the global user-level config file, and the kernel-written lock file. Unlike [`model/`](../model/README.md), [`tool/`](../tool/README.md), and [`context/`](../context/README.md) (plugin protocols) or [`agent-loop/`](../agent-loop/README.md) (kernel turn behavior), this category is a static wiring layer that reconciles concrete needs surfaced elsewhere: `tool/protocol.md`'s `risk` field, `agent-loop/`'s sub-agent profiles and loop bounds, and `context/`'s per-provider token budgets.

## Scope

`agent.hcl` declares **static wiring**: which provider plugins are loaded, how they're configured, what policy governs their use, and what agent profiles exist. It does **not** author the dynamic per-turn plan/apply diff (a runtime `Plan` structure the frontend renders — see [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)), and it does not declare a static resource graph the way Terraform's `.tf` files do — the agent loop decides tool-call order and content at runtime, not `agent.hcl`.

## File location & loading

The project config is exactly one file, `./agent.hcl` in the project root — deliberately not Terraform's multi-file merge convention (any `*.tf` in a directory loads together); single-file was chosen for a simpler mental model with no merge-order question to define. This means a config that outgrows one file comfortably has no relief valve in v1: no `import`/split mechanism is defined (see [`conformance.md`](conformance.md#open-questions)).

The full set of paths this category reads from and writes to — the global config, the cache, the lock file, session state — is the same XDG layout `architecture.md` already tables; see [`architecture.md#xdg-layout`](../architecture.md#xdg-layout) rather than duplicating it here. In this category's terms:

- `./agent.hcl` (+ nothing else — no sibling `*.hcl` merges) is the project config, decoded by [`blocks-reference.md`](blocks-reference.md).
- `./.agent/agent.lock.hcl` is the kernel-written lock file, never operator-authored — see [`lock-file.md`](lock-file.md).
- `$XDG_CONFIG_HOME/agent/config.hcl` is the global, per-user, never-committed config — see [`settings-and-global.md`](settings-and-global.md).

## The schema-to-`cty` bridge, in brief

Every provider declares a `ConfigSchema` (returned alongside `GetCapabilities`/`GetSchema` — see each category's own `protocol.md`) describing the attributes its `provider "<name>" { ... }` block accepts. The kernel converts that schema into an `hcldec` spec, decodes the matching block body into a `cty.Value` against real HCL2, and marshals the result to the JSON a plugin's `Configure` RPC expects. This is the one bridge between `agent.hcl`'s HCL/`cty` type system and every plugin category's own data-type system — see [`blocks-reference.md`](blocks-reference.md#the-schema-to-cty-bridge) for the full mechanics, including the secrets rule.

## Directory contents

- [`blocks-reference.md`](blocks-reference.md) — `required_providers`, `provider "<name>" { ... }`, `settings { ... }` (including the full `observability{}` sub-block), and the schema-to-`cty` bridge & secrets mechanics.
- [`policy-dsl.md`](policy-dsl.md) — `policy "<name>" { ... }`: match schema, specificity & conflict resolution, evaluation semantics.
- [`agent-profiles.md`](agent-profiles.md) — `agent_profile "<name>" { ... }`: the implicit root profile, model routing, tool scoping, depth budget, loop bounds, explicit hook subscriptions.
- [`settings-and-global.md`](settings-and-global.md) — the rest of `settings{}` (retry defaults, the telemetry switch) and the global config file (`dev_overrides`, `registry_mirror`).
- [`lock-file.md`](lock-file.md) — `.agent/agent.lock.hcl`: shape, version-check posture, checksum verification.
- [`examples.md`](examples.md) — a full worked `agent.hcl`, a worked policy-conflict (and non-conflict) example, and a worked lock file.
- [`conformance.md`](conformance.md) — the MUST/SHOULD/MAY summary matrix and genuinely open questions.
