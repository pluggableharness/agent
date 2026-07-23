# internal/agentprofile

Sub-agent profile semantics from `specifications/configuration.md` §8:
depth-budget inheritance, capability-aware model fallback, and tool-scoping
resolution for `agent_profile{}` blocks.

## What this package does

- `types.go` — `ModelRef`, `ModelBlock`, `AgentProfile`.
- `depth.go` — `RootRemainingDepth` and `ChildRemainingDepth`, the two
  distinct formulas from §8.4's inherited, only-shrinking depth budget.
- `model.go` — `SelectModel`, walking a profile's primary-then-fallback
  chain against caller-supplied `ModelSpec` capabilities to find the first
  eligible model for a turn's actual requirements (§8.2, `provider.md` §9).
- `tools.go` — `ResolveTools`, expanding a profile's flat tool-scoping list
  (including `"<provider>.*"` wildcards) into a concrete allowed set,
  honoring §8.3's strict default (an omitted `tools` list grants nothing).

## How it fits in

`internal/config` decodes `agent_profile{}` blocks into this package's
`AgentProfile` type. This package owns none of the HCL parsing and none of
the actual provider communication — `SelectModel` and `ResolveTools` both
take caller-supplied capability data rather than fetching it themselves.
