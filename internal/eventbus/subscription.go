package eventbus

import (
	"context"
	"log/slog"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Handler is invoked once per Event delivered to a Subscription, on that
// Subscription's own dedicated delivery goroutine — never on the
// publisher's goroutine (Publish always returns before delivery happens;
// see doc.go). The ctx passed to Handler is the Subscription's own
// lifetime-scoped context (derived from the ctx given to Subscribe), not
// the ctx the publisher happened to call Publish with — a slow Handler
// must not observe a publisher's transient, possibly-already-canceled
// ctx.
type Handler func(ctx context.Context, event Event)

// Subscription is one open registration on a Bus: a Topic, a Handler, and
// the unbounded queue plus delivery goroutine that feeds it. The zero
// value is not usable — obtain a Subscription from Bus.Subscribe.
type Subscription struct {
	bus     *Bus
	topic   string
	handler Handler

	ctx    context.Context
	cancel context.CancelFunc

	queue *queue
	done  chan struct{}

	queueWarnThreshold int
	warned             bool // guarded by queue's own mutex indirectly, via warnOnDepth's single-goroutine (deliverLoop) access

	logger    *slog.Logger
	telemetry *telemetry.Provider
}

// newSubscription builds a Subscription bound to bus, deriving its own
// lifetime context from ctx. It does not start delivery — call start for
// that — so a caller can finish registering the Subscription in bus's
// registry before its delivery goroutine can possibly call back into
// bus.remove (see Bus.Subscribe's comment on why that ordering matters).
func newSubscription(ctx context.Context, bus *Bus, topic string, handler Handler, logger *slog.Logger, prov *telemetry.Provider, queueWarnThreshold int) *Subscription {
	subCtx, cancel := context.WithCancel(ctx)
	return &Subscription{
		bus:                bus,
		topic:              topic,
		handler:            handler,
		ctx:                subCtx,
		cancel:             cancel,
		queue:              newQueue(),
		done:               make(chan struct{}),
		queueWarnThreshold: queueWarnThreshold,
		logger:             logger,
		telemetry:          prov,
	}
}

// start launches the delivery goroutine. Must be called at most once, and
// only after the Subscription is already registered in bus's registry —
// see Bus.Subscribe.
func (s *Subscription) start() {
	go s.deliverLoop()
}

// enqueue adds event to s's queue for out-of-band delivery, warning once
// (a throttled WARN, not a repeat per event) if its depth crosses
// queueWarnThreshold. enqueue itself never blocks — it is Publish's fast,
// per-subscriber fan-out step.
func (s *Subscription) enqueue(ctx context.Context, event Event) {
	s.queue.push(event)

	if s.queueWarnThreshold <= 0 || s.warned {
		return
	}
	if depth := s.queue.len(); depth >= s.queueWarnThreshold {
		s.warned = true
		s.logger.WarnContext(ctx, "eventbus: subscriber queue depth crossed warn threshold",
			"topic", s.topic, "depth", depth, "threshold", s.queueWarnThreshold)
	}
}

// deliverLoop drains s's queue, invoking handler for each Event, until
// s.ctx is canceled (by Close, or by the ctx originally passed to
// Subscribe being canceled/expiring). It always removes s from bus's
// registry and closes s.done before returning, regardless of which of
// those triggered the exit, so Close is correct no matter who initiated
// it.
func (s *Subscription) deliverLoop() {
	defer func() {
		s.bus.remove(s)
		close(s.done)
	}()

	for {
		for {
			event, ok := s.queue.pop()
			if !ok {
				break
			}
			s.invoke(event)
			// Once depth drops back below threshold, allow a future
			// crossing to warn again instead of staying silently
			// throttled forever.
			if s.warned && s.queue.len() < s.queueWarnThreshold {
				s.warned = false
			}
		}

		select {
		case <-s.queue.signal:
			continue
		case <-s.ctx.Done():
			// Per doc.go's vaporize contract, any Events still queued at
			// this point are discarded, not drained — closing a
			// subscription (or its Bus) is not a flush.
			return
		}
	}
}

// invoke calls s.handler for event, recovering a panic from it so one
// misbehaving subscriber can't kill its own delivery goroutine (and, via
// an unrecovered panic propagating up an unrelated goroutine, potentially
// the whole process) — the same "one slow/broken subscriber never affects
// another" guarantee this package already gives for delivery timing.
func (s *Subscription) invoke(event Event) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(s.ctx, "eventbus: subscriber handler panicked", "topic", s.topic, "panic", r)
		}
	}()

	s.handler(s.ctx, event)
	if s.telemetry.Instruments().EventBusEventsDelivered != nil {
		s.telemetry.Instruments().EventBusEventsDelivered.Add(s.ctx, 1)
	}
}

// Close cancels s's own context and waits for its delivery goroutine to
// finish handling any in-flight Event and exit, discarding anything still
// queued (doc.go's vaporize contract). Close is idempotent:
// context.CancelFunc is safe to call more than once, and a closed channel
// is safe to receive from more than once, so no additional guard is
// needed to make repeated or concurrent Close calls safe. Close never
// returns a non-nil error; it returns error only so callers can defer it
// uniformly with other Close-like methods in this codebase.
func (s *Subscription) Close() error {
	s.cancel()
	<-s.done
	return nil
}
