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

// The three Append* methods below are the KERNEL-INTERNAL write path, and
// they are what every kernel-side collaborator (internal/modelcall,
// internal/tooldispatch, internal/plangate, internal/hookdispatch,
// internal/contextassembly) persists through. Each takes an already-built
// statebackend.Event rather than an EmitRecord, and that difference is the
// whole point: those callers assign their own event identity and their own
// timestamp (internal/modelcall deliberately reuses the kernel-assigned
// message id as the event id, per its own notes), so a method that minted
// a fresh one would overwrite a decision the caller already made.
//
// They deliberately do NOT debit the budget tracker. The session driver
// debits exactly once per turn from turn.Result.CostUSD
// (internal/session's absorb); debiting here as well would count every
// completion's cost twice. Budget.Debit stays the session driver's job —
// see internal/session/CLAUDE.md.
//
// Their signatures are exactly *statebackend.Session's own Append*
// signatures, which is what lets a *Live be dropped in wherever those five
// packages declare their event-sink interface, with no adapter and no call
// site change — and it is what routes every kernel-originated event
// through the bus republish below, rather than straight to sqlite.

// AppendEvent persists ev verbatim and republishes it onto
// kernel.event.{kind} after the commit succeeds.
func (l *Live) AppendEvent(ctx context.Context, ev statebackend.Event) (_ int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmit(ctx, l.id, ev.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: append event", "session_id", l.id, "kind", ev.Kind)

	seq, appendErr := l.session.AppendEvent(ctx, ev)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: append event: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: append event: failed", "session_id", l.id, "err", err)
		return 0, err
	}

	l.republish(ctx, ev.ID, seq, ev.Kind, ev.SchemaVersion, ev.Payload, ev.Timestamp)
	return seq, nil
}

// AppendMessage persists ev and its cost_ledger row in one transaction
// (state-backend.md requires cost_ledger populated at the same time as the
// message event that produced it), then republishes.
func (l *Live) AppendMessage(ctx context.Context, ev statebackend.Event, cost statebackend.CostEntry) (_ int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmitMessage(ctx, l.id, ev.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: append message", "session_id", l.id)

	seq, appendErr := l.session.AppendMessage(ctx, ev, cost)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: append message: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: append message: failed", "session_id", l.id, "err", err)
		return 0, err
	}

	l.republish(ctx, ev.ID, seq, ev.Kind, ev.SchemaVersion, ev.Payload, ev.Timestamp)
	return seq, nil
}

// AppendPlan persists ev and every plan_items row in one transaction
// (state-backend.md#plan_items), then republishes.
func (l *Live) AppendPlan(ctx context.Context, ev statebackend.Event, items []statebackend.PlanItem) (_ int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx, span := l.telem.StartSessionStateEmitPlan(ctx, l.id, ev.Producer)
	defer func() { telemetry.EndSpan(span, err) }()
	l.logger.DebugContext(ctx, "sessionstate: append plan", "session_id", l.id, "item_count", len(items))

	seq, appendErr := l.session.AppendPlan(ctx, ev, items)
	if appendErr != nil {
		err = fmt.Errorf("sessionstate: append plan: %w", appendErr)
		l.logger.ErrorContext(ctx, "sessionstate: append plan: failed", "session_id", l.id, "err", err)
		return 0, err
	}

	l.republish(ctx, ev.ID, seq, ev.Kind, ev.SchemaVersion, ev.Payload, ev.Timestamp)
	return seq, nil
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
