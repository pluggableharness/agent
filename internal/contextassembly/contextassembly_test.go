package contextassembly

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

func testModelTarget() *modelv1.ModelTarget {
	return &modelv1.ModelTarget{Id: "claude-x", ContextWindow: 200000, EffectiveCeiling: 100000}
}

// violationCount force-flushes prov's backend and sums the
// ContextContributionViolations series matching reason.
func violationCount(t *testing.T, backend *fake.Backend, assembler *Assembler, reason string) int64 {
	t.Helper()
	if err := assembler.telemetry.ForceFlush(t.Context()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "pluggableharness.context.contribution.violations" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "pluggableharness.context.violation_reason" && attr.Value.AsString() == reason {
						total += dp.Value
					}
				}
			}
		}
	}
	return total
}

func TestAssemble_multipleProvidersInOrder(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, _ := testAssembler(t, sink)

	var gitReq, claudeReq *contextv1.ContextRequest
	git := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		gitReq = req
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("git", "git status", textBlock("clean"))),
		}, nil
	}}
	claude := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		claudeReq = req
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("claude", "CLAUDE.md", textBlock("conventions"))),
		}, nil
	}}

	// Declared out of Position order on purpose -- Position, not slice
	// order, decides the chain (data-types.md#ordering--chaining).
	providers := []providercatalog.ContextHandle{
		contextHandle("claude", 1, 1000, false, claude),
		contextHandle("git", 0, 1000, false, git),
	}

	result, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{
		SessionID:   "sess-1",
		TurnID:      "turn-1",
		ModelTarget: testModelTarget(),
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Sections) != 2 {
		t.Fatalf("Sections = %d, want 2", len(result.Sections))
	}
	if result.Sections[0].GetProvider() != "git" || result.Sections[1].GetProvider() != "claude" {
		t.Fatalf("Sections order = [%s, %s], want [git, claude]", result.Sections[0].GetProvider(), result.Sections[1].GetProvider())
	}

	// git ran first (position 0): it must have seen an empty prior chain.
	if len(gitReq.GetPriorSections()) != 0 {
		t.Errorf("git's PriorSections = %d, want 0", len(gitReq.GetPriorSections()))
	}
	// claude ran second: it must have seen git's section already merged.
	if len(claudeReq.GetPriorSections()) != 1 || claudeReq.GetPriorSections()[0].GetProvider() != "git" {
		t.Errorf("claude's PriorSections = %v, want [git]", claudeReq.GetPriorSections())
	}

	if len(sink.appended()) != 2 {
		t.Fatalf("appended events = %d, want 2", len(sink.appended()))
	}
	for _, ev := range sink.appended() {
		if ev.Kind != kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION {
			t.Errorf("event kind = %v, want EVENT_KIND_CONTEXT_CONTRIBUTION", ev.Kind)
		}
	}
}

// TestAssemble_persistedEventUsesTheInjectedClock pins the persisted
// context_contribution event's timestamp to Config.Clock. This package was
// the only event-persisting package in the tree reading a bare time.Now()
// instead of an injected clock, which left its one persisted event's
// timestamp unpinnable in a test while eight sibling packages could pin
// theirs.
//
// The event id is asserted to agree with the same instant, because
// persistContribution reads the clock exactly once for both — reading it
// twice would let a ULID's embedded timestamp and the Timestamp column
// disagree by however long the marshal between them took.
func TestAssemble_persistedEventUsesTheInjectedClock(t *testing.T) {
	t.Parallel()

	pinned := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	sink := &fakeEventSink{}
	assembler, _ := testAssemblerWithClock(t, sink, func() time.Time { return pinned })

	git := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("git", "git status", textBlock("clean"))),
		}, nil
	}}

	if _, err := assembler.Assemble(t.Context(), []providercatalog.ContextHandle{
		contextHandle("git", 0, 1000, false, git),
	}, nil, TurnInputs{
		SessionID:   "sess-1",
		TurnID:      "turn-1",
		ModelTarget: testModelTarget(),
	}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	events := sink.appended()
	if len(events) != 1 {
		t.Fatalf("appended events = %d, want 1", len(events))
	}
	if !events[0].Timestamp.Equal(pinned) {
		t.Errorf("event Timestamp = %v, want the injected clock's %v", events[0].Timestamp, pinned)
	}
	if want := statebackend.NewEventID(pinned); events[0].ID[:10] != want[:10] {
		// A ULID's first 10 characters encode its millisecond timestamp;
		// the remaining 16 are entropy and differ per id by design.
		t.Errorf("event ID timestamp prefix = %q, want %q (id and Timestamp must come from one clock read)",
			events[0].ID[:10], want[:10])
	}
}

func TestAssemble_budgetViolationDropsSection(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, backend := testAssembler(t, sink)

	// "0123456789..." 40 bytes of text -> Fallback ceil(40/4) = 10 tokens,
	// exceeding a budget of 4.
	overBudget := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("big", "too big", textBlock("0123456789012345678901234567890123456789"))),
		}, nil
	}}

	providers := []providercatalog.ContextHandle{contextHandle("big", 0, 4, false, overBudget)}

	result, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Sections) != 0 {
		t.Fatalf("Sections = %d, want 0 (over-budget section dropped)", len(result.Sections))
	}
	if len(sink.appended()) != 0 {
		t.Fatalf("appended events = %d, want 0 -- a dropped section must not persist a contribution event", len(sink.appended()))
	}
	if got := violationCount(t, backend, assembler, "budget"); got != 1 {
		t.Errorf("budget violation count = %d, want 1", got)
	}
}

func TestAssemble_scopeViolationDiscardsWholeResponse(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, backend := testAssembler(t, sink)

	git := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("git", "git status", textBlock("clean"))),
		}, nil
	}}
	// rogue is a non-compactor that mutates git's already-contributed
	// section -- a scope violation (data-types.md#ordering--chaining).
	rogue := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		mutated := proto.Clone(req.GetPriorSections()[0]).(*contentv1.ContextSection)
		mutated.Label = "tampered"
		return &contextv1.ContextContribution{
			Sections: append([]*contentv1.ContextSection{mutated}, section("rogue", "rogue's own", textBlock("hi"))),
		}, nil
	}}

	providers := []providercatalog.ContextHandle{
		contextHandle("git", 0, 1000, false, git),
		contextHandle("rogue", 1, 1000, false, rogue),
	}

	result, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// rogue's entire response is discarded; the chain reverts to exactly
	// what it was before rogue's call -- git's section, unmutated.
	if len(result.Sections) != 1 {
		t.Fatalf("Sections = %d, want 1 (rogue's response discarded)", len(result.Sections))
	}
	if result.Sections[0].GetProvider() != "git" || result.Sections[0].GetLabel() != "git status" {
		t.Fatalf("Sections[0] = %+v, want git's original, unmutated section", result.Sections[0])
	}
	if len(sink.appended()) != 1 {
		t.Fatalf("appended events = %d, want 1 (only git's, not rogue's)", len(sink.appended()))
	}
	if got := violationCount(t, backend, assembler, "scope"); got != 1 {
		t.Errorf("scope violation count = %d, want 1", got)
	}
}

func TestAssemble_compactorMayRewriteOthersSections(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, _ := testAssembler(t, sink)

	git := &fakeContextClient{fn: func(_ context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: append(append([]*contentv1.ContextSection{}, req.GetPriorSections()...), section("git", "git status", textBlock("clean"))),
		}, nil
	}}
	// compactor merges git's section into its own summarized version --
	// legal only because it declares compactor: true.
	compactor := &fakeContextClient{fn: func(_ context.Context, _ *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections:         []*contentv1.ContextSection{section("compactor", "summary", textBlock("summarized"))},
			RewrittenHistory: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{textBlock("compacted history")}}},
		}, nil
	}}

	providers := []providercatalog.ContextHandle{
		contextHandle("git", 0, 1000, false, git),
		contextHandle("compactor", 1, 1000, true, compactor),
	}

	history := []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{textBlock("hello")}}}

	result, err := assembler.Assemble(t.Context(), providers, history, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Sections) != 1 || result.Sections[0].GetProvider() != "compactor" {
		t.Fatalf("Sections = %+v, want the compactor's merged section only", result.Sections)
	}
	if len(result.RewrittenHistory) != 1 || result.RewrittenHistory[0].GetContent()[0].GetText().GetText() != "compacted history" {
		t.Fatalf("RewrittenHistory = %+v, want the compactor's rewritten history", result.RewrittenHistory)
	}
}

func TestAssemble_historyAndAssembledTokens(t *testing.T) {
	t.Parallel()

	assembler, _ := testAssembler(t, &fakeEventSink{})

	client := &fakeContextClient{fn: func(_ context.Context, _ *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: []*contentv1.ContextSection{section("p", "label", textBlock("12345678"))}, // 8 bytes -> 2 tokens
		}, nil
	}}
	providers := []providercatalog.ContextHandle{contextHandle("p", 0, 1000, false, client)}

	history := []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{textBlock("1234")}}} // 4 bytes -> 1 token

	result, err := assembler.Assemble(t.Context(), providers, history, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget(), AssembledTokensLastTurn: 42})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if result.HistoryTokens != 1 {
		t.Errorf("HistoryTokens = %d, want 1", result.HistoryTokens)
	}
	if result.AssembledTokensLastTurn != 2 {
		t.Errorf("AssembledTokensLastTurn = %d, want 2 (this call's own assembled total)", result.AssembledTokensLastTurn)
	}

	// The threaded-in prior turn's total must reach every provider's
	// request untouched.
	if len(client.requests()) != 1 || client.requests()[0].GetAssembledTokensLastTurn() != 42 {
		t.Fatalf("provider's AssembledTokensLastTurn = %+v, want 42 threaded through from TurnInputs", client.requests())
	}
}

func TestAssemble_nonTextBlockRejected(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, backend := testAssembler(t, sink)

	client := &fakeContextClient{fn: func(_ context.Context, _ *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: []*contentv1.ContextSection{section("p", "label", nonTextBlock())},
		}, nil
	}}
	providers := []providercatalog.ContextHandle{contextHandle("p", 0, 1000, false, client)}

	result, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Fatalf("Sections = %d, want 0 (non-text section rejected)", len(result.Sections))
	}
	if len(sink.appended()) != 0 {
		t.Fatalf("appended events = %d, want 0", len(sink.appended()))
	}
	if got := violationCount(t, backend, assembler, "non_text"); got != 1 {
		t.Errorf("non_text violation count = %d, want 1", got)
	}
}

func TestAssemble_contributeRPCErrorAbortsChain(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{}
	assembler, _ := testAssembler(t, sink)

	wantErr := status.Error(codes.Unavailable, "plugin crashed")
	failing := &fakeContextClient{fn: func(context.Context, *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return nil, wantErr
	}}
	neverCalled := &fakeContextClient{fn: func(context.Context, *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		t.Fatal("neverCalled: Contribute must not be reached after an earlier provider's RPC error")
		return nil, nil
	}}

	providers := []providercatalog.ContextHandle{
		contextHandle("failing", 0, 1000, false, failing),
		contextHandle("later", 1, 1000, false, neverCalled),
	}

	_, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err == nil {
		t.Fatal("Assemble: want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Assemble error = %v, want wraps %v", err, wantErr)
	}
	if len(sink.appended()) != 0 {
		t.Fatalf("appended events = %d, want 0", len(sink.appended()))
	}
}

func TestAssemble_missingModelTarget(t *testing.T) {
	t.Parallel()

	assembler, _ := testAssembler(t, &fakeEventSink{})

	_, err := assembler.Assemble(t.Context(), nil, nil, TurnInputs{TurnID: "turn-1"})
	if !errors.Is(err, ErrMissingModelTarget) {
		t.Errorf("Assemble error = %v, want ErrMissingModelTarget", err)
	}
}

func TestAssemble_noProviders(t *testing.T) {
	t.Parallel()

	assembler, _ := testAssembler(t, &fakeEventSink{})

	result, err := assembler.Assemble(t.Context(), nil, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Errorf("Sections = %d, want 0", len(result.Sections))
	}
}

func TestAssemble_eventSinkFailureDoesNotFailTurn(t *testing.T) {
	t.Parallel()

	sink := &fakeEventSink{err: errors.New("disk full")}
	assembler, _ := testAssembler(t, sink)

	client := &fakeContextClient{fn: func(_ context.Context, _ *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
		return &contextv1.ContextContribution{
			Sections: []*contentv1.ContextSection{section("p", "label", textBlock("hi"))},
		}, nil
	}}
	providers := []providercatalog.ContextHandle{contextHandle("p", 0, 1000, false, client)}

	result, err := assembler.Assemble(t.Context(), providers, nil, TurnInputs{TurnID: "turn-1", ModelTarget: testModelTarget()})
	if err != nil {
		t.Fatalf("Assemble: %v, want no error even though the event sink failed", err)
	}
	if len(result.Sections) != 1 {
		t.Errorf("Sections = %d, want 1 -- a persistence failure must not roll back the assembled chain", len(result.Sections))
	}
}
