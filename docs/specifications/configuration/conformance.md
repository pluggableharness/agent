# Conformance

## Required vs. optional — summary matrix

| Element | Level | See |
|---|---|---|
| Single-file `agent.hcl` at project root | MUST | [`README.md`](README.md#file-location--loading) |
| Global config never contains provider auth | MUST | [`settings-and-global.md`](settings-and-global.md#global-config--xdg_config_homeagentconfighcl) |
| `required_providers`/`provider`/`policy`/`agent_profile`/`hook`/`settings` as the only top-level blocks | MUST | [`blocks-reference.md`](blocks-reference.md) |
| Schema-to-`cty` bridge via `ConfigSchema` | MUST | [`blocks-reference.md`](blocks-reference.md#the-schema-to-cty-bridge) |
| `sensitive` attributes reject literal expressions | MUST | [`blocks-reference.md`](blocks-reference.md#secrets-sensitive-and-env) |
| `env()` fails fast on unset variable | MUST | [`blocks-reference.md`](blocks-reference.md#secrets-sensitive-and-env) |
| `source`/`version` constraint syntax matching Terraform's operators | MUST | [`blocks-reference.md`](blocks-reference.md#required_providers) |
| Provider aliasing (multiple instances per entry) | Not supported (v1), confirmed | [`blocks-reference.md`](blocks-reference.md#required_providers) |
| `token_budget` as reserved convention field | MUST, where applicable | [`blocks-reference.md`](blocks-reference.md) |
| Most-specific-wins policy conflict resolution | MUST | [`policy-dsl.md`](policy-dsl.md#conflict-detection) |
| Identical-specificity-and-value policy conflict | MUST be config-load-time error | [`policy-dsl.md`](policy-dsl.md#conflict-detection) |
| Policy evaluation covers `data_source` and `interactive` calls (allow/deny only) | MUST | [`policy-dsl.md`](policy-dsl.md#evaluation-semantics) |
| `ask` on a `data_source`/`interactive` match downgrades to `deny` with a logged warning | MUST | [`policy-dsl.md`](policy-dsl.md#evaluation-semantics) |
| `PolicyMatch.kind` restricted to `resource`/`data_source` (no `interactive` value) | Documented v1 limitation | [`policy-dsl.md`](policy-dsl.md#match-schema) |
| Implicit root profile named `default` | MUST | [`agent-profiles.md`](agent-profiles.md#the-implicit-root-profile) |
| Structured `model` block (primary + fallback) | MUST | [`agent-profiles.md`](agent-profiles.md#model-routing) |
| Capability-aware fallback eligibility | MUST | [`agent-profiles.md`](agent-profiles.md#model-routing) |
| Strict-default (empty) tool scoping when `tools` omitted | MUST | [`agent-profiles.md`](agent-profiles.md#tool-scoping) |
| Strict-default (empty) `slash_commands` scoping when omitted | MUST | [`agent-profiles.md`](agent-profiles.md#tool-scoping) |
| Inherited, only-shrinking `max_depth` budget | MUST | [`agent-profiles.md`](agent-profiles.md#depth-budget) |
| Loop bounds as `agent_profile` fields, no separate `session` block | MUST | [`agent-profiles.md`](agent-profiles.md#loop-bounds) |
| Explicit `hook` block for non-default subscriptions | MAY | [`agent-profiles.md`](agent-profiles.md#explicit-hook-subscriptions) |
| Textual-position ordering across implicit + explicit hook subscriptions | MUST | [`agent-profiles.md`](agent-profiles.md#explicit-hook-subscriptions) |
| `settings{}` block, including `retry{}` canonical defaults | MUST (block exists); values operator-overridable | [`blocks-reference.md`](blocks-reference.md#settings) / [`settings-and-global.md`](settings-and-global.md#retry-defaults) |
| `observability{}` all-or-nothing once declared | MUST | [`blocks-reference.md`](blocks-reference.md#observability) |
| `dev_overrides` in global config | MUST (mechanism); SHOULD (used) | [`settings-and-global.md`](settings-and-global.md#dev_overrides) |
| `registry_mirror` with per-prefix `mirror{}` blocks | MAY | [`settings-and-global.md`](settings-and-global.md#registry_mirror) |
| `registry_mirror`/`mirror.auth` forbids literal secrets | MUST | [`settings-and-global.md`](settings-and-global.md#registry_mirror) |
| Kernel-written lock file, `lock_file_version` checked on open | MUST | [`lock-file.md`](lock-file.md) |
| Lock file checksums cover every installed platform, not just the invoking one | MUST | [`lock-file.md`](lock-file.md) |
| Checksum re-verified on every install, not just first resolution | MUST | [`lock-file.md`](lock-file.md) |

## Open questions

- **Provider aliasing / multiple instances is not supported in v1**: running the same `required_providers` entry twice with different config (e.g. two `filesystem` roots with different permissions) is out of scope. The real syntax design this would need (naming collisions, disambiguated tool-reference syntax) is deliberately deferred. This interacts with the memory provider's `MemoryScope`, which resolves the specific cross-scope-memory case that aliasing would otherwise have been needed for — see [`../memory/README.md`](../memory/README.md).
- **No `import`/split mechanism** remains genuinely live — a direct consequence of the single-file decision (see [`README.md#file-location--loading`](README.md#file-location--loading)): large configs have no relief valve if they outgrow one file comfortably. Not raised as a problem in practice yet, but worth watching as real `agent.hcl` files grow.
