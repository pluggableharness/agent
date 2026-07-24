package telemetry

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
)

func TestBoundAttributes(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		kvs, dropped := boundAttributes(nil)
		if kvs != nil || dropped != 0 {
			t.Fatalf("boundAttributes(nil) = %v, %d; want nil, 0", kvs, dropped)
		}
	})

	t.Run("under the bound", func(t *testing.T) {
		t.Parallel()
		kvs, dropped := boundAttributes(map[string]string{"b": "2", "a": "1"})
		if dropped != 0 {
			t.Fatalf("dropped = %d, want 0", dropped)
		}
		if len(kvs) != 2 {
			t.Fatalf("len(kvs) = %d, want 2", len(kvs))
		}
		// sorted by key, deterministically, regardless of map iteration order.
		if kvs[0].Key != "a" || kvs[1].Key != "b" {
			t.Fatalf("kvs = %+v, want sorted [a, b]", kvs)
		}
	})

	t.Run("over the bound", func(t *testing.T) {
		t.Parallel()
		attrs := make(map[string]string, MaxDynamicMetricAttributes+3)
		for i := range MaxDynamicMetricAttributes + 3 {
			attrs[string(rune('a'+i))] = "v"
		}
		kvs, dropped := boundAttributes(attrs)
		if dropped != 3 {
			t.Fatalf("dropped = %d, want 3", dropped)
		}
		if len(kvs) != MaxDynamicMetricAttributes {
			t.Fatalf("len(kvs) = %d, want %d", len(kvs), MaxDynamicMetricAttributes)
		}
		// The kept keys are the lexicographically first MaxDynamicMetricAttributes.
		if kvs[0].Key != "a" {
			t.Fatalf("kvs[0].Key = %q, want a", kvs[0].Key)
		}
	})
}

func TestDynamicMetrics_getOrCreate(t *testing.T) {
	t.Parallel()

	dm := newDynamicMetrics(noop.NewMeterProvider().Meter("test"))

	inst1, err := dm.getOrCreate("plugin.tool.github.calls", DynamicMetricKindCounter)
	if err != nil {
		t.Fatalf("getOrCreate: %v", err)
	}
	if inst1.counter == nil {
		t.Fatal("counter instrument not created")
	}

	inst2, err := dm.getOrCreate("plugin.tool.github.calls", DynamicMetricKindCounter)
	if err != nil {
		t.Fatalf("getOrCreate (second call, same kind): %v", err)
	}
	if inst1 != inst2 {
		t.Fatal("getOrCreate created a second instrument for the same name/kind instead of returning the cached one")
	}

	if _, err := dm.getOrCreate("plugin.tool.github.calls", DynamicMetricKindHistogram); !errors.Is(err, ErrDynamicMetricKindMismatch) {
		t.Fatalf("getOrCreate (kind mismatch) = %v, want ErrDynamicMetricKindMismatch", err)
	}

	if _, err := dm.getOrCreate("plugin.tool.github.other", DynamicMetricKindUnspecified); !errors.Is(err, ErrDynamicMetricKindUnspecified) {
		t.Fatalf("getOrCreate (unspecified kind) = %v, want ErrDynamicMetricKindUnspecified", err)
	}
}

func TestDynamicMetrics_upDownCounterAndHistogram(t *testing.T) {
	t.Parallel()

	dm := newDynamicMetrics(noop.NewMeterProvider().Meter("test"))

	updown, err := dm.getOrCreate("plugin.tool.github.active", DynamicMetricKindUpDownCounter)
	if err != nil {
		t.Fatalf("getOrCreate (up_down_counter): %v", err)
	}
	if updown.updown == nil {
		t.Fatal("up_down_counter instrument not created")
	}

	hist, err := dm.getOrCreate("plugin.tool.github.duration", DynamicMetricKindHistogram)
	if err != nil {
		t.Fatalf("getOrCreate (histogram): %v", err)
	}
	if hist.histogram == nil {
		t.Fatal("histogram instrument not created")
	}
}
