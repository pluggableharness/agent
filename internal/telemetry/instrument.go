package telemetry

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// Instruments holds every metric instrument this package defines, created
// exactly once per Provider. A metric.Meter's Int64Counter/Float64Histogram
// constructors register a new instrument each call, so re-creating one per
// use (rather than once at Provider construction) would duplicate
// registrations against the same name.
//
// SessionDepth as an observable gauge is deliberately not included in v0 —
// an ObservableGauge needs a callback that reads live session-tree state
// at collection time, which only the kernel loop (not yet built) has.
// SessionDepthKey is still recorded per-span (span.go's StartSession);
// the aggregate gauge is a Phase 6 follow-up once that state exists to
// read. See CLAUDE.md.
type Instruments struct {
	Turns           metric.Int64Counter
	Tokens          metric.Int64Counter
	CostUSD         metric.Float64Counter
	ToolCalls       metric.Int64Counter
	BoundsFired     metric.Int64Counter
	DoomLoops       metric.Int64Counter
	PolicyDecisions metric.Int64Counter
	HookErrors      metric.Int64Counter
	PluginCrashes   metric.Int64Counter

	TurnDuration  metric.Float64Histogram
	ModelDuration metric.Float64Histogram
	ToolDuration  metric.Float64Histogram
	HookDuration  metric.Float64Histogram

	ActiveSessions metric.Int64UpDownCounter

	EventBusEventsPublished     metric.Int64Counter
	EventBusEventsDelivered     metric.Int64Counter
	EventBusSubscriptionsActive metric.Int64UpDownCounter
}

// newInstruments registers every instrument against meter. An error here
// is a programming error (a malformed instrument name or unit), not a
// runtime condition, but it's still returned rather than panicking so a
// test can assert on it directly.
func newInstruments(meter metric.Meter) (*Instruments, error) {
	var errs []error
	check := func(name string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	turns, err := meter.Int64Counter("pluggableharness.turns",
		metric.WithDescription("Turns executed."))
	check("pluggableharness.turns", err)

	tokens, err := meter.Int64Counter("pluggableharness.tokens",
		metric.WithDescription("Model tokens consumed, by token.type."))
	check("pluggableharness.tokens", err)

	costUSD, err := meter.Float64Counter("pluggableharness.cost.usd",
		metric.WithDescription("Modeled spend in USD."),
		metric.WithUnit("{USD}"))
	check("pluggableharness.cost.usd", err)

	toolCalls, err := meter.Int64Counter("pluggableharness.tool.calls",
		metric.WithDescription("Tool calls executed."))
	check("pluggableharness.tool.calls", err)

	boundsFired, err := meter.Int64Counter("pluggableharness.bounds.fired",
		metric.WithDescription("Loop bounds tripped (agent-loop.md §3.1)."))
	check("pluggableharness.bounds.fired", err)

	doomLoops, err := meter.Int64Counter("pluggableharness.doomloop.detected",
		metric.WithDescription("Doom-loop detections."))
	check("pluggableharness.doomloop.detected", err)

	policyDecisions, err := meter.Int64Counter("pluggableharness.policy.decisions",
		metric.WithDescription("Policy decisions, by decision."))
	check("pluggableharness.policy.decisions", err)

	hookErrors, err := meter.Int64Counter("pluggableharness.hook.errors",
		metric.WithDescription("Hook subscriber errors (observe mode never aborts the loop, but errors are still counted)."))
	check("pluggableharness.hook.errors", err)

	pluginCrashes, err := meter.Int64Counter("pluggableharness.plugin.crashes",
		metric.WithDescription("Plugin subprocess crashes, by producer."))
	check("pluggableharness.plugin.crashes", err)

	turnDuration, err := meter.Float64Histogram("pluggableharness.turn.duration",
		metric.WithDescription("Turn wall-clock duration."),
		metric.WithUnit("s"))
	check("pluggableharness.turn.duration", err)

	modelDuration, err := meter.Float64Histogram("pluggableharness.model.call.duration",
		metric.WithDescription("StreamCompletion call duration."),
		metric.WithUnit("s"))
	check("pluggableharness.model.call.duration", err)

	toolDuration, err := meter.Float64Histogram("pluggableharness.tool.duration",
		metric.WithDescription("Tool Invoke call duration."),
		metric.WithUnit("s"))
	check("pluggableharness.tool.duration", err)

	hookDuration, err := meter.Float64Histogram("pluggableharness.hook.dispatch.duration",
		metric.WithDescription("Hook dispatch duration, by hook.point."),
		metric.WithUnit("s"))
	check("pluggableharness.hook.dispatch.duration", err)

	activeSessions, err := meter.Int64UpDownCounter("pluggableharness.sessions.active",
		metric.WithDescription("Currently active sessions (root + sub-agent)."))
	check("pluggableharness.sessions.active", err)

	eventBusEventsPublished, err := meter.Int64Counter("pluggableharness.eventbus.events.published",
		metric.WithDescription("internal/eventbus Publish calls that reached at least the fan-out step (topic is never an attribute here — see EventBusTopicKey's cardinality rule)."))
	check("pluggableharness.eventbus.events.published", err)

	eventBusEventsDelivered, err := meter.Int64Counter("pluggableharness.eventbus.events.delivered",
		metric.WithDescription("internal/eventbus handler invocations — one per (event, subscriber) pair actually delivered."))
	check("pluggableharness.eventbus.events.delivered", err)

	eventBusSubscriptionsActive, err := meter.Int64UpDownCounter("pluggableharness.eventbus.subscriptions.active",
		metric.WithDescription("Currently open internal/eventbus subscriptions, across all topics."))
	check("pluggableharness.eventbus.subscriptions.active", err)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &Instruments{
		Turns:           turns,
		Tokens:          tokens,
		CostUSD:         costUSD,
		ToolCalls:       toolCalls,
		BoundsFired:     boundsFired,
		DoomLoops:       doomLoops,
		PolicyDecisions: policyDecisions,
		HookErrors:      hookErrors,
		PluginCrashes:   pluginCrashes,
		TurnDuration:    turnDuration,
		ModelDuration:   modelDuration,
		ToolDuration:    toolDuration,
		HookDuration:    hookDuration,
		ActiveSessions:  activeSessions,

		EventBusEventsPublished:     eventBusEventsPublished,
		EventBusEventsDelivered:     eventBusEventsDelivered,
		EventBusSubscriptionsActive: eventBusSubscriptionsActive,
	}, nil
}
