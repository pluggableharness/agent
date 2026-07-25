package tokencount

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// fakeHandler is a hand-written slog.Handler fake (go-testing.md: fakes,
// not mocking frameworks) that captures every Record it receives, mirroring
// internal/pluginruntime's, internal/log's, and internal/kernelcallback's
// own fakeHandler. Guarded by a mutex (unlike those siblings) because this
// package's own TestCounter_Count_concurrent deliberately drives the same
// logger from many goroutines at once, per go-testing.md's race-safety
// requirement for concurrency-sensitive code.
type fakeHandler struct {
	minLevel slog.Level

	mu      sync.Mutex
	records []slog.Record
}

func (h *fakeHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *fakeHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *fakeHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *fakeHandler) WithGroup(_ string) slog.Handler      { return h }

// hasLevel reports whether h captured any record at exactly level.
func (h *fakeHandler) hasLevel(level slog.Level) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level {
			return true
		}
	}
	return false
}

// testLogger returns a *slog.Logger writing to a fresh fakeHandler at
// DEBUG-and-up (so every DEBUG/WARN/ERROR call this package makes is
// captured), plus the handler itself for assertions.
func testLogger() (*slog.Logger, *fakeHandler) {
	h := &fakeHandler{minLevel: slog.LevelDebug}
	return slog.New(h), h
}

// testProvider returns a *telemetry.Provider wired to a fresh fake backend
// (internal/telemetry/drivers/fake), mirroring internal/registry's own
// helpers_test.go testProvider helper.
func testProvider(t *testing.T) *telemetry.Provider {
	t.Helper()
	prov, _ := testProviderWithBackend(t)
	return prov
}

// testProviderWithBackend returns the same Provider testProvider does, plus
// the fake.Backend it's wired to, for a test that also needs to assert on
// recorded metrics (force-flush the Provider, then read
// backend.Metrics.Collect).
func testProviderWithBackend(t *testing.T) (*telemetry.Provider, *fake.Backend) {
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
	return prov, backend
}

// fakeModelClient is a hand-written modelv1.ModelServiceClient fake
// (go-testing.md: fakes, not mocking frameworks), implementing only
// CountTokens meaningfully — every other method panics if called, since
// internal/tokencount never calls them and a call would indicate a bug in
// this package, not a legitimate test scenario. calls is mutex-guarded
// because TestCounter_Count_concurrent deliberately drives one
// fakeModelClient from many goroutines at once.
type fakeModelClient struct {
	// countTokensFunc is invoked by CountTokens. If nil, CountTokens
	// panics — every test that exercises the RPC path sets this.
	countTokensFunc func(ctx context.Context, in *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error)

	mu sync.Mutex
	// calls records every CountTokens invocation's request, for tests
	// that assert on call count/content.
	calls []*modelv1.CountTokensRequest
}

var _ modelv1.ModelServiceClient = (*fakeModelClient)(nil)

func (f *fakeModelClient) CountTokens(ctx context.Context, in *modelv1.CountTokensRequest, _ ...grpc.CallOption) (*modelv1.CountTokensResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	f.mu.Unlock()
	if f.countTokensFunc == nil {
		panic("fakeModelClient: CountTokens called with no countTokensFunc set")
	}
	return f.countTokensFunc(ctx, in)
}

func (f *fakeModelClient) GetCapabilities(context.Context, *modelv1.GetCapabilitiesRequest, ...grpc.CallOption) (*modelv1.GetCapabilitiesResponse, error) {
	panic("fakeModelClient: GetCapabilities unexpectedly called")
}

func (f *fakeModelClient) Configure(context.Context, *modelv1.ConfigureRequest, ...grpc.CallOption) (*modelv1.ConfigureResponse, error) {
	panic("fakeModelClient: Configure unexpectedly called")
}

func (f *fakeModelClient) StreamCompletion(context.Context, *modelv1.StreamCompletionRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[modelv1.StreamEvent], error) {
	panic("fakeModelClient: StreamCompletion unexpectedly called")
}

func (f *fakeModelClient) Render(context.Context, *modelv1.RenderRequest, ...grpc.CallOption) (*modelv1.RenderResponse, error) {
	panic("fakeModelClient: Render unexpectedly called")
}

func (f *fakeModelClient) Describe(context.Context, *modelv1.DescribeRequest, ...grpc.CallOption) (*modelv1.DescribeResponse, error) {
	panic("fakeModelClient: Describe unexpectedly called")
}

// panickingClient is a modelv1.ModelServiceClient that panics on
// CountTokens unconditionally — used to prove memoization actually
// short-circuits the round trip (a provider marked unimplemented must
// never reach this client's CountTokens a second time).
type panickingClient struct{}

var _ modelv1.ModelServiceClient = panickingClient{}

func (panickingClient) CountTokens(context.Context, *modelv1.CountTokensRequest, ...grpc.CallOption) (*modelv1.CountTokensResponse, error) {
	panic("panickingClient: CountTokens must not be called after memoization")
}

func (panickingClient) GetCapabilities(context.Context, *modelv1.GetCapabilitiesRequest, ...grpc.CallOption) (*modelv1.GetCapabilitiesResponse, error) {
	panic("panickingClient: GetCapabilities unexpectedly called")
}

func (panickingClient) Configure(context.Context, *modelv1.ConfigureRequest, ...grpc.CallOption) (*modelv1.ConfigureResponse, error) {
	panic("panickingClient: Configure unexpectedly called")
}

func (panickingClient) StreamCompletion(context.Context, *modelv1.StreamCompletionRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[modelv1.StreamEvent], error) {
	panic("panickingClient: StreamCompletion unexpectedly called")
}

func (panickingClient) Render(context.Context, *modelv1.RenderRequest, ...grpc.CallOption) (*modelv1.RenderResponse, error) {
	panic("panickingClient: Render unexpectedly called")
}

func (panickingClient) Describe(context.Context, *modelv1.DescribeRequest, ...grpc.CallOption) (*modelv1.DescribeResponse, error) {
	panic("panickingClient: Describe unexpectedly called")
}

// fakeLookup is a hand-written tokencount.ModelLookup fake.
type fakeLookup struct {
	clients map[string]modelv1.ModelServiceClient
}

func newFakeLookup() *fakeLookup {
	return &fakeLookup{clients: make(map[string]modelv1.ModelServiceClient)}
}

// with registers client under name. name is a real parameter (every test
// in this package currently happens to use "anthropic", which is why
// unparam flags it) — a general-purpose fake fixture, not a single-use
// helper, so the name stays a parameter rather than a hardcoded literal.
//
//nolint:unparam // general-purpose fake API; see comment above.
func (l *fakeLookup) with(name string, client modelv1.ModelServiceClient) *fakeLookup {
	l.clients[name] = client
	return l
}

func (l *fakeLookup) ModelClientByLocalName(name string) (modelv1.ModelServiceClient, bool) {
	c, ok := l.clients[name]
	return c, ok
}

// unimplementedErr and canceledErr build status errors for the two
// codes-classified branches Count's resolution order distinguishes beyond
// plain success/generic-error.
func unimplementedErr() error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func canceledErr() error {
	return status.Error(codes.Canceled, "context canceled")
}

func unavailableErr() error {
	return status.Error(codes.Unavailable, "temporarily unavailable")
}
