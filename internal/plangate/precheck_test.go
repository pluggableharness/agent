package plangate

import (
	"context"
	"testing"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/circuitbreaker"
	"github.com/pluggableharness/agent/internal/policy"
)

func dataSourceSchema(name string) *toolv1.ToolSchema {
	return &toolv1.ToolSchema{
		Name: name,
		Kind: toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
		Risk: toolv1.RiskClass_RISK_CLASS_READ_ONLY,
	}
}

func interactiveSchema(name string) *toolv1.ToolSchema {
	return &toolv1.ToolSchema{
		Name: name,
		Kind: toolv1.ToolKind_TOOL_KIND_INTERACTIVE,
		Risk: toolv1.RiskClass_RISK_CLASS_LOW,
	}
}

func TestPrecheck_outcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		rules          []policy.Rule
		schema         *toolv1.ToolSchema
		wantAllowed    bool
		wantRule       string
		wantDowngraded bool
	}{
		{
			name:        "no matching rule allows a read by default",
			schema:      dataSourceSchema("read_file"),
			wantAllowed: true,
		},
		{
			name:        "no matching rule allows an interactive call by default",
			schema:      interactiveSchema("read_file"),
			wantAllowed: true,
		},
		{
			name:        "an allow rule allows",
			rules:       []policy.Rule{ruleFor("reads", "fs", "read_file", policy.ActionAllow)},
			schema:      dataSourceSchema("read_file"),
			wantAllowed: true,
			wantRule:    "reads",
		},
		{
			name:        "a plain deny is not a downgrade",
			rules:       []policy.Rule{ruleFor("no-reads", "fs", "read_file", policy.ActionDeny)},
			schema:      dataSourceSchema("read_file"),
			wantAllowed: false,
			wantRule:    "no-reads",
		},
		{
			name:           "an ask against a data_source is a distinguishable downgrade",
			rules:          []policy.Rule{ruleFor("confirm-reads", "fs", "read_file", policy.ActionAsk)},
			schema:         dataSourceSchema("read_file"),
			wantAllowed:    false,
			wantRule:       "confirm-reads",
			wantDowngraded: true,
		},
		{
			name:           "an ask against an interactive call is a distinguishable downgrade",
			rules:          []policy.Rule{ruleFor("confirm-reads", "fs", "read_file", policy.ActionAsk)},
			schema:         interactiveSchema("read_file"),
			wantAllowed:    false,
			wantRule:       "confirm-reads",
			wantDowngraded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newTestGate(t, Config{Rules: tt.rules})
			call := &toolv1.ToolCall{Id: "call-1", ToolName: "read_file"}
			results := g.Precheck(context.Background(), []PrecheckCall{
				{Call: call, Provider: "fs", Schema: tt.schema},
			})
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1", len(results))
			}
			got := results[0]
			if got.Call != call {
				t.Error("result does not echo the call it was built from")
			}
			if got.Allowed != tt.wantAllowed {
				t.Errorf("Allowed = %t, want %t", got.Allowed, tt.wantAllowed)
			}
			if got.Rule != tt.wantRule {
				t.Errorf("Rule = %q, want %q", got.Rule, tt.wantRule)
			}
			if got.Downgraded != tt.wantDowngraded {
				t.Errorf("Downgraded = %t, want %t", got.Downgraded, tt.wantDowngraded)
			}
			if tt.wantAllowed && got.Denial != nil {
				t.Errorf("Denial = %v on an allowed call, want nil", got.Denial)
			}
			if !tt.wantAllowed {
				if got.Denial == nil {
					t.Fatal("Denial = nil on a denied call, want a synthesized ToolError")
				}
				if got.Denial.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED {
					t.Errorf("Denial category = %v, want PERMISSION_DENIED", got.Denial.GetCategory())
				}
				if got.Denial.GetRetryable() {
					t.Error("Denial is retryable; re-issuing an identical denied call is the denial storm the breaker exists to stop")
				}
			}
		})
	}
}

func TestPrecheck_evaluatesEachCallIndependently(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{Rules: []policy.Rule{
		ruleFor("no-secrets", "fs", "read_secret", policy.ActionDeny),
	}})

	results := g.Precheck(context.Background(), []PrecheckCall{
		{Call: &toolv1.ToolCall{Id: "c1", ToolName: "read_file"}, Provider: "fs", Schema: dataSourceSchema("read_file")},
		{Call: &toolv1.ToolCall{Id: "c2", ToolName: "read_secret"}, Provider: "fs", Schema: dataSourceSchema("read_secret")},
		{Call: &toolv1.ToolCall{Id: "c3", ToolName: "get"}, Provider: "http", Schema: dataSourceSchema("get")},
	})

	want := []bool{true, false, true}
	if len(results) != len(want) {
		t.Fatalf("results = %d, want %d", len(results), len(want))
	}
	for i, w := range want {
		if results[i].Allowed != w {
			t.Errorf("result %d Allowed = %t, want %t", i, results[i].Allowed, w)
		}
	}
}

func TestPrecheck_nilCallIsDeniedNotFatal(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{})
	results := g.Precheck(context.Background(), []PrecheckCall{
		{Provider: "fs", Schema: dataSourceSchema("read_file")},
		{Call: &toolv1.ToolCall{Id: "c2", ToolName: "read_file"}, Provider: "fs", Schema: dataSourceSchema("read_file")},
	})

	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (one bad call must not drop the good one)", len(results))
	}
	if results[0].Allowed || results[0].Denial == nil {
		t.Error("a nil call must be reported as a denial with a synthesized error")
	}
	if !results[1].Allowed {
		t.Error("the well-formed call was denied because of its malformed neighbor")
	}
}

func TestPrecheck_circuitBreakerTripSurfaces(t *testing.T) {
	t.Parallel()

	breaker := circuitbreaker.New(circuitbreaker.Config{ConsecutiveThreshold: 3})
	g := newTestGate(t, Config{
		Rules:   []policy.Rule{ruleFor("no-reads", "fs", "read_file", policy.ActionDeny)},
		Breaker: breaker,
	})

	call := PrecheckCall{
		Call:     &toolv1.ToolCall{Id: "c1", ToolName: "read_file"},
		Provider: "fs",
		Schema:   dataSourceSchema("read_file"),
	}

	for attempt := 1; attempt <= 3; attempt++ {
		results := g.Precheck(context.Background(), []PrecheckCall{call})
		got := results[0].Tripped
		want := attempt == 3
		if got != want {
			t.Errorf("attempt %d: Tripped = %t, want %t", attempt, got, want)
		}
	}
}

func TestPrecheck_noBreakerNeverTrips(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{Rules: []policy.Rule{ruleFor("no-reads", "fs", "read_file", policy.ActionDeny)}})
	for range 5 {
		results := g.Precheck(context.Background(), []PrecheckCall{{
			Call:     &toolv1.ToolCall{Id: "c1", ToolName: "read_file"},
			Provider: "fs",
			Schema:   dataSourceSchema("read_file"),
		}})
		if results[0].Tripped {
			t.Fatal("Tripped = true with no configured Breaker")
		}
	}
}
