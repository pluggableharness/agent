package policy

import toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

// Kind is the 2-value subset of tool.md's ToolKind that a policy `match`
// block can target directly: `resource` or `data_source`
// (configuration.md §7.1's PolicyMatch.kind). It is a distinct, narrower
// type from toolv1.ToolKind rather than a reuse of it, because that
// narrowness is a real, documented v1 limitation, not an oversight:
// tool.md added a third kind, `interactive` (the ask_user case), after
// PolicyMatch's schema was fixed at 2 values, and configuration.md §7.1
// never widened it. An operator therefore cannot write
// `match = { kind = "interactive" }` — interactive calls can still be
// targeted through the other three match fields (tool_name, provider,
// risk), just not by kind.
//
// This does not mean interactive calls fall outside policy evaluation.
// agent-loop.md §5.4bis states that interactive calls "reuse [the
// data_source precheck] verbatim — same allow/deny-only outcome space" as
// data_source calls; Evaluate (evaluate.go) honors that by extending
// data_source's defaulting and ask-downgrade behavior to
// TOOL_KIND_INTERACTIVE calls even though no Match.Kind value can name
// that kind explicitly. The zero value is KindUnspecified, per this
// project's convention that an enum's zero value is a sentinel never
// stored as a meaningful choice.
type Kind int

const (
	// KindUnspecified is Kind's zero value. It is not a valid, meaningful
	// choice for a Match.Kind field — a Match with Kind set to a
	// non-nil pointer to this value should not occur; the way to express
	// "any kind" is a nil Match.Kind pointer.
	KindUnspecified Kind = iota

	// KindResource matches a call whose toolv1.ToolKind is
	// TOOL_KIND_RESOURCE.
	KindResource

	// KindDataSource matches a call whose toolv1.ToolKind is
	// TOOL_KIND_DATA_SOURCE.
	KindDataSource
)

// Match is configuration.md §7.1's PolicyMatch: the criteria a `policy`
// block's `match = { ... }` attribute specifies. Every field is a pointer
// so that nil unambiguously means "omitted" — per §7.1, "an omitted field
// matches anything"; specified fields are ANDed together.
type Match struct {
	// ToolName, if non-nil, requires an exact match against the call's
	// tool name.
	ToolName *string

	// Provider, if non-nil, requires an exact match against the call's
	// provider name.
	Provider *string

	// Risk, if non-nil, requires an exact match against the call's
	// risk class (tool.md §2's RiskClass, reused verbatim per this
	// project's "exactly one Go representation of each wire message"
	// rule).
	Risk *toolv1.RiskClass

	// Kind, if non-nil, requires an exact match against the call's kind,
	// restricted to Kind's 2-value subset — see Kind's doc comment for
	// why this can't name `interactive`.
	Kind *Kind
}

// Action is the outcome a policy rule assigns to a matching call:
// configuration.md §7's `action = "allow" | "ask" | "deny"`.
type Action int

const (
	// ActionUnspecified is Action's zero value. It is never a valid
	// stored action for a real Rule — every Rule's Action MUST be one of
	// ActionAllow, ActionAsk, or ActionDeny.
	ActionUnspecified Action = iota

	// ActionAllow lets a matching call proceed without operator
	// confirmation.
	ActionAllow

	// ActionAsk requires operator confirmation before a matching call
	// proceeds. Evaluate downgrades this to ActionDeny for data_source
	// and interactive calls, per configuration.md §7.3 and
	// agent-loop.md §5.4bis — see evaluate.go.
	ActionAsk

	// ActionDeny blocks a matching call outright.
	ActionDeny
)

// Rule is one `policy "<name>" { match = ...; action = ... }` block,
// decoded from agent.hcl.
type Rule struct {
	// Name is the policy block's label — used only for diagnostics (a
	// conflict error names both rules by Name).
	Name string

	// Match is the rule's match criteria (configuration.md §7.1).
	Match Match

	// Action is the rule's action (configuration.md §7's `action`
	// attribute), one of ActionAllow, ActionAsk, or ActionDeny.
	Action Action
}
