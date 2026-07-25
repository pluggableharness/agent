package sessionstate

import (
	"context"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/statebackend"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// fixedClock returns a clock func that always reports t — deterministic
// event IDs/timestamps for assertions (determinism.md).
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// testProducer returns a stable, real-category producer identity for test
// events that aren't exercising the reserved kernel producer path.
func testProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "test-tool",
		Version:  "1",
	}
}

// newTestSession creates a fresh *statebackend.Session in a t.TempDir()
// store, registering its Close via t.Cleanup.
func newTestSession(t *testing.T) *statebackend.Session {
	t.Helper()
	st, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessionID := statebackend.NewSessionID(time.Now())
	sess, err := st.Create(context.Background(), statebackend.SessionMeta{
		SessionID: sessionID,
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// newTestLive builds a *Live over a fresh test session and a fresh, live
// eventbus.Bus, both cleaned up via t.Cleanup. clock defaults to a fixed
// instant if now is the zero time.
func newTestLive(t *testing.T, limits bounds.Limits, parent *bounds.Tracker, now time.Time) (*Live, *eventbus.Bus) {
	t.Helper()
	sess := newTestSession(t)
	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })

	if now.IsZero() {
		now = time.Now()
	}
	live := NewLive(sess, bus, limits, parent, fixedClock(now), nil, nil)
	return live, bus
}

func TestNewLive_budgetIsUsable(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{MaxCostUSD: 10}, nil, time.Time{})

	if live.Budget() == nil {
		t.Fatal("Budget() = nil, want a usable *bounds.Tracker")
	}
	if got := live.Budget().TotalCostUSD(); got != 0 {
		t.Errorf("fresh Budget().TotalCostUSD() = %v, want 0", got)
	}
}

func TestLive_Close(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second Close (statebackend.Session.Close is documented idempotent)
	// must not panic or error.
	if err := live.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestNewLive_defaultsClockLoggerTelemetry exercises NewLive's nil-fallback
// branches directly (clock -> time.Now, logger -> slog.Default(), telem ->
// a disabled Provider) — newTestLive's helper always supplies a fixed
// clock, so this is the one place those defaults are exercised.
func TestNewLive_defaultsClockLoggerTelemetry(t *testing.T) {
	t.Parallel()
	sess := newTestSession(t)
	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })

	live := NewLive(sess, bus, bounds.Limits{}, nil, nil, nil, nil)
	if live.clock == nil {
		t.Fatal("clock default not applied")
	}
	if live.logger == nil {
		t.Fatal("logger default not applied")
	}
	if live.telem == nil {
		t.Fatal("telemetry default not applied")
	}

	// Exercise the defaulted clock/logger/telemetry through a real Emit.
	_, err := live.Emit(context.Background(), EmitRecord{
		Producer:      testProducer(),
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	})
	if err != nil {
		t.Fatalf("Emit with defaulted dependencies: %v", err)
	}
}
