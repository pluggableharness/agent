package kernel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metricv1 "github.com/pluggableharness/agent/pkg/metric/proto/v1"
)

// Errors a malformed Observation is rejected with locally, rather than
// being sent for the kernel to reject — so the caller sees which
// observation was wrong instead of a batch-level failure.
var (
	// ErrMetricNameEmpty reports an observation with no metric name.
	ErrMetricNameEmpty = errors.New("kernel: metric name is empty")
	// ErrMetricKindUnset reports an observation that names no instrument
	// shape. The kernel cannot create an instrument without one.
	ErrMetricKindUnset = errors.New("kernel: metric kind is unset")
)

// Metrics records metric observations through the kernel's RecordMetrics
// relay (kernel-callbacks.md#recordmetrics).
//
// It is the metrics counterpart to SpanExporter. A plugin exports no
// telemetry off-process itself: observations travel to the kernel, which
// records them against an instrument named
// "plugin.{category}.{name}.{metric name}" using the calling plugin's
// server-derived identity — a plugin cannot claim to be another producer.
//
// Construct one with Client.Metrics. The zero value is not usable.
//
// # Why the kernel bounds attributes and this type does not
//
// A metric's attribute key set is bounded per instrument by the kernel
// (observability.md's tracing/metrics asymmetry), and a key beyond that
// bound is dropped with a throttled warning rather than rejected. That is
// the kernel's job precisely because cardinality is a property of the
// whole system's metric store, not of any one plugin — so this type
// forwards what it is given rather than second-guessing it. Keep
// attribute values low-cardinality anyway: an unbounded value (a session
// id, a request id) belongs on a span, never on a metric.
type Metrics struct {
	client    *Client
	sessionID string
}

// Metrics returns a recorder that relays observations through c.
func (c *Client) Metrics(opts ...MetricsOption) *Metrics {
	m := &Metrics{client: c}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// MetricsOption configures a Metrics recorder.
type MetricsOption func(*Metrics)

// WithMetricsSessionID attaches a session id to every batch this recorder
// sends, so observations are attributable to the session that caused
// them. Optional, matching RecordMetricsRequest's own rule.
func WithMetricsSessionID(sessionID string) MetricsOption {
	return func(m *Metrics) { m.sessionID = sessionID }
}

// Observation is one metric measurement.
//
// Exactly one of Int or Float carries the value; use the Count/Record
// helpers below rather than building this by hand unless you need an
// attribute set or a non-default unit.
type Observation struct {
	// Name is the metric's name, without the plugin prefix the kernel
	// adds. MUST be non-empty.
	Name string
	// Description is what this metric measures. MAY be empty.
	Description string
	// Unit is UCUM-style ("ms", "By", "1"). MAY be empty.
	Unit string
	// Kind is which instrument shape this belongs to. MUST be set.
	//
	// The kernel rejects an observation whose kind disagrees with a
	// previously-created instrument of the same name, so a metric's kind
	// is effectively fixed by its first use — pick it deliberately rather
	// than letting two call sites disagree.
	Kind metricv1.MetricKind
	// Int is an integer observation. Ignored when Float is set.
	Int int64
	// Float is a floating-point observation. Takes precedence over Int.
	Float *float64
	// Attributes are open-ended labels for this observation. Keep them
	// low-cardinality: the kernel bounds the key set per instrument and
	// drops what exceeds it.
	Attributes map[string]string
	// Time is when the observation occurred. Zero means now.
	Time time.Time
}

// Count records a monotonically increasing sum, e.g. a request count.
func (m *Metrics) Count(ctx context.Context, name string, value int64, attributes map[string]string) error {
	return m.Record(ctx, Observation{
		Name:       name,
		Kind:       metricv1.MetricKind_METRIC_KIND_COUNTER,
		Int:        value,
		Attributes: attributes,
	})
}

// Histogram records one observation to be aggregated into a distribution,
// e.g. a call duration.
//
// One observation, never a pre-aggregated bucket set: the kernel's own
// histogram instrument performs the aggregation, the same way an OTel
// Histogram's Record does on the reporting side.
func (m *Metrics) Histogram(ctx context.Context, name string, value float64, unit string, attributes map[string]string) error {
	return m.Record(ctx, Observation{
		Name:       name,
		Kind:       metricv1.MetricKind_METRIC_KIND_HISTOGRAM,
		Unit:       unit,
		Float:      &value,
		Attributes: attributes,
	})
}

// Record relays one observation.
//
// Prefer Count or Histogram for the common shapes; reach for this when an
// observation needs a description, an up-down counter, or an explicit
// timestamp.
func (m *Metrics) Record(ctx context.Context, obs Observation) error {
	return m.RecordBatch(ctx, obs)
}

// RecordBatch relays several observations in one call.
//
// Batching is the reason this exists alongside Record: each call is a
// round trip to the kernel, and a plugin recording per-request metrics
// should not pay one per metric. An empty batch is a no-op rather than an
// error — RecordMetricsRequest requires a non-empty batch, so sending one
// would be a guaranteed rejection for a caller whose loop simply found
// nothing to report.
func (m *Metrics) RecordBatch(ctx context.Context, observations ...Observation) error {
	if len(observations) == 0 {
		return nil
	}

	records := make([]*metricv1.MetricRecord, 0, len(observations))
	for i, obs := range observations {
		record, err := obs.toProto()
		if err != nil {
			return fmt.Errorf("kernel: record metrics: observation %d: %w", i, err)
		}
		records = append(records, record)
	}

	req := &kernelv1.RecordMetricsRequest{Metrics: records}
	if m.sessionID != "" {
		id := m.sessionID
		req.SessionId = &id
	}
	if _, err := m.client.raw.RecordMetrics(ctx, req); err != nil {
		return fmt.Errorf("kernel: record metrics: %w", err)
	}
	return nil
}

// toProto converts one observation into its wire form, rejecting a
// declaration the kernel would reject anyway — locally, where the caller
// can see which observation was wrong.
func (o Observation) toProto() (*metricv1.MetricRecord, error) {
	if o.Name == "" {
		return nil, ErrMetricNameEmpty
	}
	if o.Kind == metricv1.MetricKind_METRIC_KIND_UNSPECIFIED {
		return nil, fmt.Errorf("%w: %q", ErrMetricKindUnset, o.Name)
	}

	at := o.Time
	if at.IsZero() {
		at = time.Now()
	}

	record := &metricv1.MetricRecord{
		Name:        o.Name,
		Description: o.Description,
		Unit:        o.Unit,
		Kind:        o.Kind,
		Attributes:  o.Attributes,
		Time:        timestamppb.New(at),
	}
	if o.Float != nil {
		record.Value = &metricv1.MetricRecord_DoubleValue{DoubleValue: *o.Float}
	} else {
		record.Value = &metricv1.MetricRecord_IntValue{IntValue: o.Int}
	}
	return record, nil
}
