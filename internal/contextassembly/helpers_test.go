package contextassembly

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"google.golang.org/grpc"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/tokencount"
)

// textBlock builds a single-text-block ContentBlock, mirroring
// tokencount's own test helper of the same name.
func textBlock(s string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: s}}}
}

// nonTextBlock builds a ContentBlock that is not a text block, for the
// v1 "text-only" rejection tests.
func nonTextBlock() *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_ToolUse{ToolUse: &contentv1.ToolUseBlock{Name: "x"}}}
}

// section builds a *contentv1.ContextSection owned by provider, with the
// given label and content blocks.
func section(provider, label string, blocks ...*contentv1.ContentBlock) *contentv1.ContextSection {
	return &contentv1.ContextSection{
		Provider:  provider,
		Label:     label,
		Content:   blocks,
		Stability: contentv1.Stability_STABILITY_STATIC,
	}
}

// noopModelLookup is a tokencount.ModelLookup that never resolves a
// model client — every Count call this package makes therefore uses the
// one canonical Fallback formula, making expected token counts a simple
// function of text length across every test in this file.
type noopModelLookup struct{}

func (noopModelLookup) ModelClientByLocalName(string) (modelv1.ModelServiceClient, bool) {
	return nil, false
}

// testLogger returns a discarding *slog.Logger — this package's tests
// assert on telemetry (spans/metrics), not on log content, so a fake
// slog.Handler recorder isn't needed here.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testAssembler returns an Assembler wired to the given events sink, a
// real tokencount.Counter backed by noopModelLookup (so every token count
// is the deterministic Fallback formula), and a real telemetry.Provider
// backed by a fresh fake.Backend for assertions.
func testAssembler(t *testing.T, events EventSink) (*Assembler, *fake.Backend) {
	t.Helper()
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"
	backend := fake.New()
	prov, err := telemetry.New(t.Context(), cfg, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	counter := tokencount.NewCounter(noopModelLookup{}, prov, testLogger())
	return New(Config{
		Tokens:    counter,
		Events:    events,
		Telemetry: prov,
		Logger:    testLogger(),
	}), backend
}

// fakeEventSink is a hand-written EventSink fake recording every
// AppendEvent call.
type fakeEventSink struct {
	mu     sync.Mutex
	events []statebackend.Event
	err    error
}

func (f *fakeEventSink) AppendEvent(_ context.Context, ev statebackend.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.events = append(f.events, ev)
	return int64(len(f.events)), nil
}

func (f *fakeEventSink) appended() []statebackend.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]statebackend.Event(nil), f.events...)
}

// fakeContextClient is a hand-written contextv1.ContextServiceClient
// fake. Only Contribute is meaningfully implemented — every other method
// panics if called, since Assembler never calls them.
type fakeContextClient struct {
	fn func(ctx context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error)

	mu    sync.Mutex
	calls []*contextv1.ContextRequest
}

var _ contextv1.ContextServiceClient = (*fakeContextClient)(nil)

func (f *fakeContextClient) Contribute(ctx context.Context, req *contextv1.ContextRequest, _ ...grpc.CallOption) (*contextv1.ContextContribution, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	return f.fn(ctx, req)
}

func (f *fakeContextClient) requests() []*contextv1.ContextRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*contextv1.ContextRequest(nil), f.calls...)
}

func (f *fakeContextClient) GetCapabilities(context.Context, *contextv1.GetCapabilitiesRequest, ...grpc.CallOption) (*contextv1.GetCapabilitiesResponse, error) {
	panic("fakeContextClient: GetCapabilities unexpectedly called")
}

func (f *fakeContextClient) Configure(context.Context, *contextv1.ConfigureRequest, ...grpc.CallOption) (*contextv1.ConfigureResponse, error) {
	panic("fakeContextClient: Configure unexpectedly called")
}

func (f *fakeContextClient) Render(context.Context, *contextv1.RenderRequest, ...grpc.CallOption) (*contextv1.RenderResponse, error) {
	panic("fakeContextClient: Render unexpectedly called")
}

func (f *fakeContextClient) Describe(context.Context, *contextv1.DescribeRequest, ...grpc.CallOption) (*contextv1.DescribeResponse, error) {
	panic("fakeContextClient: Describe unexpectedly called")
}

// contextHandle builds a providercatalog.ContextHandle for provider named
// name, at position pos, with the given token budget and compactor flag,
// backed by client.
func contextHandle(name string, pos int, budget int64, compactor bool, client contextv1.ContextServiceClient) providercatalog.ContextHandle {
	return providercatalog.ContextHandle{
		Provider: name,
		Producer: &commonv1.ProducerRef{Name: name, Category: commonv1.Category_CATEGORY_CONTEXT},
		Capabilities: &contextv1.ContextCapabilities{
			DefaultTokenBudget: budget,
			Stability:          contentv1.Stability_STABILITY_STATIC,
			Compactor:          compactor,
		},
		Client:      client,
		Position:    pos,
		TokenBudget: budget,
	}
}
