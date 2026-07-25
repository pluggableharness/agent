package plangate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/fake"
	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// errFake is the sentinel every fake in this file fails with, so a test
// can assert on the failure path with errors.Is rather than a string.
var errFake = errors.New("plangate_test: fake failure")

// discardLogger keeps test output clean; a Gate always logs, and none of
// these tests assert on log records.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixedClock returns a clock pinned to a fixed instant. Timestamps are
// display-only and never ordering-authoritative (determinism.md), so a
// test never needs a moving one.
func fixedClock() func() time.Time {
	at := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

// fakeHooks is the HookDispatcher test double: it returns one scripted
// outcome for every Dispatch call and records the payloads it saw.
type fakeHooks struct {
	mu      sync.Mutex
	calls   []*hookv1.HookPayload
	outcome HookOutcome
	err     error
}

// allowHooks returns a dispatcher whose chain always allows.
func allowHooks() *fakeHooks {
	return &fakeHooks{outcome: HookOutcome{Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW}}
}

// vetoHooks returns a dispatcher whose chain denies, attributed to
// provider.
func vetoHooks(provider string) *fakeHooks {
	return &fakeHooks{outcome: HookOutcome{
		Decision: hookv1.HookDecision_HOOK_DECISION_DENY,
		DeniedBy: provider,
	}}
}

func (f *fakeHooks) Dispatch(_ context.Context, p *hookv1.HookPayload) (HookOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	if f.err != nil {
		return HookOutcome{}, f.err
	}
	out := f.outcome
	out.Payload = p
	return out, nil
}

func (f *fakeHooks) dispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

var _ HookDispatcher = (*fakeHooks)(nil)

// recordedPlan is one AppendPlan call the sink observed.
type recordedPlan struct {
	event statebackend.Event
	items []statebackend.PlanItem
}

// recordingSink is the PlanSink test double.
type recordingSink struct {
	mu       sync.Mutex
	plans    []recordedPlan
	events   []statebackend.Event
	planErr  error
	eventErr error
	seq      int64
}

func (s *recordingSink) AppendPlan(_ context.Context, ev statebackend.Event, items []statebackend.PlanItem) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.planErr != nil {
		return 0, s.planErr
	}
	s.plans = append(s.plans, recordedPlan{event: ev, items: append([]statebackend.PlanItem(nil), items...)})
	s.seq++
	return s.seq, nil
}

func (s *recordingSink) AppendEvent(_ context.Context, ev statebackend.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventErr != nil {
		return 0, s.eventErr
	}
	s.events = append(s.events, ev)
	s.seq++
	return s.seq, nil
}

func (s *recordingSink) onlyPlan(t *testing.T) recordedPlan {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.plans) != 1 {
		t.Fatalf("AppendPlan calls = %d, want exactly 1", len(s.plans))
	}
	return s.plans[0]
}

var _ PlanSink = (*recordingSink)(nil)

// fakeTools is the ToolResolver test double, keyed "provider.tool".
type fakeTools struct {
	handles map[string]providercatalog.ToolHandle
}

func (f *fakeTools) Tool(provider, tool string) (providercatalog.ToolHandle, error) {
	h, ok := f.handles[provider+"."+tool]
	if !ok {
		return providercatalog.ToolHandle{}, providercatalog.ErrNotFound
	}
	return h, nil
}

var _ ToolResolver = (*fakeTools)(nil)

// stubToolClient implements only Preview; embedding the generated
// interface leaves every other method nil, which is exactly right — a test
// that reaches one has a bug, and a nil-method panic names it immediately.
type stubToolClient struct {
	toolv1.ToolServiceClient
	preview *renderv1.RenderTree
	err     error
	delay   time.Duration
	mu      sync.Mutex
	calls   int
}

func (s *stubToolClient) Preview(ctx context.Context, _ *toolv1.PreviewRequest, _ ...grpc.CallOption) (*toolv1.PreviewResponse, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return &toolv1.PreviewResponse{Preview: s.preview}, nil
}

// previewTree is a minimal, non-nil RenderTree — enough to tell "a preview
// was stored" from "preview stayed absent".
func previewTree() *renderv1.RenderTree {
	return &renderv1.RenderTree{Root: &renderv1.RenderNode{}}
}

// resourceItem builds a PENDING TOOL_KIND_RESOURCE plan item.
func resourceItem(id, provider, operation string) *planv1.PlanItem {
	return &planv1.PlanItem{
		Id:               id,
		CallId:           "call-" + id,
		Provider:         provider,
		OperationName:    operation,
		Input:            mustStruct(map[string]any{"path": "/tmp/x"}),
		Decision:         planv1.PlanDecision_PLAN_DECISION_PENDING,
		Kind:             toolv1.ToolKind_TOOL_KIND_RESOURCE,
		Risk:             toolv1.RiskClass_RISK_CLASS_MODERATE,
		Description:      operation,
		ProducerCategory: commonv1.Category_CATEGORY_TOOL,
	}
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(err)
	}
	return s
}

// ruleFor builds a single policy rule matching one provider's operation.
func ruleFor(name, provider, tool string, action policy.Action) policy.Rule {
	return policy.Rule{
		Name:   name,
		Match:  policy.Match{Provider: &provider, ToolName: &tool},
		Action: action,
	}
}

// newTestGate builds a Gate over the supplied collaborators with the
// noisy, environment-dependent bits pinned.
func newTestGate(t *testing.T, cfg Config, opts ...Option) *Gate {
	t.Helper()
	if cfg.SessionID == "" {
		cfg.SessionID = "sess-test"
	}
	if cfg.Hooks == nil {
		cfg.Hooks = allowHooks()
	}
	if cfg.Resolver == nil {
		cfg.Resolver = fake.NewAlways(fake.Response{Decision: plandecision.Decision{
			Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:     frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
			DecidedBy: "test",
		}})
	}
	if cfg.Events == nil {
		cfg.Events = &recordingSink{}
	}
	opts = append([]Option{WithLogger(discardLogger()), WithClock(fixedClock())}, opts...)
	return New(cfg, opts...)
}

// allowDecision is the resolver response most tests want.
func allowDecision(scope frontendv1.PlanDecisionScope) fake.Response {
	return fake.Response{Decision: plandecision.Decision{
		Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
		Scope:     scope,
		DecidedBy: "frontend",
	}}
}

func TestNew_requiresCollaborators(t *testing.T) {
	t.Parallel()

	tests := map[string]Config{
		"no hooks":    {Resolver: fake.New(), Events: &recordingSink{}},
		"no resolver": {Hooks: allowHooks(), Events: &recordingSink{}},
		"no events":   {Hooks: allowHooks(), Resolver: fake.New()},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatal("New did not panic on a missing required collaborator")
				}
			}()
			New(cfg)
		})
	}
}

func TestOptions(t *testing.T) {
	t.Parallel()

	telem, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}

	g := New(Config{Hooks: allowHooks(), Resolver: fake.New(), Events: &recordingSink{}},
		WithLogger(discardLogger()),
		WithTelemetry(telem),
		WithPreviewTimeout(42*time.Second),
		WithClock(fixedClock()),
	)
	if g.telem != telem {
		t.Error("WithTelemetry did not take effect")
	}
	if g.previewTimeout != 42*time.Second {
		t.Errorf("previewTimeout = %v, want 42s", g.previewTimeout)
	}

	// Every option ignores a zero/nil argument rather than clobbering a
	// working default with one.
	beforeTelem, beforeLogger, beforeTimeout := g.telem, g.logger, g.previewTimeout
	WithTelemetry(nil)(g)
	WithLogger(nil)(g)
	WithClock(nil)(g)
	WithPreviewTimeout(0)(g)
	if g.telem != beforeTelem || g.logger != beforeLogger || g.previewTimeout != beforeTimeout {
		t.Error("a zero-valued option overwrote a configured value")
	}
	if g.clock == nil {
		t.Error("WithClock(nil) cleared the clock")
	}
}

func TestDecidedBy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rule string
		want string
	}{
		{rule: "", want: "policy:default"},
		{rule: "deny-writes", want: "policy:deny-writes"},
	}
	for _, tt := range tests {
		if got := decidedBy(tt.rule); got != tt.want {
			t.Errorf("decidedBy(%q) = %q, want %q", tt.rule, got, tt.want)
		}
	}
	if got := hookVetoDecidedBy("guard"); got != "hook-veto:guard" {
		t.Errorf("hookVetoDecidedBy = %q, want %q", got, "hook-veto:guard")
	}
}

// schemaWithRequiredPath is an input schema a corrected_input must satisfy
// — used to prove an invalid correction is rejected rather than coerced.
func schemaWithRequiredPath() *schemav1.Schema {
	return &schemav1.Schema{
		Type:       schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Properties: map[string]*schemav1.Schema{"path": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING}},
		Required:   []string{"path"},
	}
}
