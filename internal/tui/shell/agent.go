package shell

import "github.com/pluggableharness/agent/internal/tui/theme"

// Agent is one selectable agent profile: what it is called and how it is
// colored.
//
// This is deliberately plain data rather than behavior. The roster is expected
// to come from configuration — an `agent_profile` block naming a tone — so
// nothing here may depend on the three built-in entries existing.
//
// Tone is a role, not a color: config says `color = "accent"` and the active
// theme decides what that means, which is what keeps a custom theme able to
// recolor agents along with everything else.
type Agent struct {
	Name string
	Tone theme.Tone
	// Description is shown when the roster is presented as a list. It is
	// optional; an empty value simply renders nothing.
	Description string
}

// DefaultAgents is the roster the shell falls back to when configuration
// supplies none.
//
// The three entries are the demo set, not a protocol-defined vocabulary. The
// tones are chosen to be distinguishable at a glance rather than decorative:
// building is the ordinary mode, planning is the careful read-only one and
// borrows the color the rest of the UI already uses for "worth your attention",
// and chat is the one that changes nothing.
var DefaultAgents = []Agent{
	{Name: "Code", Tone: theme.TonePrimary, Description: "build and edit"},
	{Name: "Plan", Tone: theme.ToneWarning, Description: "read-only, no tools applied"},
	{Name: "Chat", Tone: theme.ToneInfo, Description: "conversation only"},
}

// agentRing holds the selectable roster and which entry is active.
type agentRing struct {
	agents []Agent
	active int
}

func newAgentRing(agents []Agent) agentRing {
	if len(agents) == 0 {
		agents = DefaultAgents
	}

	return agentRing{agents: agents}
}

// Current returns the active agent. The roster is never empty — a caller that
// supplies none gets the default set — so this always has something to return.
func (r agentRing) Current() Agent { return r.agents[r.active] }

// cycle advances the selection, wrapping at either end.
func (r *agentRing) cycle(back bool) Agent {
	step := 1
	if back {
		step = -1
	}

	r.active = (r.active + step + len(r.agents)) % len(r.agents)

	return r.Current()
}
