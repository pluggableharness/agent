# internal/policy

The policy engine described in `specifications/configuration.md` §7:
matching, specificity-based conflict resolution, and evaluation for
`policy{}` blocks.

## What this package does

- `types.go` — `Match` (the 4-field match criteria: tool_name, provider,
  risk, kind), `Action` (allow/ask/deny), `Rule` (a named policy).
- `specificity.go` — the specificity-tuple ordering
  (`tool_name > provider > risk > kind`) and conflict detection between two
  rules.
- `evaluate.go` — `Evaluate`, which resolves a real call against a rule set
  per `configuration.md` §7.3's algorithm, extended to cover `interactive`
  calls per `agent-loop.md` §5.4bis.

## How it fits in

`internal/config` decodes `policy{}` blocks from `agent.hcl` into this
package's `Rule` type and calls `ValidateRules` before a load succeeds.
Whatever eventually dispatches tool calls at runtime calls `Evaluate`
against the loaded rule set — that composition doesn't exist yet.
