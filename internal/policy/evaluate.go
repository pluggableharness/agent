package policy

import toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

// Call is the real shape of a tool call being evaluated against policy
// rules. Unlike Match.Kind (restricted to 2 values — see Kind's doc
// comment), Call.Kind is the full toolv1.ToolKind, since a real call can
// be any of the 3 kinds tool.md defines, including TOOL_KIND_INTERACTIVE.
type Call struct {
	// Kind is the call's tool kind.
	Kind toolv1.ToolKind

	// Provider is the call's provider name.
	Provider string

	// ToolName is the call's tool name.
	ToolName string

	// Risk is the call's declared risk class.
	Risk toolv1.RiskClass
}

// match reports whether m matches call. Every field m specifies must agree
// with call; an omitted field always agrees (configuration.md §7.1).
//
// m.Kind, when set, is compared against call.Kind through Kind's narrower
// 2-value space: KindResource matches only TOOL_KIND_RESOURCE and
// KindDataSource matches only TOOL_KIND_DATA_SOURCE. A call with
// TOOL_KIND_INTERACTIVE never matches a Match with Kind set, on either
// value — a rule that specifically targets kind = data_source (or
// resource) should not also silently catch interactive calls just because
// Evaluate's defaults/downgrades later treat the two similarly. That
// similarity is about what happens when nothing matches (see Evaluate); it
// is not a matching rule in its own right.
func match(m Match, call Call) bool {
	if m.ToolName != nil && *m.ToolName != call.ToolName {
		return false
	}
	if m.Provider != nil && *m.Provider != call.Provider {
		return false
	}
	if m.Risk != nil && *m.Risk != call.Risk {
		return false
	}
	if m.Kind != nil {
		switch *m.Kind {
		case KindResource:
			if call.Kind != toolv1.ToolKind_TOOL_KIND_RESOURCE {
				return false
			}
		case KindDataSource:
			if call.Kind != toolv1.ToolKind_TOOL_KIND_DATA_SOURCE {
				return false
			}
		case KindUnspecified:
			// A Match.Kind pointer holding KindUnspecified is a
			// precondition violation (see Kind's doc comment) — treat
			// it as "never matches" rather than panicking.
			return false
		}
	}
	return true
}

// Evaluate implements configuration.md §7.3's evaluate_policy algorithm:
// it filters rules to those matching call, picks the most specific
// candidate (specificity.go), and applies call's default/downgrade
// behavior.
//
// It returns the resulting action, the name of the rule responsible for it
// (empty when no rule matched and a default applied), and downgraded,
// which reports whether the winning rule's action was ActionAsk and was
// downgraded to ActionDeny per the rule below. configuration.md §7.3 says
// the kernel "logs why" when this downgrade happens; this package has no
// logger (doc.go), so downgraded exists for a caller to log that warning
// itself instead of Evaluate doing it silently or not at all.
//
// Defaulting when no rule matches (configuration.md §7.3): a
// TOOL_KIND_DATA_SOURCE call defaults to ActionAllow ("no apply step to
// gate a read behind" — the conservative ActionAsk default would be
// meaningless); every other call defaults to ActionAsk.
//
// Extension per agent-loop.md §5.4bis (not spelled out verbatim in
// configuration.md §7.3, whose own pseudocode only discusses the 2-kind
// case, but a faithful extrapolation from it): TOOL_KIND_INTERACTIVE also
// defaults to ActionAllow, because agent-loop.md §5.4bis states an
// interactive call "reuses [data_source's] precheck verbatim — same
// allow/deny-only outcome space" on account of sharing the same "no apply
// step to gate" property.
//
// Downgrade (configuration.md §7.3, same §5.4bis extension): if call's
// kind is TOOL_KIND_DATA_SOURCE or TOOL_KIND_INTERACTIVE and the winning
// action is ActionAsk, it is downgraded to ActionDeny — asking a question
// with no apply step to answer it against is meaningless. TOOL_KIND_RESOURCE
// calls are never downgraded; allow/ask/deny pass through unchanged.
//
// If more than one candidate ties for most specific — a state
// ValidateRules is meant to reject at config-load time and which Evaluate
// therefore treats as an already-validated precondition, not a case to
// handle gracefully — Evaluate deterministically picks the
// first-encountered candidate and does not panic.
func Evaluate(rules []Rule, call Call) (action Action, matchedRule string, downgraded bool) {
	var candidates []Rule
	for _, r := range rules {
		if match(r.Match, call) {
			candidates = append(candidates, r)
		}
	}

	noGate := call.Kind == toolv1.ToolKind_TOOL_KIND_DATA_SOURCE || call.Kind == toolv1.ToolKind_TOOL_KIND_INTERACTIVE

	if len(candidates) == 0 {
		if noGate {
			return ActionAllow, "", false
		}
		return ActionAsk, "", false
	}

	winner := candidates[0]
	winnerTuple := specificity(winner.Match)
	for _, c := range candidates[1:] {
		tuple := specificity(c.Match)
		if moreSpecific(tuple, winnerTuple) {
			winner = c
			winnerTuple = tuple
		}
	}

	if noGate && winner.Action == ActionAsk {
		return ActionDeny, winner.Name, true
	}
	return winner.Action, winner.Name, false
}
