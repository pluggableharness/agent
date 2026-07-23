package policy

import (
	"testing"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestMatch(t *testing.T) {
	call := Call{
		Kind:     toolv1.ToolKind_TOOL_KIND_RESOURCE,
		Provider: "filesystem",
		ToolName: "write_file",
		Risk:     toolv1.RiskClass_RISK_CLASS_MODERATE,
	}

	tests := []struct {
		name string
		m    Match
		want bool
	}{
		{"empty match: always matches", Match{}, true},
		{"tool_name matches", Match{ToolName: strPtr("write_file")}, true},
		{"tool_name mismatches", Match{ToolName: strPtr("read_file")}, false},
		{"provider matches", Match{Provider: strPtr("filesystem")}, true},
		{"provider mismatches", Match{Provider: strPtr("network")}, false},
		{"risk matches", Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_MODERATE)}, true},
		{"risk mismatches", Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_CRITICAL)}, false},
		{"kind resource matches resource call", Match{Kind: kindPtr(KindResource)}, true},
		{"kind data_source mismatches resource call", Match{Kind: kindPtr(KindDataSource)}, false},
		{
			"kind unspecified stored in pointer never matches (precondition violation)",
			Match{Kind: kindPtr(KindUnspecified)},
			false,
		},
		{"all fields agree", Match{
			ToolName: strPtr("write_file"),
			Provider: strPtr("filesystem"),
			Risk:     riskPtr(toolv1.RiskClass_RISK_CLASS_MODERATE),
			Kind:     kindPtr(KindResource),
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := match(tt.m, call); got != tt.want {
				t.Errorf("match(%+v, %+v) = %v, want %v", tt.m, call, got, tt.want)
			}
		})
	}
}

func TestMatchKindDataSourceVsInteractiveCall(t *testing.T) {
	t.Parallel()
	m := Match{Kind: kindPtr(KindDataSource)}
	call := Call{Kind: toolv1.ToolKind_TOOL_KIND_INTERACTIVE, ToolName: "ask_user", Provider: "human"}
	if match(m, call) {
		t.Error("match() = true, want false: kind=data_source must not match an interactive call")
	}
}

func TestEvaluateDefaults(t *testing.T) {
	tests := []struct {
		name string
		kind toolv1.ToolKind
		want Action
	}{
		{"resource defaults to ask", toolv1.ToolKind_TOOL_KIND_RESOURCE, ActionAsk},
		{"data_source defaults to allow", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, ActionAllow},
		{"interactive defaults to allow (agent-loop.md §5.4bis)", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, ActionAllow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			call := Call{Kind: tt.kind, ToolName: "some_tool", Provider: "some_provider"}
			action, matchedRule, downgraded := Evaluate(nil, call)
			if action != tt.want {
				t.Errorf("Evaluate(nil, %+v) action = %v, want %v", call, action, tt.want)
			}
			if matchedRule != "" {
				t.Errorf("Evaluate(nil, %+v) matchedRule = %q, want empty", call, matchedRule)
			}
			if downgraded {
				t.Errorf("Evaluate(nil, %+v) downgraded = true, want false", call)
			}
		})
	}
}

func TestEvaluateAskDowngradesToDenyForDataSource(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Name: "gate_reads", Match: Match{Kind: kindPtr(KindDataSource)}, Action: ActionAsk},
	}
	call := Call{Kind: toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, ToolName: "web_search", Provider: "web"}

	action, matchedRule, downgraded := Evaluate(rules, call)
	if action != ActionDeny {
		t.Errorf("action = %v, want ActionDeny", action)
	}
	if matchedRule != "gate_reads" {
		t.Errorf("matchedRule = %q, want %q", matchedRule, "gate_reads")
	}
	if !downgraded {
		t.Error("downgraded = false, want true")
	}
}

func TestEvaluateAskDowngradesToDenyForInteractive(t *testing.T) {
	t.Parallel()
	// Match.Kind cannot name "interactive" directly (Kind is a 2-value
	// subset — see Kind's doc comment); this rule instead targets the
	// interactive call by provider, leaving Kind unset so it matches
	// anything.
	rules := []Rule{
		{Name: "gate_human_asks", Match: Match{Provider: strPtr("human")}, Action: ActionAsk},
	}
	call := Call{Kind: toolv1.ToolKind_TOOL_KIND_INTERACTIVE, ToolName: "ask_user", Provider: "human"}

	action, matchedRule, downgraded := Evaluate(rules, call)
	if action != ActionDeny {
		t.Errorf("action = %v, want ActionDeny", action)
	}
	if matchedRule != "gate_human_asks" {
		t.Errorf("matchedRule = %q, want %q", matchedRule, "gate_human_asks")
	}
	if !downgraded {
		t.Error("downgraded = false, want true")
	}
}

func TestEvaluateResourceAskDoesNotDowngrade(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Name: "gate_filesystem_writes", Match: Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)}, Action: ActionAsk},
	}
	call := Call{Kind: toolv1.ToolKind_TOOL_KIND_RESOURCE, ToolName: "write_file", Provider: "filesystem"}

	action, matchedRule, downgraded := Evaluate(rules, call)
	if action != ActionAsk {
		t.Errorf("action = %v, want ActionAsk", action)
	}
	if matchedRule != "gate_filesystem_writes" {
		t.Errorf("matchedRule = %q, want %q", matchedRule, "gate_filesystem_writes")
	}
	if downgraded {
		t.Error("downgraded = true, want false")
	}
}

func TestEvaluateMostSpecificCandidateWins(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Name: "block_high_risk", Match: Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_CRITICAL)}, Action: ActionDeny},
		{Name: "allow_specific_tool", Match: Match{ToolName: strPtr("trusted_tool")}, Action: ActionAllow},
	}
	call := Call{
		Kind:     toolv1.ToolKind_TOOL_KIND_RESOURCE,
		ToolName: "trusted_tool",
		Provider: "some_provider",
		Risk:     toolv1.RiskClass_RISK_CLASS_CRITICAL,
	}

	action, matchedRule, downgraded := Evaluate(rules, call)
	if action != ActionAllow {
		t.Errorf("action = %v, want ActionAllow (tool_name is more specific than risk)", action)
	}
	if matchedRule != "allow_specific_tool" {
		t.Errorf("matchedRule = %q, want %q", matchedRule, "allow_specific_tool")
	}
	if downgraded {
		t.Error("downgraded = true, want false")
	}
}

// TestEvaluateKindDoesNotBleedAcrossInteractive confirms that a rule
// targeting kind = data_source does not also match an interactive call,
// even though Evaluate's defaulting/downgrade behavior treats the two
// kinds similarly (evaluate.go's match, not its defaulting, is what's
// under test here).
func TestEvaluateKindDoesNotBleedAcrossInteractive(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Name: "block_all_data_source", Match: Match{Kind: kindPtr(KindDataSource)}, Action: ActionDeny},
	}
	call := Call{Kind: toolv1.ToolKind_TOOL_KIND_INTERACTIVE, ToolName: "ask_user", Provider: "human"}

	action, matchedRule, downgraded := Evaluate(rules, call)
	if action != ActionAllow {
		t.Errorf("action = %v, want ActionAllow (no rule should match; default applies)", action)
	}
	if matchedRule != "" {
		t.Errorf("matchedRule = %q, want empty (no rule matched)", matchedRule)
	}
	if downgraded {
		t.Error("downgraded = true, want false")
	}
}
