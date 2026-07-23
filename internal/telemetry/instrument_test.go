package telemetry

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewInstruments(t *testing.T) {
	t.Parallel()

	meter := noop.NewMeterProvider().Meter("test")

	instruments, err := newInstruments(meter)
	if err != nil {
		t.Fatalf("newInstruments: %v", err)
	}
	if instruments == nil {
		t.Fatal("newInstruments returned nil Instruments with a nil error")
	}
}

// failingMeter wraps a real (noop) metric.Meter and forces Int64Counter to
// fail for one specific instrument name, so newInstruments's errors.Join
// aggregation path is actually exercised — a real meter never rejects any
// of this package's (valid, hand-picked) instrument names on its own.
type failingMeter struct {
	metric.Meter
	failName string
}

func (m failingMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == m.failName {
		return nil, errFailingMeter
	}
	return m.Meter.Int64Counter(name, opts...)
}

var errFailingMeter = errors.New("instrument_test: forced failure")

func TestNewInstruments_error(t *testing.T) {
	t.Parallel()

	meter := failingMeter{Meter: noop.NewMeterProvider().Meter("test"), failName: "pluggableharness.agent.tokens"}

	instruments, err := newInstruments(meter)
	if err == nil {
		t.Fatal("newInstruments: want error, got nil")
	}
	if instruments != nil {
		t.Fatalf("newInstruments: want nil Instruments alongside an error, got %+v", instruments)
	}
	if !errors.Is(err, errFailingMeter) {
		t.Errorf("newInstruments: error = %v, want errors.Is errFailingMeter", err)
	}
}

// Compile-time sanity that failingMeter still satisfies metric.Meter via
// embedding plus one overridden method.
var _ metric.Meter = failingMeter{}

func TestInstruments_smoke(t *testing.T) {
	t.Parallel()

	meter := noop.NewMeterProvider().Meter("test")
	instruments, err := newInstruments(meter)
	if err != nil {
		t.Fatalf("newInstruments: %v", err)
	}

	ctx := context.Background()
	// A noop meter's instruments accept any call without panicking or
	// erroring — this just exercises every field is populated and usable.
	instruments.Turns.Add(ctx, 1)
	instruments.Tokens.Add(ctx, 1)
	instruments.CostUSD.Add(ctx, 0.01)
	instruments.ToolCalls.Add(ctx, 1)
	instruments.BoundsFired.Add(ctx, 1)
	instruments.DoomLoops.Add(ctx, 1)
	instruments.PolicyDecisions.Add(ctx, 1)
	instruments.HookErrors.Add(ctx, 1)
	instruments.PluginCrashes.Add(ctx, 1)
	instruments.TurnDuration.Record(ctx, 1.0)
	instruments.ModelDuration.Record(ctx, 1.0)
	instruments.ToolDuration.Record(ctx, 1.0)
	instruments.HookDuration.Record(ctx, 1.0)
	instruments.ActiveSessions.Add(ctx, 1)
}
