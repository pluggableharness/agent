package agentprofile

// ModelRef identifies one candidate model within a model{} block's primary
// or fallback sub-blocks: an explicit {provider, id} pair, resolved against
// the required_providers/provider blocks elsewhere in agent.hcl
// (configuration.md §8.2, §5, §6).
type ModelRef struct {
	// Provider is the provider block's declared name (not the vendor's
	// model-family name) that this ref resolves against.
	Provider string
	// ID is the vendor's exact model identifier, matching ModelSpec.Id
	// (pkg/provider/proto/v1) for the resolved provider.
	ID string
}

// ModelBlock is an agent_profile's model{} block: one required primary
// candidate and zero or more fallback candidates, tried in declared order
// subject to capability-aware eligibility (configuration.md §8.2).
type ModelBlock struct {
	// Primary is the first candidate SelectModel tries.
	Primary ModelRef
	// Fallbacks are tried in slice order after Primary, each subject to the
	// same eligibility check.
	Fallbacks []ModelRef
}

// AgentProfile is the decoded form of one agent_profile "<name>" { ... }
// block (configuration.md §8).
type AgentProfile struct {
	// Name is the profile's declared label, e.g. "default" or
	// "code-reviewer" (configuration.md §8, §8.1).
	Name string

	// Model is the profile's model{} block — primary plus fallback
	// candidates (configuration.md §8.2).
	Model ModelBlock

	// Tools is the flat tool-scoping list: concrete
	// "<provider>.<tool_name>" entries and/or "<provider>.*" wildcards. A
	// nil or empty Tools resolves to no tools at all — configuration.md
	// §8.3's intentionally strict default (a profile that omits tools does
	// not inherit its parent's capability set).
	Tools []string

	// SlashCommands is the flat allow-list of prompt_expansion slash
	// command names this profile's sessions may invoke. A nil or empty
	// SlashCommands resolves to no prompt_expansion commands — the same
	// strict-default posture as Tools (configuration.md §8.3). This has no
	// effect on direct_invoke commands, which remain scoped by Tools alone.
	SlashCommands []string

	// MaxTurns is this profile's loop-bound turn cap, matching
	// agent-loop.md §3.1's LoopBounds (configuration.md §8.5).
	MaxTurns int

	// MaxCostUSD is this profile's loop-bound cost cap in US dollars,
	// matching agent-loop.md §3.1's LoopBounds (configuration.md §8.5). It
	// is a float because agent.hcl's own example (max_cost_usd = 5.00)
	// carries a fractional dollar amount.
	MaxCostUSD float64

	// MaxWallClockS is this profile's loop-bound wall-clock cap in whole
	// seconds, matching agent-loop.md §3.1's LoopBounds (configuration.md
	// §8.5).
	MaxWallClockS int

	// MaxDepth is this profile's own declared depth ceiling
	// (configuration.md §8.4). nil means unset: the root profile falls back
	// to the kernel's configured default (RootRemainingDepth's
	// kernelDefault); a non-root profile falls back to +inf, meaning it
	// imposes no ceiling of its own and simply inherits whatever budget its
	// parent has left (ChildRemainingDepth). A profile's own MaxDepth is
	// always a ceiling it never exceeds on its own — never a guarantee it
	// gets that much depth, since an ancestor's tighter budget always wins
	// going down.
	MaxDepth *int

	// MaxConcurrentSubagents caps how many sub-agent sessions this
	// profile's sessions may run concurrently (configuration.md §8).
	MaxConcurrentSubagents int
}
