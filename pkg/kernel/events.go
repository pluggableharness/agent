package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// StoredEventHandler is invoked once per *kernelv1.StoredEvent a ReadEvents
// stream receives, in the kernel's ascending-sequence delivery order
// (kernel-callbacks.md#readevents — never by time, per
// .claude/rules/determinism.md), on that stream's own dedicated receive
// goroutine — never on the caller's own goroutine, mirroring
// BusEventHandler's shape in eventbus.go.
type StoredEventHandler func(event *kernelv1.StoredEvent)

// ReadEvents opens a server-streaming read-back of the calling plugin's own
// session's persisted event log, ordered by sequence
// (kernel-callbacks.md#readevents), invoking handler once per StoredEvent
// on a dedicated goroutine this method owns — the same shape Subscribe
// (eventbus.go) already uses, rather than a second streaming idiom in this
// package. The returned Subscription's Close stops receiving early; unlike
// Subscribe's open-ended bus subscription, a ReadEvents stream is also
// naturally finite on its own — the kernel closes it once every matching
// event (bounded by req.Limit, if set) has been delivered, at which point
// handler simply stops being called and the Subscription's internal
// goroutine exits without the caller needing to call Close at all.
//
// req.SessionId is mandatory and MUST name the session this plugin was
// actually invoked for — the same one-session-only rule Emit documents;
// the kernel rejects any other value. req.Kinds MAY be empty (meaning
// every kind); req.FromSequence and req.Limit MAY be omitted (meaning
// "from the start of the log" and "no limit," respectively). req is passed
// through directly rather than exploded into discrete parameters: one
// mandatory field plus three independently-optional ones is exactly the
// case where an options-struct-of-parameters would just reproduce the
// generated type's own shape.
func (c *Client) ReadEvents(ctx context.Context, req *kernelv1.ReadEventsRequest, handler StoredEventHandler) (*Subscription, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.raw.ReadEvents(streamCtx, req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("kernel: read events: %w", err)
	}

	sub := &Subscription{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(sub.done)
		for {
			event, err := stream.Recv()
			if err != nil {
				// Stream ended — naturally (every matching event delivered,
				// the common case for this RPC), via Close's cancel, or an
				// ordinary EOF. Nothing further to receive either way; same
				// no-error-surfaced-from-this-goroutine shape Subscribe uses.
				return
			}
			handler(event)
		}
	}()
	return sub, nil
}
