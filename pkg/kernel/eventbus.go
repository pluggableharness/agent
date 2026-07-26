package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// Publish emits one event onto the event bus (event-bus.md) under a topic
// the kernel constructs from this plugin's own server-derived identity —
// a plugin never supplies its own topic (kernel-callbacks.md#publish).
// Returns the fully-resolved topic on success.
func (c *Client) Publish(ctx context.Context, eventType string, payload []byte, payloadType, schemaVersion string) (string, error) {
	result, err := c.raw.Publish(ctx, &kernelv1.PublishRequest{
		EventType:     eventType,
		Payload:       payload,
		PayloadType:   payloadType,
		SchemaVersion: schemaVersion,
	})
	if err != nil {
		return "", fmt.Errorf("kernel: publish: %w", err)
	}
	return result.GetTopic(), nil
}

// BusEventHandler is invoked once per *kernelv1.BusEvent a Subscription
// receives, on that Subscription's own dedicated receive goroutine —
// never on the caller's own goroutine.
type BusEventHandler func(event *kernelv1.BusEvent)

// Subscription represents an open plugin-side subscription to the event
// bus, opened via Client.Subscribe. The zero value is not usable.
type Subscription struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Close stops this Subscription's receive goroutine and waits for it to
// exit before returning. Idempotent — safe to call more than once, since
// context.CancelFunc is documented-safe to call repeatedly and a closed
// channel is safe to receive from repeatedly (the same reasoning
// internal/eventbus.Subscription.Close already documents for its own,
// analogous Close).
func (s *Subscription) Close() error {
	s.cancel()
	<-s.done
	return nil
}

// runSubscription drives recv into handler until the stream ends, then
// closes done and releases the subscription's own context.
//
// Both Subscribe and ReadEvents share this rather than each inlining the
// loop, because the ordering of the two deferred cleanups is the whole
// point and duplicating it invites the two from drifting: cancel MUST run
// even when the stream ends on its own, not only when Close cancels it.
// A caller is not obliged to call Close once delivery has stopped — for
// ReadEvents, whose stream is naturally finite, that is the documented
// normal case — so without this the derived context would stay attached to
// its parent for the parent's whole lifetime, and repeated calls under one
// long-lived plugin context would accumulate.
//
// Calling cancel here and again from Close is harmless:
// context.CancelFunc is documented-safe to call repeatedly.
//
// The two defers are ordered so cancel runs BEFORE done is closed —
// defers run LIFO, so close(done) is registered first. done is what Close
// waits on, so it must be the last signal: closing it while the context
// was still live would let Close return before teardown had actually
// finished, which is the one thing a caller is entitled to rely on.
func runSubscription[T any](cancel context.CancelFunc, done chan struct{}, recv func() (T, error), handler func(T)) {
	defer close(done)
	defer cancel()
	for {
		event, err := recv()
		if err != nil {
			// Stream ended — via Close's cancel, the kernel closing it
			// (e.g. event-bus.md#backpressure's slow-consumer
			// disconnect), or ordinary EOF. Nothing further to receive
			// either way; the caller observes closure only via Close
			// returning (or simply by handler calls stopping), not via an
			// error surfaced from this goroutine.
			return
		}
		handler(event)
	}
}

// Subscribe opens a server-streaming subscription to the event bus,
// filtered by filters (event-bus.md#filter-grammar: each entry is an
// exact topic or a trailing-wildcard prefix ending in "*"), and invokes
// handler once per received event on a dedicated goroutine this method
// owns — so a plugin author writes a handler, not stream-receive
// plumbing. The returned Subscription's Close stops receiving.
//
// handler runs sequentially, in delivery order, on the one goroutine this
// call starts — a slow handler delays only this Subscription's own
// delivery, never the caller's goroutine or any other Subscription.
func (c *Client) Subscribe(ctx context.Context, filters []string, handler BusEventHandler) (*Subscription, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.raw.Subscribe(streamCtx, &kernelv1.SubscribeRequest{TopicFilters: filters})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("kernel: subscribe: %w", err)
	}

	sub := &Subscription{cancel: cancel, done: make(chan struct{})}
	go runSubscription(cancel, sub.done, stream.Recv, handler)
	return sub, nil
}
