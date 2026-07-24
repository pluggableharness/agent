//go:build integration

package pluginruntime_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/pluginruntime"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fixtureBinary is built once in TestMain and reused by every test in
// this file, so the (comparatively slow, one-time) `go build` of the
// fixture doesn't count against any individual test's speed budget
// (go-testing.md: integration tests ≤5s each).
var fixtureBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pluginruntime-fixture-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pluginruntime: integration: mkdtemp:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	fixtureBinary = filepath.Join(dir, "fixture-plugin")
	cmd := exec.Command("go", "build", "-tags=integration", "-o", fixtureBinary, "./testdata/plugin")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "pluginruntime: integration: build fixture: %v\n%s", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// captureHandler is a hand-written, concurrency-safe slog.Handler fake
// (go-testing.md: fakes, not mocking frameworks) — concurrency-safe
// because the fixture's Log callback arrives on a background goroutine,
// concurrently with the test's own assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// hasFixtureLog reports whether the fixture's "fixture plugin started"
// Log callback has arrived, attributed to producer via
// internal/kernelcallback.Server's server-derived identity.
func (h *captureHandler) hasFixtureLog(producer *commonv1.ProducerRef) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != "fixture plugin started" {
			continue
		}
		var gotName, gotCategory bool
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "producer_name":
				gotName = a.Value.String() == producer.GetName()
			case "producer_category":
				gotCategory = a.Value.String() == producer.GetCategory().String()
			}
			return true
		})
		if gotName && gotCategory {
			return true
		}
	}
	return false
}

// newFixtureLaunch builds a Config for launching fixtureBinary, along
// with the captureHandler its kernelcallback.Server logs through.
func newFixtureLaunch(t *testing.T) (pluginruntime.Config, *captureHandler, *commonv1.ProducerRef) {
	t.Helper()

	h := &captureHandler{}
	logger := slog.New(h)
	producer := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "fixture",
		Version:  "0.0.1",
	}

	telemetryBackend := fake.New()
	prov, err := telemetry.New(context.Background(), telemetry.DefaultConfig, telemetryBackend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("telemetry.Shutdown: %v", err)
		}
	})

	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })

	cb := kernelcallback.NewServer(kernelcallback.Config{
		Log:            log.NewServer(logger),
		Producer:       producer,
		Telemetry:      prov,
		TelemetryRelay: telemetryrelay.New(telemetryBackend.RelayedSpans),
		Bus:            bus,
		Logger:         logger,
	})

	return pluginruntime.Config{
		BinaryPath: fixtureBinary,
		Producer:   producer,
		Callback:   cb,
		Telemetry:  prov,
		Logger:     logger,
	}, h, producer
}

// TestLaunch_realSubprocess is the primary integration assertion: a real
// subprocess launch, a category RPC round-tripping through Dispensed(),
// and the fixture's callback reaching internal/kernelcallback.Server with
// correct producer attribution — the version gate implicitly passes too,
// since Launch would have failed otherwise (see plan's "Testing" section).
func TestLaunch_realSubprocess(t *testing.T) {
	cfg, h, producer := newFixtureLaunch(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pl, err := pluginruntime.Launch(ctx, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		if err := pl.Close(closeCtx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if got := pl.Producer(); got != producer {
		t.Errorf("Producer() = %v, want the same *ProducerRef passed in Config", got)
	}

	client, ok := pl.Dispensed().(toolv1.ToolServiceClient)
	if !ok {
		t.Fatalf("Dispensed() = %T, want toolv1.ToolServiceClient", pl.Dispensed())
	}

	resp, err := client.GetSchema(ctx, &toolv1.GetSchemaRequest{})
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	tools := resp.GetTools()
	if len(tools) != 1 || tools[0].GetName() != "fixture_echo" {
		t.Fatalf("GetSchema: tools = %v, want exactly one fixture_echo", tools)
	}

	// The fixture's Log callback fires from a background goroutine on its
	// side (see testdata/plugin/main.go's GRPCServer) — poll for it
	// rather than assuming synchronous delivery, bounded well inside the
	// 5s integration-test budget.
	deadline := time.Now().Add(3 * time.Second)
	for !h.hasFixtureLog(producer) {
		if time.Now().After(deadline) {
			t.Fatal("fixture's Log callback never reached internal/kernelcallback.Server with correct producer attribution")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestLaunch_cancelContextTearsDownSubprocess confirms canceling the ctx
// passed to Launch tears the subprocess down: a subsequent RPC fails, and
// Close still returns promptly rather than hanging on a process that no
// longer exists.
func TestLaunch_cancelContextTearsDownSubprocess(t *testing.T) {
	cfg, _, _ := newFixtureLaunch(t)

	launchCtx, cancelLaunch := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLaunch()

	pl, err := pluginruntime.Launch(launchCtx, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	cancelLaunch() // tear the subprocess tree down
	time.Sleep(200 * time.Millisecond)

	client, ok := pl.Dispensed().(toolv1.ToolServiceClient)
	if !ok {
		t.Fatalf("Dispensed() = %T, want toolv1.ToolServiceClient", pl.Dispensed())
	}
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()
	if _, err := client.GetSchema(rpcCtx, &toolv1.GetSchemaRequest{}); err == nil {
		t.Error("GetSchema succeeded after canceling the launch context, want an error (subprocess torn down)")
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer closeCancel()
	if err := pl.Close(closeCtx); err != nil {
		t.Errorf("Close after cancellation: %v", err)
	}
}
