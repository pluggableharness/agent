package kernelcallback

import (
	"context"
	"sync"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// DeltaHub fans live TokenDeltas out to StreamDeltas subscribers, per
// session. Out-of-band with respect to the event bus: no topic matching,
// no shared subscriber queue. Per-stream FIFO only; not durable.
//
// Safe for concurrent use. A nil *DeltaHub is treated as "no deltas" by
// Server.StreamDeltas.
type DeltaHub struct {
	mu   sync.Mutex
	subs map[string]map[chan *kernelv1.TokenDelta]struct{} // sessionID -> set of chans
}

// NewDeltaHub returns an empty hub.
func NewDeltaHub() *DeltaHub {
	return &DeltaHub{subs: make(map[string]map[chan *kernelv1.TokenDelta]struct{})}
}

// Publish delivers delta to every active StreamDeltas subscriber for its
// session. Non-blocking: a slow subscriber that has filled its buffer
// drops that one delta (live-only, best-effort within a stream — the
// finished text still arrives via ReadEvents as a RenderTree).
func (h *DeltaHub) Publish(delta *kernelv1.TokenDelta) {
	if h == nil || delta == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[delta.GetSessionId()] {
		select {
		case ch <- delta:
		default:
		}
	}
}

// Serve registers a subscriber for sessionID and forwards deltas to
// stream until ctx is canceled.
func (h *DeltaHub) Serve(ctx context.Context, sessionID string, stream kernelv1.KernelCallbackService_StreamDeltasServer) error {
	ch := make(chan *kernelv1.TokenDelta, 64)
	h.mu.Lock()
	if h.subs[sessionID] == nil {
		h.subs[sessionID] = make(map[chan *kernelv1.TokenDelta]struct{})
	}
	h.subs[sessionID][ch] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.subs[sessionID], ch)
		if len(h.subs[sessionID]) == 0 {
			delete(h.subs, sessionID)
		}
		h.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case delta, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(delta); err != nil {
				return err
			}
		}
	}
}
