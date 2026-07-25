package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/hookpayload"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/turn"
)

// compile-time anchor: the concrete turn driver satisfies this package's
// TurnDriver with no adapter code. If turn.Driver's RunTurn signature ever
// drifts, this fails to build rather than failing at a wiring site.
var _ TurnDriver = (*turn.Driver)(nil)

// compile-time anchor: the concrete hook dispatcher satisfies this
// package's HookDispatcher with no adapter code.
var _ HookDispatcher = (*hookdispatch.Dispatcher)(nil)

// step is one scripted RunTurn outcome.
type step struct {
	result turn.Result
	err    error
}

// scriptedTurn is a TurnDriver returning a scripted sequence of results
// across successive calls. Once the script is exhausted it repeats its
// last step forever, so a scenario that needs "keep going until a bound
// fires" scripts one non-done step rather than counting turns by hand.
type scriptedTurn struct {
	mu    sync.Mutex
	steps []step
	calls []turn.Request
	// onCall, when set, runs before each result is returned — used to
	// advance a test clock, cancel a context, or inspect live grant state
	// mid-loop.
	onCall func(n int, req turn.Request)
}

// RunTurn implements TurnDriver.
func (s *scriptedTurn) RunTurn(_ context.Context, req turn.Request) (turn.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.calls)
	s.calls = append(s.calls, req)
	if s.onCall != nil {
		s.onCall(n, req)
	}

	if len(s.steps) == 0 {
		return turn.Result{History: req.History, Done: true}, nil
	}
	next := s.steps[min(n, len(s.steps)-1)]
	if next.err != nil {
		return turn.Result{}, next.err
	}
	result := next.result
	if result.History == nil {
		result.History = append(append([]*contentv1.Message{}, req.History...), assistantMessage(req.TurnIndex))
	}
	if result.Message == nil {
		result.Message = result.History[len(result.History)-1]
	}
	return result, nil
}

// requests returns a copy of every turn.Request the driver received.
func (s *scriptedTurn) requests() []turn.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]turn.Request{}, s.calls...)
}

// assistantMessage builds the placeholder assistant message a scripted
// turn appends to history.
func assistantMessage(index int) *contentv1.Message {
	return &contentv1.Message{
		Role: contentv1.Role_ROLE_ASSISTANT,
		Id:   fmt.Sprintf("msg-%d", index),
		Content: []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "ok"}},
		}},
	}
}

// running is a scripted step for a turn that made tool calls and did not
// end the loop.
func running(costUSD float64, hashes ...string) step {
	return step{result: turn.Result{
		CostUSD:    costUSD,
		CallHashes: hashes,
		Usage:      &modelv1.Usage{InputTokens: 10, OutputTokens: 5},
	}}
}

// done is a scripted step for a turn that ended the loop.
func done(costUSD float64) step {
	return step{result: turn.Result{
		CostUSD:    costUSD,
		Usage:      &modelv1.Usage{InputTokens: 10, OutputTokens: 5},
		Done:       true,
		DoneReason: turn.DoneNoToolCalls,
	}}
}

// startRecord is one observed session-start payload, copied into plain Go
// fields — a generated message carries a mutex and must not be copied by
// value (govet's copylocks).
type startRecord struct {
	sessionID        string
	profile          string
	workingDirectory string
	hasParent        bool
}

// endRecord is one observed session-end payload, same plain-fields
// reasoning as startRecord.
type endRecord struct {
	sessionID string
	status    sessionv1.SessionStatus
}

// recordingHooks is a HookDispatcher recording every dispatched point.
type recordingHooks struct {
	mu     sync.Mutex
	points []commonv1.HookPoint
	ends   []endRecord
	starts []startRecord
	err    error
}

// Dispatch implements HookDispatcher.
func (h *recordingHooks) Dispatch(_ context.Context, payload *hookv1.HookPayload) (hookdispatch.Outcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	point, _ := hookpayload.Point(payload)
	h.points = append(h.points, point)
	if start := payload.GetSessionStart(); start != nil {
		h.starts = append(h.starts, startRecord{
			sessionID:        start.GetSessionId(),
			profile:          start.GetProfile(),
			workingDirectory: start.GetWorkingDirectory(),
			hasParent:        start.ParentSessionId != nil,
		})
	}
	if end := payload.GetSessionEnd(); end != nil {
		h.ends = append(h.ends, endRecord{sessionID: end.GetSessionId(), status: end.GetStatus()})
	}
	if h.err != nil {
		return hookdispatch.Outcome{}, h.err
	}
	return hookdispatch.Outcome{Payload: payload, Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW}, nil
}

// dispatched returns a copy of the hook points seen so far.
func (h *recordingHooks) dispatched() []commonv1.HookPoint {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]commonv1.HookPoint{}, h.points...)
}

// testClock is a manually advanced clock, so the wall-clock bound is
// exercised without a sleep and without depending on real elapsed time.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

// newTestClock returns a clock pinned to a fixed instant.
func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)}
}

// Now returns the current pinned instant.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond) // keep minted ULIDs monotonic and distinct
	return c.now
}

// advance moves the clock forward by d.
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// modelProducer is the fake model plugin every harness registers.
func modelProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_MODEL, Name: "anthropic", Version: "1.0.0"}
}

// toolProducer is the fake tool plugin the tool-scoping scenarios register.
func toolProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_TOOL, Name: "filesystem", Version: "2.0.0"}
}

// contextProducer is the fake context plugin every harness registers.
func contextProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_CONTEXT, Name: "workspace", Version: "3.0.0"}
}

// testModelRef is the ref the harness catalog's single model is loaded
// under.
var testModelRef = agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4-8"}

// newCatalog builds the fake catalog every harness uses: one model, one
// context provider, and — when withTool is set — one tool operation.
func newCatalog(withTool bool) *fake.Catalog {
	cat := fake.New()
	cat.AddModel(testModelRef, providercatalog.ModelHandle{
		Producer: modelProducer(),
		Spec: &modelv1.ModelSpec{
			Id:              testModelRef.ID,
			ContextWindow:   200000,
			SupportsToolUse: true,
		},
	})
	cat.AddContext(providercatalog.ContextHandle{Provider: "workspace", Producer: contextProducer()})
	if withTool {
		cat.AddTool("filesystem", "read_file", providercatalog.ToolHandle{
			Producer: toolProducer(),
			Schema:   &toolv1.ToolSchema{Name: "read_file"},
		})
	}
	return cat
}

// harness is one fully wired Runner plus the collaborators a test asserts
// against.
type harness struct {
	runner  *Runner
	turns   *scriptedTurn
	hooks   *recordingHooks
	scopes  *sessionscope.Registry
	table   *sessionstate.Table
	store   *statebackend.Store
	catalog *fake.Catalog
	clock   *testClock
}

// newHarness wires a Runner over a real state-backend store in t.TempDir,
// a real sessionscope registry, a real live-session table, a real event
// bus, and the scripted turn driver / recording hook dispatcher above.
//
// Real sqlite over a temp dir stays inside the unit tier's bounds, the
// same reasoning internal/sessionstate's and internal/statebackend's own
// tests already apply.
func newHarness(t *testing.T, profiles map[string]agentprofile.AgentProfile, steps []step, withTool bool) *harness {
	t.Helper()

	store, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	h := &harness{
		turns:   &scriptedTurn{steps: steps},
		hooks:   &recordingHooks{},
		scopes:  sessionscope.NewRegistry(),
		table:   sessionstate.NewTable(),
		store:   store,
		catalog: newCatalog(withTool),
		clock:   newTestClock(),
	}

	runner, err := New(Config{
		Store:    h.store,
		Sessions: h.table,
		Scopes:   h.scopes,
		Bus:      eventbus.New(),
		Turn:     h.turns,
		Hooks:    h.hooks,
		Catalog:  h.catalog,
		Profiles: profiles,
		Clock:    h.clock.Now,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	h.runner = runner
	return h
}

// profileWith returns a one-entry profile map named "default" with limits
// applied on top of the builtin defaults.
func profileWith(mutate func(*agentprofile.AgentProfile)) map[string]agentprofile.AgentProfile {
	profile := BuiltinDefaultProfile()
	profile.Model = agentprofile.ModelBlock{Primary: testModelRef}
	mutate(&profile)
	return map[string]agentprofile.AgentProfile{DefaultProfileName: profile}
}

// assertNoOutstandingGrants fails when any plugin still holds a grant for
// sessionID.
func assertNoOutstandingGrants(t *testing.T, scopes *sessionscope.Registry, sessionID string) {
	t.Helper()
	for _, key := range []sessionscope.Key{
		sessionscope.KeyFor(modelProducer()),
		sessionscope.KeyFor(toolProducer()),
		sessionscope.KeyFor(contextProducer()),
	} {
		if scopes.Authorized(key, sessionID) {
			t.Fatalf("grant for %v/%s still outstanding after Run", key.Category, key.Name)
		}
	}
}
