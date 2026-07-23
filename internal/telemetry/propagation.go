package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
)

// Propagator is the W3C Trace Context propagator
// (https://www.w3.org/TR/trace-context/) this package uses for crossing
// the go-plugin gRPC boundary in both directions (grpchooks.go). Baggage
// is deliberately not included in v0 — nothing in this system needs
// cross-process baggage yet, and carrying an unused propagator only grows
// every carrier's wire size for no benefit.
var Propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{})

// Inject writes ctx's span context into carrier using Propagator, for a
// caller managing its own carrier (e.g. gRPC metadata.MD via
// propagation.MapCarrier) rather than going through otelgrpc's stats
// handler, which does this automatically.
func Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	Propagator.Inject(ctx, carrier)
}

// Extract reads a remote span context out of carrier using Propagator,
// returning a context a subsequent span can be started as a child of.
func Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return Propagator.Extract(ctx, carrier)
}
