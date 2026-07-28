package plangate

import (
	"context"
	"errors"
	"strings"
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/circuitbreaker"
	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/fake"
	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/schemavalidate"
	"github.com/pluggableharness/agent/internal/statebackend"
)

func threeProviderPlan() *planv1.Plan {
	return &planv1.Plan{
		TurnId: "turn-1",
		Items: []*planv1.PlanItem{
			resourceItem("i1", "fs", "write_file"),
			resourceItem("i2", "http", "post"),
			resourceItem("i3", "shell", "exec"),
		},
	}
}

// Three resource calls against three providers MUST receive three
// independently evaluated decisions — plan-apply-gate.md#plan-construction-and-policy-evaluation.
func TestDecide_perItemPolicyEvaluation(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Rules: []policy.Rule{
			ruleFor("allow-writes", "fs", "write_file", policy.ActionAllow),
			ruleFor("deny-shell", "shell", "exec", policy.ActionDeny),
			// http.post matches nothing and falls through to the
			// resource default, which is ask.
		},
		Resolver: fake.NewAlways(allowDecision(planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE)),
		Events:   sink,
	})

	plan := threeProviderPlan()
	d, err := g.Decide(context.Background(), plan)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	wantDecidedBy := map[string]string{
		"i1": "policy:allow-writes",
		"i2": "policy:default+resolver:frontend",
		"i3": "policy:deny-shell",
	}
	wantDecision := map[string]planv1.PlanDecision{
		"i1": planv1.PlanDecision_PLAN_DECISION_ALLOW,
		"i2": planv1.PlanDecision_PLAN_DECISION_ALLOW,
		"i3": planv1.PlanDecision_PLAN_DECISION_DENY,
	}
	for _, item := range plan.GetItems() {
		if got := item.GetDecidedBy(); got != wantDecidedBy[item.GetId()] {
			t.Errorf("%s decided_by = %q, want %q", item.GetId(), got, wantDecidedBy[item.GetId()])
		}
		if got := item.GetDecision(); got != wantDecision[item.GetId()] {
			t.Errorf("%s decision = %v, want %v", item.GetId(), got, wantDecision[item.GetId()])
		}
	}
	if len(d.Allowed) != 2 || len(d.Denied) != 1 {
		t.Fatalf("allowed/denied = %d/%d, want 2/1", len(d.Allowed), len(d.Denied))
	}
	if d.VetoedBy != "" {
		t.Errorf("VetoedBy = %q, want empty", d.VetoedBy)
	}

	// Persistence: one plan event, one terminal row per item.
	rec := sink.onlyPlan(t)
	if rec.event.Kind != kernelv1.EventKind_EVENT_KIND_PLAN {
		t.Errorf("event kind = %v, want EVENT_KIND_PLAN", rec.event.Kind)
	}
	if !statebackend.IsKernelProducer(rec.event.Producer) {
		t.Errorf("event producer = %v, want the reserved kernel producer", rec.event.Producer)
	}
	if len(rec.event.Payload) == 0 {
		t.Error("plan event payload is empty")
	}
	if len(rec.items) != 3 {
		t.Fatalf("plan_items rows = %d, want 3", len(rec.items))
	}
	for _, row := range rec.items {
		switch row.Decision {
		case planv1.PlanDecision_PLAN_DECISION_ALLOW, planv1.PlanDecision_PLAN_DECISION_DENY:
		default:
			t.Errorf("row %s.%s persisted with a non-terminal decision %v", row.ProviderName, row.ToolName, row.Decision)
		}
		if row.TurnID != "turn-1" {
			t.Errorf("row turn id = %q, want %q", row.TurnID, "turn-1")
		}
		if row.DecidedBy != wantDecidedBy[strings.TrimPrefix(row.ToolCallID, "call-")] {
			t.Errorf("row %s decided_by = %q", row.ToolCallID, row.DecidedBy)
		}
	}
}

func TestDecide_askEscalatesToResolverWithCompositeDecidedBy(t *testing.T) {
	t.Parallel()

	resolver := fake.New(fake.Response{Decision: plandecision.Decision{
		Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
		Scope:     planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
		DecidedBy: "tui",
	}})
	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: resolver,
		Events:   sink,
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), plan); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	const want = "policy:confirm-writes+resolver:tui"
	if got := plan.GetItems()[0].GetDecidedBy(); got != want {
		t.Errorf("decided_by = %q, want %q", got, want)
	}
	rec := sink.onlyPlan(t)
	if rec.items[0].DecidedBy != want {
		t.Errorf("persisted decided_by = %q, want %q", rec.items[0].DecidedBy, want)
	}
	if calls := resolver.Calls(); len(calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(calls))
	}
}

func TestDecide_correctedInputReplacesTheItemInput(t *testing.T) {
	t.Parallel()

	corrected := mustStruct(map[string]any{"path": "/tmp/safe"})
	resolver := fake.NewAlways(fake.Response{Decision: plandecision.Decision{
		Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
		Scope:          planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
		CorrectedInput: corrected,
		DecidedBy:      "tui",
	}})
	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: resolver,
		Tools:    toolsWithSchema(),
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), plan); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := plan.GetItems()[0].GetInput().GetFields()["path"].GetStringValue(); got != "/tmp/safe" {
		t.Errorf("input path = %q, want the corrected %q", got, "/tmp/safe")
	}
}

// toolsWithSchema resolves fs.write_file to a handle declaring a required
// string "path", so corrected_input re-validation has something to check.
func toolsWithSchema() *fakeTools {
	return &fakeTools{handles: map[string]providercatalog.ToolHandle{
		"fs.write_file": {
			Provider: "fs",
			Schema: &toolv1.ToolSchema{
				Name:        "write_file",
				Kind:        toolv1.ToolKind_TOOL_KIND_RESOURCE,
				Risk:        toolv1.RiskClass_RISK_CLASS_MODERATE,
				InputSchema: schemaWithRequiredPath(),
			},
		},
	}}
}

// An invalid corrected_input is a distinct error — never coerced, and
// never silently downgraded to a plain deny.
func TestDecide_invalidCorrectedInputIsRejected(t *testing.T) {
	t.Parallel()

	bad := mustStruct(map[string]any{"path": 42})
	resolver := fake.NewAlways(fake.Response{Decision: plandecision.Decision{
		Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
		Scope:          planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
		CorrectedInput: bad,
		DecidedBy:      "tui",
	}})
	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: resolver,
		Events:   sink,
		Tools:    toolsWithSchema(),
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	_, err := g.Decide(context.Background(), plan)
	if !errors.Is(err, schemavalidate.ErrValidation) {
		t.Fatalf("Decide err = %v, want one wrapping schemavalidate.ErrValidation", err)
	}
	if got := plan.GetItems()[0].GetDecision(); got != planv1.PlanDecision_PLAN_DECISION_ASK {
		t.Errorf("item decision = %v; an invalid correction must not be downgraded to a decision", got)
	}
	if got := plan.GetItems()[0].GetInput().GetFields()["path"].GetStringValue(); got != "/tmp/x" {
		t.Errorf("input path = %q; an invalid correction must not be coerced onto the item", got)
	}
	if len(sink.plans) != 0 {
		t.Error("a plan was persisted despite the rejected correction")
	}
}

// An ALWAYS-scoped verdict has no writable policy store to land in, and
// MUST surface as a distinct error rather than a silent downgrade.
func TestDecide_alwaysScopeIsRejected(t *testing.T) {
	t.Parallel()

	resolver := fake.NewAlways(allowDecision(planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS))
	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: resolver,
		Events:   sink,
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	_, err := g.Decide(context.Background(), plan)
	if !errors.Is(err, plandecision.ErrPolicyPersistenceUnavailable) {
		t.Fatalf("Decide err = %v, want ErrPolicyPersistenceUnavailable", err)
	}
	if len(sink.plans) != 0 {
		t.Error("a plan was persisted despite the rejected ALWAYS-scoped verdict")
	}
	if got := plan.GetItems()[0].GetDecision(); got == planv1.PlanDecision_PLAN_DECISION_ALLOW {
		t.Error("an ALWAYS-scoped verdict was applied instead of rejected — that is the silent downgrade the spec forbids")
	}
}

// A SESSION-scoped verdict applies to every later matching item in the
// same Gate without a second resolver round trip.
func TestDecide_sessionScopeSuppressesTheSecondResolverCall(t *testing.T) {
	t.Parallel()

	resolver := fake.New(fake.Response{Decision: plandecision.Decision{
		Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
		Scope:     planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION,
		DecidedBy: "tui",
	}})
	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: resolver,
	})

	first := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), first); err != nil {
		t.Fatalf("Decide (first turn): %v", err)
	}

	// The fake's queue holds exactly one response; a second Resolve
	// would fail with ErrExhausted, so a clean second turn proves the
	// resolver was never consulted again.
	second := &planv1.Plan{TurnId: "turn-2", Items: []*planv1.PlanItem{resourceItem("i2", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), second); err != nil {
		t.Fatalf("Decide (second turn): %v", err)
	}

	if calls := resolver.Calls(); len(calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1 (the SESSION verdict must suppress the second)", len(calls))
	}
	if got := second.GetItems()[0].GetDecision(); got != planv1.PlanDecision_PLAN_DECISION_ALLOW {
		t.Errorf("second-turn decision = %v, want ALLOW from the remembered verdict", got)
	}
	const want = "policy:confirm-writes+session:tui"
	if got := second.GetItems()[0].GetDecidedBy(); got != want {
		t.Errorf("second-turn decided_by = %q, want %q", got, want)
	}
}

func TestDecide_sessionScopeIsPerGate(t *testing.T) {
	t.Parallel()

	// A SESSION verdict lapses at session end. A fresh Gate is a fresh
	// session, so it must consult the resolver again.
	newGate := func() (*Gate, *fake.Resolver) {
		r := fake.New(fake.Response{Decision: plandecision.Decision{
			Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:     planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION,
			DecidedBy: "tui",
		}})
		return newTestGate(t, Config{
			Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
			Resolver: r,
		}), r
	}

	g1, r1 := newGate()
	if _, err := g1.Decide(context.Background(), &planv1.Plan{
		TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")},
	}); err != nil {
		t.Fatalf("Decide (gate 1): %v", err)
	}

	g2, r2 := newGate()
	if _, err := g2.Decide(context.Background(), &planv1.Plan{
		TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")},
	}); err != nil {
		t.Fatalf("Decide (gate 2): %v", err)
	}

	if len(r1.Calls()) != 1 || len(r2.Calls()) != 1 {
		t.Errorf("resolver calls = %d/%d, want 1/1 — a SESSION verdict must not leak across Gates",
			len(r1.Calls()), len(r2.Calls()))
	}
}

// A plan-ready veto denies the WHOLE plan, even items policy allowed.
func TestDecide_hookVetoDeniesTheWholePlan(t *testing.T) {
	t.Parallel()

	hooks := vetoHooks("guardrails")
	resolver := fake.NewAlways(allowDecision(planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE))
	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Rules: []policy.Rule{
			ruleFor("allow-writes", "fs", "write_file", policy.ActionAllow),
			ruleFor("allow-post", "http", "post", policy.ActionAllow),
			ruleFor("allow-exec", "shell", "exec", policy.ActionAllow),
		},
		Hooks:    hooks,
		Resolver: resolver,
		Events:   sink,
	})

	plan := threeProviderPlan()
	d, err := g.Decide(context.Background(), plan)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.VetoedBy != "guardrails" {
		t.Errorf("VetoedBy = %q, want %q", d.VetoedBy, "guardrails")
	}
	if len(d.Allowed) != 0 || len(d.Denied) != 3 {
		t.Fatalf("allowed/denied = %d/%d, want 0/3", len(d.Allowed), len(d.Denied))
	}
	for _, item := range plan.GetItems() {
		if item.GetDecision() != planv1.PlanDecision_PLAN_DECISION_DENY {
			t.Errorf("%s decision = %v, want DENY", item.GetId(), item.GetDecision())
		}
		if got := item.GetDecidedBy(); got != "hook-veto:guardrails" {
			t.Errorf("%s decided_by = %q, want %q", item.GetId(), got, "hook-veto:guardrails")
		}
	}
	if hooks.dispatchCount() != 1 {
		t.Errorf("plan-ready dispatches = %d, want exactly 1", hooks.dispatchCount())
	}
	if len(resolver.Calls()) != 0 {
		t.Error("the resolver was consulted despite a plan-wide veto")
	}
	if len(sink.onlyPlan(t).items) != 3 {
		t.Error("a vetoed plan must still persist one terminal row per item")
	}
}

func TestDecide_hookDispatchErrorIsNotAVerdict(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	g := newTestGate(t, Config{
		Hooks:  &fakeHooks{err: errFake},
		Events: sink,
	})

	_, err := g.Decide(context.Background(), threeProviderPlan())
	if !errors.Is(err, errFake) {
		t.Fatalf("Decide err = %v, want the dispatcher's error propagated", err)
	}
	if len(sink.plans) != 0 {
		t.Error("a plan was persisted after a dispatcher failure")
	}
}

func TestDecide_denialSynthesizesAToolResultBlock(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{Rules: []policy.Rule{
		ruleFor("allow-writes", "fs", "write_file", policy.ActionAllow),
		ruleFor("deny-shell", "shell", "exec", policy.ActionDeny),
		ruleFor("allow-post", "http", "post", policy.ActionAllow),
	}})

	d, err := g.Decide(context.Background(), threeProviderPlan())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	blocks := g.DenialBlocks(d)
	if len(blocks) != 1 {
		t.Fatalf("denial blocks = %d, want 1", len(blocks))
	}
	tr := blocks[0].GetToolResult()
	if tr == nil {
		t.Fatal("denial block is not a tool_result block")
	}
	if !tr.GetIsError() {
		t.Error("denial tool_result is_error = false, want true")
	}
	if tr.GetToolUseId() != "call-i3" {
		t.Errorf("tool_use_id = %q, want %q", tr.GetToolUseId(), "call-i3")
	}
	if len(tr.GetContent()) != 1 {
		t.Fatalf("denial content blocks = %d, want 1", len(tr.GetContent()))
	}
	text := tr.GetContent()[0].GetText().GetText()
	if !strings.Contains(text, "shell.exec") || !strings.Contains(text, "policy:deny-shell") {
		t.Errorf("denial text = %q, want it to name the call and the deciding rule", text)
	}
}

func TestDecide_circuitBreakerTripSurfaces(t *testing.T) {
	t.Parallel()

	breaker := circuitbreaker.New(circuitbreaker.Config{ConsecutiveThreshold: 2})
	g := newTestGate(t, Config{
		Rules:   []policy.Rule{ruleFor("deny-shell", "shell", "exec", policy.ActionDeny)},
		Breaker: breaker,
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{
		resourceItem("i1", "shell", "exec"),
		resourceItem("i2", "shell", "exec"),
	}}
	d, err := g.Decide(context.Background(), plan)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(d.Denied) != 2 {
		t.Fatalf("denied = %d, want 2", len(d.Denied))
	}
	if d.Denied[0].Tripped {
		t.Error("the first denial tripped a threshold of 2")
	}
	if !d.Denied[1].Tripped {
		t.Error("the second consecutive denial did not trip a threshold of 2")
	}
	got := d.TrippedProviders()
	if len(got) != 1 || got[0] != "shell" {
		t.Errorf("TrippedProviders = %v, want [shell]", got)
	}
}

func TestDecisions_TrippedProvidersIsSortedAndDeduped(t *testing.T) {
	t.Parallel()

	d := Decisions{Denied: []DeniedItem{
		{Item: &planv1.PlanItem{Provider: "shell"}, Tripped: true},
		{Item: &planv1.PlanItem{Provider: "fs"}, Tripped: true},
		{Item: &planv1.PlanItem{Provider: "shell"}, Tripped: true},
		{Item: &planv1.PlanItem{Provider: "http"}, Tripped: false},
	}}
	got := d.TrippedProviders()
	want := []string{"fs", "shell"}
	if len(got) != len(want) {
		t.Fatalf("TrippedProviders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TrippedProviders = %v, want %v", got, want)
		}
	}
	if len(Decisions{}.TrippedProviders()) != 0 {
		t.Error("an untripped plan reported tripped providers")
	}
}

// A data_source item routed through the plan path rather than Precheck
// still gets the policy engine's ask-to-deny downgrade — the gate never
// re-derives that rule, it just honors whatever Evaluate returned.
func TestDecide_dataSourceItemInAPlanIsStillDowngraded(t *testing.T) {
	t.Parallel()

	item := resourceItem("i1", "fs", "read_file")
	item.Kind = toolv1.ToolKind_TOOL_KIND_DATA_SOURCE

	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-reads", "fs", "read_file", policy.ActionAsk)},
		Resolver: fake.New(), // an empty queue: any Resolve call fails the test
	})

	d, err := g.Decide(context.Background(), &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{item}})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if item.GetDecision() != planv1.PlanDecision_PLAN_DECISION_DENY {
		t.Errorf("decision = %v, want DENY from the policy engine's downgrade", item.GetDecision())
	}
	if len(d.Denied) != 1 {
		t.Fatalf("denied = %d, want 1", len(d.Denied))
	}
}

func TestDecide_errors(t *testing.T) {
	t.Parallel()

	t.Run("nil plan", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(t, Config{})
		if _, err := g.Decide(context.Background(), nil); !errors.Is(err, ErrNilItem) {
			t.Fatalf("Decide(nil) err = %v, want ErrNilItem", err)
		}
	})

	t.Run("nil item", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(t, Config{})
		plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{nil}}
		if _, err := g.Decide(context.Background(), plan); !errors.Is(err, ErrNilItem) {
			t.Fatalf("Decide err = %v, want ErrNilItem", err)
		}
	})

	t.Run("resolver failure", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(t, Config{
			Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
			Resolver: fake.NewAlways(fake.Response{Err: errFake}),
		})
		plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
		if _, err := g.Decide(context.Background(), plan); !errors.Is(err, errFake) {
			t.Fatalf("Decide err = %v, want the resolver's error", err)
		}
	})

	t.Run("non-terminal resolver verdict", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(t, Config{
			Rules: []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
			Resolver: fake.NewAlways(fake.Response{Decision: plandecision.Decision{
				Decision:  planv1.PlanDecision_PLAN_DECISION_ASK,
				DecidedBy: "tui",
			}}),
		})
		plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
		if _, err := g.Decide(context.Background(), plan); !errors.Is(err, plandecision.ErrNonTerminalDecision) {
			t.Fatalf("Decide err = %v, want ErrNonTerminalDecision", err)
		}
	})

	t.Run("append failure", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(t, Config{Events: &recordingSink{planErr: errFake}})
		if _, err := g.Decide(context.Background(), threeProviderPlan()); !errors.Is(err, errFake) {
			t.Fatalf("Decide err = %v, want the sink's error", err)
		}
	})
}

// A missing catalog must not turn a legitimate corrected_input into a
// turn-level error — the re-validation simply has no schema to check.
func TestDecide_correctedInputWithoutACatalogIsAccepted(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{
		Rules: []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: fake.NewAlways(fake.Response{Decision: plandecision.Decision{
			Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:          planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
			CorrectedInput: mustStruct(map[string]any{"path": 42}),
			DecidedBy:      "tui",
		}}),
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), plan); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if plan.GetItems()[0].GetInput().GetFields()["path"].GetNumberValue() != 42 {
		t.Error("the correction was not applied when no schema was available to check it")
	}
}

// An unresolvable operation is logged and treated as "no schema", not
// escalated into a decision failure.
func TestDecide_unknownOperationFallsBackToNoSchema(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{
		Rules:    []policy.Rule{ruleFor("confirm-writes", "fs", "write_file", policy.ActionAsk)},
		Resolver: fake.NewAlways(allowDecision(planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE)),
		Tools:    &fakeTools{handles: map[string]providercatalog.ToolHandle{}},
	})

	plan := &planv1.Plan{TurnId: "turn-1", Items: []*planv1.PlanItem{resourceItem("i1", "fs", "write_file")}}
	if _, err := g.Decide(context.Background(), plan); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if plan.GetItems()[0].GetDecision() != planv1.PlanDecision_PLAN_DECISION_ALLOW {
		t.Error("an unresolvable operation blocked an otherwise valid approval")
	}
}
