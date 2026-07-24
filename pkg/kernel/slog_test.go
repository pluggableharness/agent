package kernel_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/pkg/kernel"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

// logCapture records every LogRequest a fakeServer's Log method receives.
type logCapture struct {
	mu    sync.Mutex
	batch []*kernelv1.LogRequest
}

func (c *logCapture) record(req *kernelv1.LogRequest) (*kernelv1.LogResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batch = append(c.batch, req)
	return &kernelv1.LogResult{}, nil
}

func (c *logCapture) requests() []*kernelv1.LogRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*kernelv1.LogRequest, len(c.batch))
	copy(out, c.batch)
	return out
}

func (c *logCapture) totalEntries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, req := range c.batch {
		n += len(req.GetEntries())
	}
	return n
}

func TestSlogHandler_flushesOnMaxBatchSize(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithMaxBatchSize(2), kernel.WithFlushInterval(time.Hour))
	t.Cleanup(func() { _ = h.Close() })
	logger := slog.New(h)

	logger.Info("first")
	logger.Info("second") // crosses maxBatch=2, should flush immediately

	deadline := time.Now().Add(2 * time.Second)
	for capture.totalEntries() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := capture.totalEntries(); got != 2 {
		t.Fatalf("totalEntries() = %d, want 2", got)
	}
}

func TestSlogHandler_flushesOnTimer(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithFlushInterval(20 * time.Millisecond))
	t.Cleanup(func() { _ = h.Close() })
	logger := slog.New(h)

	logger.Info("one entry, well under any batch-size threshold")

	deadline := time.Now().Add(2 * time.Second)
	for capture.totalEntries() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := capture.totalEntries(); got != 1 {
		t.Fatalf("totalEntries() = %d, want 1 (timer-triggered flush)", got)
	}
}

func TestSlogHandler_closeFlushesRemaining(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithFlushInterval(time.Hour), kernel.WithMaxBatchSize(1000))
	logger := slog.New(h)

	logger.Info("pending at close")
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := capture.totalEntries(); got != 1 {
		t.Fatalf("totalEntries() after Close = %d, want 1", got)
	}
}

func TestSlogHandler_sessionID(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithSessionID("sess-123"), kernel.WithFlushInterval(time.Hour), kernel.WithMaxBatchSize(1000))
	logger := slog.New(h)
	logger.Info("hi")
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reqs := capture.requests()
	if len(reqs) != 1 || reqs[0].GetSessionId() != "sess-123" {
		t.Fatalf("requests = %+v, want one request with session_id=sess-123", reqs)
	}
}

func TestSlogHandler_enabledUsesKernelReportedLevel(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{
		getTelemetryConfigFunc: func(*kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
			return &kernelv1.GetTelemetryConfigResult{LogLevel: logv1.LogLevel_LOG_LEVEL_WARN}, nil
		},
	})
	if err := c.LoadTelemetryConfig(t.Context()); err != nil {
		t.Fatalf("LoadTelemetryConfig: %v", err)
	}

	h := c.NewSlogHandler()
	t.Cleanup(func() { _ = h.Close() })

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(Info) = true, want false (kernel-reported floor is WARN)")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Enabled(Warn) = false, want true")
	}
}

func TestSlogHandler_withLevelOverridesKernelFloor(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{
		getTelemetryConfigFunc: func(*kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
			return &kernelv1.GetTelemetryConfigResult{LogLevel: logv1.LogLevel_LOG_LEVEL_ERROR}, nil
		},
	})
	if err := c.LoadTelemetryConfig(t.Context()); err != nil {
		t.Fatalf("LoadTelemetryConfig: %v", err)
	}

	h := c.NewSlogHandler().WithLevel(slog.LevelDebug)
	t.Cleanup(func() { _ = h.Close() })

	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Enabled(Debug) = false, want true (WithLevel overrides the kernel-reported ERROR floor)")
	}
}

func TestSlogHandler_groupPrefixesPerCallAttrs(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithFlushInterval(time.Hour), kernel.WithMaxBatchSize(1000))
	logger := slog.New(h).WithGroup("req")
	logger.Info("handled", "method", "GET") // "method" arrives via Record.Attrs, not WithAttrs
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fields := capture.requests()[0].GetEntries()[0].GetFields().GetFields()
	if fields["req.method"].GetStringValue() != "GET" {
		t.Errorf("req.method = %v, want GET (Handle's own r.Attrs() loop must prefix with the active group)", fields["req.method"])
	}
}

func TestSlogHandler_withGroupEmptyNameIsNoop(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})
	h := c.NewSlogHandler()
	t.Cleanup(func() { _ = h.Close() })

	if h.WithGroup("") != h {
		t.Error("WithGroup(\"\") should return the same handler unchanged")
	}
}

func TestSlogHandler_withAttrsEmptyIsNoop(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})
	h := c.NewSlogHandler()
	t.Cleanup(func() { _ = h.Close() })

	if h.WithAttrs(nil) != h {
		t.Error("WithAttrs(nil) should return the same handler unchanged")
	}
}

func TestSlogHandler_withAttrsAndGroup(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	c := newTestClient(t, &fakeServer{logFunc: capture.record})

	h := c.NewSlogHandler(kernel.WithFlushInterval(time.Hour), kernel.WithMaxBatchSize(1000))
	logger := slog.New(h).With("base", "b").WithGroup("req").With("method", "GET")
	logger.Info("handled")
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reqs := capture.requests()
	if len(reqs) != 1 || len(reqs[0].GetEntries()) != 1 {
		t.Fatalf("requests = %+v, want one request with one entry", reqs)
	}
	fields := reqs[0].GetEntries()[0].GetFields().GetFields()
	if fields["base"].GetStringValue() != "b" {
		t.Errorf("base = %v, want b (no group prefix — set before WithGroup)", fields["base"])
	}
	if fields["req.method"].GetStringValue() != "GET" {
		t.Errorf("req.method = %v, want GET (prefixed by the active group)", fields["req.method"])
	}
}
