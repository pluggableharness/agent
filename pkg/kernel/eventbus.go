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
	go func() {
		defer close(sub.done)
		for {
			event, err := stream.Recv()
			if err != nil {
				// Stream ended — via Close's cancel, the kernel closing it
				// (e.g. event-bus.md#backpressure's slow-consumer
				// disconnect), or ordinary EOF. Nothing further to receive
				// either way; the caller observes closure only via Close
				// returning (or simply by handler calls stopping), not via
				// a returned error from this goroutine.
				return
			}
			handler(event)
		}
	}()
	return sub, nil
}
