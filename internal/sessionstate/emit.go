package sessionstate

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// kernelEventTopicPrefix is the reserved bus namespace Emit republishes
// onto (docs/specifications/event-bus.md#the-kernel-namespace): "kernel.event."
// plus the persisted EventKind's lowercase text form
// (statebackend.EventKindText), e.g. "kernel.event.tool_call".
const kernelEventTopicPrefix = "kernel.event."

// EmitRecord is one already-validated Emit call — validation (session_id
// non-empty and authorized, kind != EVENT_KIND_UNSPECIFIED, schema_version
// non-empty, payload non-nil, and the kernel-owned-kind rejection for
// EVENT_KIND_MESSAGE/EVENT_KIND_PLAN, per this package's own doc comment
// on EmitMessage/EmitPlan) is the CALLER's job (the future kernelcallback
// RPC handler) — this package assumes rec is already valid and focuses on
// the write-then-republish mechanics.
type EmitRecord struct {
	// Producer is the event's producer identity — server-derived by the
	// caller (kernel-callbacks.md#the-callback-channel: a plugin cannot
	// declare a producer identity other than its own), never
	// client-supplied.
	Producer *commonv1.ProducerRef
	// Kind identifies the event envelope's payload shape
	// (docs/specifications/state-backend.md#the-kind-enum).
	Kind kernelv1.EventKind
	// SchemaVersion versions the shape of Payload.
	SchemaVersion string
	// Payload is the opaque event body.
	Payload []byte
}

// EmitOutcome is the result of a successful Emit/EmitMessage/EmitPlan
// call: the assigned, storage-independent event id and the assigned
// ordering-authoritative sequence number (kernel-callbacks.md#emit's
// EmitResult).
type EmitOutcome struct {
	ID       string
	Sequence int64
}

// Emit persists rec and republishes it onto kernel.event.{kind} after the
// sqlite commit succeeds — a bus publish failure never fails the Emit
// itself (the durable write already happened; the bus is best-effort by
// construction, per event-bus.md#delivery-semantics). Uses
// statebackend.NewEventID for the id.
func (l *Live) Emit(ctx context.Context, rec EmitRecord) (_ EmitOutcome, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmit(ctx, l.id, rec.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: emit", "session_id", l.id, "kind", rec.Kind)

	now := l.clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          rec.Kind,
		Producer:      rec.Producer,
		SchemaVersion: rec.SchemaVersion,
		Payload:       rec.Payload,
	}

	seq, appendErr := l.session.AppendEvent(ctx, ev)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: emit: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: emit: append failed", "session_id", l.id, "err", err)
		return EmitOutcome{}, err
	}

	l.republish(ctx, ev.ID, seq, rec.Kind, rec.SchemaVersion, rec.Payload, now)
	return EmitOutcome{ID: ev.ID, Sequence: seq}, nil
}

// EmitMessage is the kernel-internal path for EVENT_KIND_MESSAGE events —
// it additionally writes a cost_ledger row in the same transaction (via
// statebackend.Session.AppendMessage) and debits this session's (and, via
// the parent link, every ancestor's) budget tracker. This method is NOT
// reachable from a plugin's Emit call — a future kernelcallback handler
// rejects EVENT_KIND_MESSAGE from a plugin-facing Emit and calls THIS
// method itself instead, since only the kernel's own model-call path
// produces message events (state-backend.md's conformance table requires
// cost_ledger populated "at the same time as the message event that
// produced it", which a generic plugin Emit path cannot guarantee).
func (l *Live) EmitMessage(ctx context.Context, rec EmitRecord, cost statebackend.CostEntry) (_ EmitOutcome, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmitMessage(ctx, l.id, rec.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: emit message", "session_id", l.id)

	now := l.clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          rec.Kind,
		Producer:      rec.Producer,
		SchemaVersion: rec.SchemaVersion,
		Payload:       rec.Payload,
	}

	seq, appendErr := l.session.AppendMessage(ctx, ev, cost)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: emit message: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: emit message: append failed", "session_id", l.id, "err", err)
		return EmitOutcome{}, err
	}

	l.budget.Debit(cost.CostUSD)
	l.republish(ctx, ev.ID, seq, rec.Kind, rec.SchemaVersion, rec.Payload, now)
	return EmitOutcome{ID: ev.ID, Sequence: seq}, nil
}

// EmitPlan is the analogous kernel-internal path for EVENT_KIND_PLAN,
// writing plan_items rows in the same transaction via
// statebackend.Session.AppendPlan. Also not reachable from a plugin's
// Emit — use statebackend.KernelProducer() as rec.Producer here (this is
// exactly the "kernel-synthesized event with no single owning plugin"
// case that producer identity exists to serve).
func (l *Live) EmitPlan(ctx context.Context, rec EmitRecord, items []statebackend.PlanItem) (_ EmitOutcome, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmitPlan(ctx, l.id, rec.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: emit plan", "session_id", l.id, "item_count", len(items))

	now := l.clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          rec.Kind,
		Producer:      rec.Producer,
		SchemaVersion: rec.SchemaVersion,
		Payload:       rec.Payload,
	}

	seq, appendErr := l.session.AppendPlan(ctx, ev, items)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: emit plan: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: emit plan: append failed", "session_id", l.id, "err", err)
		return EmitOutcome{}, err
	}

	l.republish(ctx, ev.ID, seq, rec.Kind, rec.SchemaVersion, rec.Payload, now)
	return EmitOutcome{ID: ev.ID, Sequence: seq}, nil
}

// republish builds the kernel.event.{kind} BusEvent for a just-persisted
// event and publishes it — called only after the sqlite append this
// event's id/sequence came from has already committed
// (kernel-callbacks.md#emit's write-then-republish ordering). A publish
// failure is logged at WARN with session_id/topic/sequence and otherwise
// swallowed: the durable write already succeeded, and event-bus.md's own
// contract makes the bus best-effort by design.
func (l *Live) republish(ctx context.Context, id string, seq int64, kind kernelv1.EventKind, schemaVersion string, payload []byte, at time.Time) {
	kindText, err := statebackend.EventKindText(kind)
	if err != nil {
		// Unreachable in practice: kind already passed the identical
		// encodeEventKind validation inside the AppendEvent/AppendMessage/
		// AppendPlan call that produced id/seq, above.
		l.logger.ErrorContext(ctx, "sessionstate: republish: unable to build topic", "session_id", l.id, "event_id", id, "err", err)
		return
	}
	payloadType, err := statebackend.EventPayloadType(kind)
	if err != nil {
		l.logger.ErrorContext(ctx, "sessionstate: republish: unable to resolve payload type", "session_id", l.id, "event_id", id, "err", err)
		return
	}

	topic := kernelEventTopicPrefix + kindText
	busEvent := &kernelv1.BusEvent{
		Topic:         topic,
		Payload:       payload,
		PayloadType:   payloadType,
		SchemaVersion: schemaVersion,
		Time:          timestamppb.New(at),
	}

	if pubErr := l.bus.Publish(ctx, eventbus.Event{Topic: topic, Payload: busEvent}); pubErr != nil {
		l.logger.WarnContext(ctx, "sessionstate: republish failed", "session_id", l.id, "event_id", id, "topic", topic, "sequence", seq, "err", pubErr)
		return
	}
	l.logger.DebugContext(ctx, "sessionstate: republished", "session_id", l.id, "event_id", id, "topic", topic, "sequence", seq)
}
