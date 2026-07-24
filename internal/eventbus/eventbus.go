package eventbus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// defaultQueueWarnThreshold is the queue depth WithQueueWarnThreshold
// defaults to when not overridden — a value large enough that a healthy
// subscriber keeping pace never approaches it, but small enough that a
// genuinely stuck subscriber is flagged well before its queue represents
// a serious amount of retained memory.
const defaultQueueWarnThreshold = 1024

// wildcardEntry is one Subscription's registration under one wildcard
// filter — prefix is that filter with its trailing "*" already stripped
// (filter.go's wildcardPrefix). Bus.wildcards holds one entry per
// (Subscription, wildcard filter) pair, scanned linearly by Publish;
// exact filters never appear here — they live in Bus.subs instead, which
// stays a direct map lookup.
type wildcardEntry struct {
	prefix string
	sub    *Subscription
}

// Bus is an ephemeral, in-process publish/subscribe fan-out — see doc.go
// for the full contract. The zero value is not usable; construct one with
// New.
type Bus struct {
	mu        sync.RWMutex
	subs      map[string]map[*Subscription]struct{} // exact topic filter -> that topic's open subscriptions
	wildcards []wildcardEntry                       // trailing-wildcard filters, scanned per Publish

	logger             *slog.Logger
	telemetry          *telemetry.Provider
	queueWarnThreshold int

	closed    atomic.Bool
	closeOnce sync.Once
}

// Option configures a Bus constructed by New.
type Option func(*Bus)

// WithLogger sets the *slog.Logger the Bus logs through. A nil logger (or
// omitting this option) leaves the default of slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(b *Bus) {
		if logger != nil {
			b.logger = logger
		}
	}
}

// WithTelemetry sets the *telemetry.Provider the Bus instruments through
// (internal/telemetry/span.go's StartEventBusPublish and
// internal/telemetry.Instruments' EventBus* fields). Omitting this option
// (or passing nil) leaves the default: a Provider with every signal
// disabled, so New wires OTel's own no-op providers directly — the
// instrumentation code path still runs on every call, at effectively zero
// cost, rather than being conditionally skipped (statebackend.go's
// defaultTelemetryProvider does the same, for the same reason).
func WithTelemetry(prov *telemetry.Provider) Option {
	return func(b *Bus) {
		if prov != nil {
			b.telemetry = prov
		}
	}
}

// WithQueueWarnThreshold sets the per-subscription queue depth at which
// Publish's fan-out logs a throttled WARN for that subscription (doc.go's
// unbounded-delivery design decision). A threshold of 0 disables the
// warning entirely. Omitting this option leaves defaultQueueWarnThreshold.
func WithQueueWarnThreshold(n int) Option {
	return func(b *Bus) {
		b.queueWarnThreshold = n
	}
}

// defaultTelemetryProvider builds the Provider a Bus falls back to when
// WithTelemetry isn't supplied, following statebackend.go's
// defaultTelemetryProvider exactly: every signal disabled, so
// telemetry.New never actually calls into the noop.Backend passed here.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// New returns a ready-to-use, empty Bus. Construction cannot fail from a
// caller's perspective on ordinary inputs, so New has no error return;
// the one internal fallible step (building the default telemetry
// Provider) can only fail on a telemetry.Config this package itself
// controls, which is why it panics rather than propagating an error
// through a signature every other package in this codebase expects to be
// infallible for a package with no required arguments.
func New(opts ...Option) *Bus {
	b := &Bus{
		subs:               make(map[string]map[*Subscription]struct{}),
		logger:             slog.Default(),
		queueWarnThreshold: defaultQueueWarnThreshold,
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.telemetry == nil {
		prov, err := defaultTelemetryProvider()
		if err != nil {
			// Unreachable in practice: defaultTelemetryProvider's
			// telemetry.Config{} is a fixed, valid zero value this
			// package controls end to end (see telemetry.Config.Validate)
			// — not a value that can vary with caller input.
			panic(fmt.Errorf("eventbus: new: %w", err))
		}
		b.telemetry = prov
	}
	return b
}

// Publish fans event out to every Subscription currently registered for
// event.Topic, then returns — it never blocks on delivery and never drops
// event for a slow subscriber (doc.go's design decisions). Publish
// returns ErrClosed if the Bus has already been closed, ErrEmptyTopic if
// event.Topic is empty, and otherwise never fails: a Topic with zero
// current subscribers is not an error, it is simply a Publish nobody
// heard.
func (b *Bus) Publish(ctx context.Context, event Event) error {
	if b.closed.Load() {
		return ErrClosed
	}
	if err := event.validate(); err != nil {
		return err
	}

	ctx, span := b.telemetry.StartEventBusPublish(ctx, event.Topic)
	defer func() { telemetry.EndSpan(span, nil) }()

	// Snapshot the current subscriber set under RLock, then enqueue
	// outside the lock — enqueue itself never blocks (queue.push), but
	// holding a lock across it would still needlessly serialize Publish
	// calls against Subscribe/unsubscribe for no benefit, and would let a
	// panic inside a misbehaving future enqueue path (there is none
	// today) hold the lock indefinitely.
	b.mu.RLock()
	topicSubs := b.subs[event.Topic]
	// seen dedupes a Subscription that matches via more than one
	// registration (an exact hit and a wildcard hit, or two overlapping
	// wildcard filters on the same Subscription) so it's enqueued exactly
	// once per Publish call, never once per matching filter.
	seen := make(map[*Subscription]struct{}, len(topicSubs))
	targets := make([]*Subscription, 0, len(topicSubs))
	for sub := range topicSubs {
		if _, dup := seen[sub]; dup {
			continue
		}
		seen[sub] = struct{}{}
		targets = append(targets, sub)
	}
	for _, entry := range b.wildcards {
		if _, dup := seen[entry.sub]; dup {
			continue
		}
		if strings.HasPrefix(event.Topic, entry.prefix) {
			seen[entry.sub] = struct{}{}
			targets = append(targets, entry.sub)
		}
	}
	b.mu.RUnlock()

	for _, sub := range targets {
		sub.enqueue(ctx, event)
	}

	b.logger.DebugContext(ctx, "eventbus: published", "topic", event.Topic, "subscribers", len(targets))
	b.telemetry.Instruments().EventBusEventsPublished.Add(ctx, 1)
	return nil
}

// Subscribe registers handler to receive every future Event published
// with the given topic, returning a Subscription the caller uses to
// unregister it (Subscription.Close). It is sugar for
// SubscribeFilters(ctx, []string{topic}, handler) — see that method for
// the full contract, including trailing-wildcard filter support.
//
// Subscribe returns ErrClosed if the Bus has already been closed,
// ErrEmptyTopic if topic is empty, and ErrNilHandler if handler is nil.
func (b *Bus) Subscribe(ctx context.Context, topic string, handler Handler) (*Subscription, error) {
	if topic == "" {
		return nil, ErrEmptyTopic
	}
	return b.SubscribeFilters(ctx, []string{topic}, handler)
}

// SubscribeFilters registers handler to receive every future Event whose
// Topic matches any of filters, returning a Subscription the caller uses
// to unregister it (Subscription.Close). handler runs on a dedicated
// delivery goroutine, out-of-band from any Publish call (Handler's doc
// comment). The subscription's lifetime is bounded by both ctx (canceling
// or letting ctx expire stops delivery, exactly as calling
// Subscription.Close would) and by an explicit Close call — whichever
// comes first.
//
// Each entry in filters is either an exact topic (matches only that exact
// string) or a trailing-wildcard filter ending in "*" (matches any topic
// sharing the filter's prefix — filter.go's isWildcardFilter/matchesFilter).
// A single Subscription may mix both kinds; an Event matching more than
// one of a Subscription's filters is still delivered to it exactly once
// per Publish call (Publish's own dedup), never once per matching filter.
//
// SubscribeFilters returns ErrClosed if the Bus has already been closed,
// ErrEmptyTopic if filters is empty or any entry is empty, and
// ErrNilHandler if handler is nil.
func (b *Bus) SubscribeFilters(ctx context.Context, filters []string, handler Handler) (*Subscription, error) {
	if len(filters) == 0 {
		return nil, ErrEmptyTopic
	}
	for _, f := range filters {
		if f == "" {
			return nil, ErrEmptyTopic
		}
	}
	if handler == nil {
		return nil, ErrNilHandler
	}

	sub := newSubscription(ctx, b, filters, handler, b.logger, b.telemetry, b.queueWarnThreshold)

	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	for _, f := range filters {
		if isWildcardFilter(f) {
			b.wildcards = append(b.wildcards, wildcardEntry{prefix: wildcardPrefix(f), sub: sub})
			continue
		}
		if b.subs[f] == nil {
			b.subs[f] = make(map[*Subscription]struct{})
		}
		b.subs[f][sub] = struct{}{}
	}
	b.mu.Unlock()

	// start is deliberately called only after the lock above is released:
	// its delivery goroutine can call back into b.remove (which itself
	// locks b.mu) as soon as sub's ctx is already canceled — starting it
	// while still holding the lock could deadlock against that same
	// goroutine's very first iteration.
	sub.start()

	b.logger.DebugContext(ctx, "eventbus: subscribed", "filters", filters)
	b.telemetry.Instruments().EventBusSubscriptionsActive.Add(ctx, 1)
	return sub, nil
}

// remove unregisters sub from b's registry — every exact-filter bucket
// and every wildcard entry it was registered under. Called exactly once
// per Subscription, from the end of its own deliverLoop — never called
// directly by Subscription.Close, which only signals and waits (see
// subscription.go). Safe to call after Bus.Close has already cleared
// b.subs: deleting from (and reading from) a nil map is a documented Go
// no-op, not a panic.
func (b *Bus) remove(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, f := range sub.filters {
		if isWildcardFilter(f) {
			continue // wildcard entries are pruned in the pass below
		}
		if set, ok := b.subs[f]; ok {
			delete(set, sub)
			if len(set) == 0 {
				delete(b.subs, f)
			}
		}
	}

	if len(b.wildcards) > 0 {
		kept := b.wildcards[:0]
		for _, entry := range b.wildcards {
			if entry.sub != sub {
				kept = append(kept, entry)
			}
		}
		b.wildcards = kept
	}

	b.telemetry.Instruments().EventBusSubscriptionsActive.Add(sub.ctx, -1)
}

// Close closes every open Subscription (discarding whatever each still
// has queued, per doc.go's vaporize contract) and marks the Bus itself
// closed, so every subsequent Publish or Subscribe call returns
// ErrClosed immediately. Close is idempotent — safe to call more than
// once — and always returns nil; it returns error only so callers can
// treat it uniformly with other Close-like methods in this codebase.
func (b *Bus) Close() error {
	b.closeOnce.Do(func() {
		b.closed.Store(true)

		b.mu.Lock()
		subs := make([]*Subscription, 0)
		for _, set := range b.subs {
			for sub := range set {
				subs = append(subs, sub)
			}
		}
		b.mu.Unlock()

		// Each Close call below waits for that Subscription's delivery
		// goroutine to fully exit (subscription.go's Close), and that
		// goroutine's own exit calls b.remove — so by the time this loop
		// finishes, every subscriber's goroutine is gone and b.subs (read
		// again, if ever, by a stray call already in flight) reflects an
		// empty registry.
		for _, sub := range subs {
			_ = sub.Close()
		}

		b.logger.Info("eventbus: closed")
	})
	return nil
}
