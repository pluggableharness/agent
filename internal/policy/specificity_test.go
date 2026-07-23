package policy

import (
	"errors"
	"testing"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func strPtr(s string) *string { return &s }

func kindPtr(k Kind) *Kind { return &k }

func riskPtr(r toolv1.RiskClass) *toolv1.RiskClass { return &r }

func TestSpecificity(t *testing.T) {
	tests := []struct {
		name string
		m    Match
		want [4]bool
	}{
		{"empty", Match{}, [4]bool{false, false, false, false}},
		{"tool name only", Match{ToolName: strPtr("read_file")}, [4]bool{true, false, false, false}},
		{"provider only", Match{Provider: strPtr("filesystem")}, [4]bool{false, true, false, false}},
		{"risk only", Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_CRITICAL)}, [4]bool{false, false, true, false}},
		{"kind only", Match{Kind: kindPtr(KindDataSource)}, [4]bool{false, false, false, true}},
		{"all fields", Match{
			ToolName: strPtr("read_file"),
			Provider: strPtr("filesystem"),
			Risk:     riskPtr(toolv1.RiskClass_RISK_CLASS_LOW),
			Kind:     kindPtr(KindResource),
		}, [4]bool{true, true, true, true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := specificity(tt.m)
			if got != tt.want {
				t.Fatalf("specificity(%+v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}

func TestMoreSpecific(t *testing.T) {
	tests := []struct {
		name string
		a, b [4]bool
		want bool
	}{
		{"a has tool_name, b has provider+risk+kind: tool_name wins", [4]bool{true, false, false, false}, [4]bool{false, true, true, true}, true},
		{"a has provider, b has risk+kind: provider wins", [4]bool{false, true, false, false}, [4]bool{false, false, true, true}, true},
		{"a has risk, b has kind: risk wins", [4]bool{false, false, true, false}, [4]bool{false, false, false, true}, true},
		{"equal tuples: neither wins", [4]bool{true, false, true, false}, [4]bool{true, false, true, false}, false},
		{"a less specific than b", [4]bool{false, true, true, true}, [4]bool{true, false, false, false}, false},
		{"all false vs all true: b wins", [4]bool{false, false, false, false}, [4]bool{true, true, true, true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := moreSpecific(tt.a, tt.b); got != tt.want {
				t.Fatalf("moreSpecific(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	tests := []struct {
		name string
		a, b Match
		want bool
	}{
		{
			name: "read_file vs write_file: same tuple, disjoint tool_name, not a conflict",
			a:    Match{ToolName: strPtr("read_file")},
			b:    Match{ToolName: strPtr("write_file")},
			want: false,
		},
		{
			name: "identical empty match: conflict",
			a:    Match{},
			b:    Match{},
			want: true,
		},
		{
			name: "identical tool_name: conflict",
			a:    Match{ToolName: strPtr("read_file")},
			b:    Match{ToolName: strPtr("read_file")},
			want: true,
		},
		{
			name: "same tuple, all shared fields agree: conflict",
			a:    Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)},
			b:    Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)},
			want: true,
		},
		{
			name: "same tuple, one shared field disagrees: not a conflict",
			a:    Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)},
			b:    Match{Provider: strPtr("network"), Kind: kindPtr(KindResource)},
			want: false,
		},
		{
			name: "different tuples: not a conflict",
			a:    Match{ToolName: strPtr("read_file")},
			b:    Match{Provider: strPtr("filesystem")},
			want: false,
		},
		{
			name: "different risk values, same tuple: not a conflict",
			a:    Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_LOW)},
			b:    Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_CRITICAL)},
			want: false,
		},
		{
			name: "different kind values, same tuple: not a conflict",
			a:    Match{Kind: kindPtr(KindResource)},
			b:    Match{Kind: kindPtr(KindDataSource)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Conflicts(tt.a, tt.b); got != tt.want {
				t.Fatalf("Conflicts(%+v, %+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			// Conflicts must be symmetric.
			if got := Conflicts(tt.b, tt.a); got != tt.want {
				t.Fatalf("Conflicts(%+v, %+v) [swapped] = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name    string
		rules   []Rule
		wantErr bool
	}{
		{
			name: "no conflict: read_file vs write_file near-miss",
			rules: []Rule{
				{Name: "allow_read", Match: Match{ToolName: strPtr("read_file")}, Action: ActionAllow},
				{Name: "gate_write", Match: Match{ToolName: strPtr("write_file")}, Action: ActionAsk},
			},
			wantErr: false,
		},
		{
			name: "no conflict: distinct specificity tuples",
			rules: []Rule{
				{Name: "gate_filesystem_writes", Match: Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)}, Action: ActionAsk},
				{Name: "block_critical", Match: Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_CRITICAL)}, Action: ActionDeny},
			},
			wantErr: false,
		},
		{
			name: "real conflict: identical match",
			rules: []Rule{
				{Name: "rule_a", Match: Match{Provider: strPtr("filesystem")}, Action: ActionAllow},
				{Name: "rule_b", Match: Match{Provider: strPtr("filesystem")}, Action: ActionDeny},
			},
			wantErr: true,
		},
		{
			name: "real conflict: same tuple, all shared fields agree",
			rules: []Rule{
				{Name: "rule_a", Match: Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)}, Action: ActionAllow},
				{Name: "rule_b", Match: Match{Provider: strPtr("filesystem"), Kind: kindPtr(KindResource)}, Action: ActionAsk},
				{Name: "rule_c", Match: Match{ToolName: strPtr("read_file")}, Action: ActionAllow},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateRules(tt.rules)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateRules(%v) = nil error, want error", tt.rules)
				}
				if !errors.Is(err, ErrConflictingRules) {
					t.Fatalf("ValidateRules(%v) error = %v, want wrapping ErrConflictingRules", tt.rules, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRules(%v) unexpected error: %v", tt.rules, err)
			}
		})
	}
}

func TestIsEmptyMatch(t *testing.T) {
	tests := []struct {
		name string
		m    Match
		want bool
	}{
		{"empty", Match{}, true},
		{"tool name set", Match{ToolName: strPtr("read_file")}, false},
		{"provider set", Match{Provider: strPtr("filesystem")}, false},
		{"risk set", Match{Risk: riskPtr(toolv1.RiskClass_RISK_CLASS_LOW)}, false},
		{"kind set", Match{Kind: kindPtr(KindResource)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsEmptyMatch(tt.m); got != tt.want {
				t.Fatalf("IsEmptyMatch(%+v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}
