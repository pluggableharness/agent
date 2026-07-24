package telemetry

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MaxDynamicMetricAttributes bounds the number of attribute keys
// RecordDynamicMetric keeps per observation before recording it — a
// plugin-supplied metric.v1.MetricRecord.attributes map is open-ended by
// construction, and .claude/rules/logging-telemetry.md's cardinality rule
// is non-negotiable (specifications/observability.md#the-tracing-metrics-asymmetry).
// A key beyond this bound is dropped, not the whole observation, and the
// drop is counted via Instruments.RecordMetricsAttributesDropped rather
// than silently disappearing with no signal.
const MaxDynamicMetricAttributes = 8

// DynamicMetricKind selects which OTel instrument shape
// RecordDynamicMetric creates for a given name — the shape a plugin
// declares via metric.v1.MetricKind (kernelcallback translates the wire
// enum into this local type; this package intentionally does not import
// the kernel proto package to describe its own OTel-facing surface).
type DynamicMetricKind int

const (
	// DynamicMetricKindUnspecified is the zero value — never a valid
	// argument to RecordDynamicMetric.
	DynamicMetricKindUnspecified DynamicMetricKind = iota
	// DynamicMetricKindCounter is a monotonically increasing sum.
	DynamicMetricKindCounter
	// DynamicMetricKindUpDownCounter is a sum that can increase or
	// decrease.
	DynamicMetricKindUpDownCounter
	// DynamicMetricKindHistogram is one observation to be aggregated into
	// a distribution.
	DynamicMetricKindHistogram
)

// ErrDynamicMetricKindMismatch is returned by RecordDynamicMetric when a
// name is reused with a kind that disagrees with the instrument already
// created for it — kernel-callbacks.md's RecordMetrics documents this as
// a MUST reject, since OTel does not allow one instrument name to change
// shape mid-process.
var ErrDynamicMetricKindMismatch = fmt.Errorf("telemetry: dynamic metric: kind mismatch")

// ErrDynamicMetricKindUnspecified is returned by RecordDynamicMetric when
// kind is DynamicMetricKindUnspecified.
var ErrDynamicMetricKindUnspecified = fmt.Errorf("telemetry: dynamic metric: kind unspecified")

// dynamicInstrument is whichever one of the three OTel instrument types a
// given name was first created as — exactly one field is ever set,
// matching the kind it was created under.
type dynamicInstrument struct {
	kind      DynamicMetricKind
	counter   metric.Float64Counter
	updown    metric.Float64UpDownCounter
	histogram metric.Float64Histogram
}

// dynamicMetrics lazily creates and caches metric instruments by name, for
// RecordMetrics' plugin-declared observations — a case Instruments' fixed
// struct doesn't cover, since instrument names here are runtime/plugin-
// declared rather than a fixed compile-time set. Every dynamic instrument
// is Float64-shaped regardless of whether the originating observation was
// int64 or double-valued: OTel's own API splits Int64*/Float64* instrument
// families, and tracking both per name would double the bookkeeping for
// no practical benefit at this value range — a plugin's int64 count is
// losslessly representable as a float64 well past any realistic counter
// value.
type dynamicMetrics struct {
	meter metric.Meter

	mu          sync.Mutex
	instruments map[string]*dynamicInstrument
}

func newDynamicMetrics(meter metric.Meter) *dynamicMetrics {
	return &dynamicMetrics{
		meter:       meter,
		instruments: make(map[string]*dynamicInstrument),
	}
}

// getOrCreate returns the cached instrument for name, creating it against
// kind on first use. A second call for the same name with a different
// kind returns ErrDynamicMetricKindMismatch rather than silently reusing
// (or silently replacing) the original instrument.
func (d *dynamicMetrics) getOrCreate(name string, kind DynamicMetricKind) (*dynamicInstrument, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if inst, ok := d.instruments[name]; ok {
		if inst.kind != kind {
			return nil, fmt.Errorf("%w: %q was created as kind %d, called again as kind %d", ErrDynamicMetricKindMismatch, name, inst.kind, kind)
		}
		return inst, nil
	}

	inst := &dynamicInstrument{kind: kind}
	var err error
	switch kind {
	case DynamicMetricKindCounter:
		inst.counter, err = d.meter.Float64Counter(name)
	case DynamicMetricKindUpDownCounter:
		inst.updown, err = d.meter.Float64UpDownCounter(name)
	case DynamicMetricKindHistogram:
		inst.histogram, err = d.meter.Float64Histogram(name)
	case DynamicMetricKindUnspecified:
		return nil, ErrDynamicMetricKindUnspecified
	default:
		return nil, fmt.Errorf("telemetry: dynamic metric: unknown kind %d", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: dynamic metric: create %q: %w", name, err)
	}

	d.instruments[name] = inst
	return inst, nil
}

// boundAttributes converts attrs into attribute.KeyValue pairs, sorted by
// key for deterministic ordering (.claude/rules/determinism.md's
// no-map-iteration-order rule), truncated to MaxDynamicMetricAttributes
// entries. It reports how many keys were dropped, so the caller can
// increment a dropped-attributes counter and log once per call, not once
// per dropped key.
func boundAttributes(attrs map[string]string) (kvs []attribute.KeyValue, dropped int) {
	if len(attrs) == 0 {
		return nil, 0
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > MaxDynamicMetricAttributes {
		dropped = len(keys) - MaxDynamicMetricAttributes
		keys = keys[:MaxDynamicMetricAttributes]
	}

	kvs = make([]attribute.KeyValue, 0, len(keys))
	for _, k := range keys {
		kvs = append(kvs, attribute.String(k, attrs[k]))
	}
	return kvs, dropped
}

// RecordDynamicMetric records one plugin-declared metric observation
// against a lazily-created, per-name instrument on p's own MeterProvider
// — see specifications/observability.md#the-tracing-metrics-asymmetry for
// why this does not relay OTLP the way ExportSpans does. name is the
// fully-qualified instrument name ("plugin.{category}.{name}.{metric
// name}", server-derived by the caller from the authenticated callback
// connection — this method does not construct it and does not validate
// its shape). attrs is bounded to MaxDynamicMetricAttributes keys; excess
// keys are dropped and counted via Instruments().RecordMetricsAttributesDropped,
// never silently accepted.
func (p *Provider) RecordDynamicMetric(ctx context.Context, name string, kind DynamicMetricKind, value float64, attrs map[string]string) error {
	inst, err := p.dynamicMetrics.getOrCreate(name, kind)
	if err != nil {
		return err
	}

	kvs, dropped := boundAttributes(attrs)
	if dropped > 0 {
		p.instruments.RecordMetricsAttributesDropped.Add(ctx, int64(dropped))
	}
	opt := metric.WithAttributes(kvs...)

	switch kind {
	case DynamicMetricKindCounter:
		inst.counter.Add(ctx, value, opt)
	case DynamicMetricKindUpDownCounter:
		inst.updown.Add(ctx, value, opt)
	case DynamicMetricKindHistogram:
		inst.histogram.Record(ctx, value, opt)
	}
	return nil
}
