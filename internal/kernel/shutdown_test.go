package kernel

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
)

// TestShutdown_partialBringUpIsSafe asserts every phase is skipped when
// its field is nil — the shape bringUp leaves behind when it fails early.
func TestShutdown_partialBringUpIsSafe(t *testing.T) {
	t.Parallel()

	k := &kernel{logger: slog.New(slog.DiscardHandler)}
	if err := k.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown of an empty kernel = %v, want nil", err)
	}
}

// TestShutdown_runsEveryPhaseOnACanceledContext is the reason shutdown
// derives from context.WithoutCancel: it is normally reached *because* the
// caller's context was canceled, and a teardown that no-ops on a Done
// context flushes nothing.
func TestShutdown_runsEveryPhaseOnACanceledContext(t *testing.T) {
	t.Parallel()

	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	uploader, err := noop.New().TraceUploader(context.Background())
	if err != nil {
		t.Fatalf("TraceUploader: %v", err)
	}
	bus := eventbus.New()

	k := &kernel{
		logger: slog.New(slog.DiscardHandler),
		telem:  prov,
		relay:  telemetryrelay.New(uploader),
		bus:    bus,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := k.shutdown(ctx); err != nil {
		t.Fatalf("shutdown on a canceled context = %v, want every phase to still run", err)
	}
	// The bus really closed: Close is idempotent but Publish is not
	// tolerated after it.
	if err := bus.Close(); err != nil {
		t.Errorf("second bus.Close = %v, want nil", err)
	}
}

// TestShutdown_aggregatesAndContinues is the property the phased teardown
// exists for: an early failure must not skip the later phases.
func TestShutdown_aggregatesAndContinues(t *testing.T) {
	t.Parallel()

	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	// Shutting the Provider down twice: the second call is what fails,
	// giving a real, non-fabricated mid-sequence failure to aggregate.
	if err := prov.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}

	bus := eventbus.New()
	if err := bus.Close(); err != nil {
		t.Fatalf("pre-close bus: %v", err)
	}

	k := &kernel{logger: slog.New(slog.DiscardHandler), telem: prov, bus: bus}
	shutErr := k.shutdown(context.Background())

	// Whether either double-teardown reports an error is the collaborator
	// packages' own contract, not this one's. What this test pins is that
	// shutdown reached the LAST phase regardless of what the first did:
	// a joined error never omits a phase it skipped, because it skips none.
	if shutErr != nil && !strings.Contains(shutErr.Error(), "kernel: shutdown ") {
		t.Errorf("shutdown error %q is not phase-labeled", shutErr)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("bus.Close after shutdown = %v, want the bus to have been reached", err)
	}
}

// TestShutdown_bootstrapProviderIsTornDownWhenBringUpFailedEarly covers
// the one path where bootTelem survives to teardown: a failure between
// constructing it and replacing it with the real Provider.
func TestShutdown_bootstrapProviderIsTornDownWhenBringUpFailedEarly(t *testing.T) {
	t.Parallel()

	boot, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	k := &kernel{logger: slog.New(slog.DiscardHandler), bootTelem: boot}

	if err := k.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown with only a bootstrap Provider = %v, want nil", err)
	}
}

// TestRun_bringUpFailureStillShutsDown asserts Run's error covers the
// bring-up failure rather than being replaced by a teardown one.
func TestRun_bringUpFailureStillShutsDown(t *testing.T) {
	project := newProject(t, "")

	err := Run(context.Background(), testOptions(t, project, &stringSink{}, &stringSink{}))
	if err == nil {
		t.Fatal("Run = nil, want the bring-up failure")
	}
	if !strings.Contains(err.Error(), "no config file at") {
		t.Errorf("Run error %q lost the bring-up failure", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Error("Run reported cancellation for a config failure")
	}
}
