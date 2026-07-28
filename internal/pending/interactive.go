package pending

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/interactive"
)

// InteractiveBridge implements interactive.Resolver by parking on Complete.
type InteractiveBridge struct {
	mu      sync.Mutex
	waiters map[string]chan interactiveResult // key: sessionID + "\x00" + callID
}

type interactiveResult struct {
	resp interactive.Response
	err  error
}

// NewInteractiveBridge returns an empty bridge.
func NewInteractiveBridge() *InteractiveBridge {
	return &InteractiveBridge{waiters: make(map[string]chan interactiveResult)}
}

// Resolve implements interactive.Resolver.
//
// sessionID is not on interactive.Request — the bridge keys only by CallID
// globally within a process (call ids are ULIDs and unique). For multi-
// session safety the Complete path still accepts sessionID for logging
// and future partitioning; waiters are keyed by callID alone today.
func (b *InteractiveBridge) Resolve(ctx context.Context, req interactive.Request) (interactive.Response, error) {
	if req.CallID == "" {
		return interactive.Response{}, fmt.Errorf("pending: interactive: call_id is required")
	}
	key := req.CallID
	ch := make(chan interactiveResult, 1)

	b.mu.Lock()
	if _, exists := b.waiters[key]; exists {
		b.mu.Unlock()
		return interactive.Response{}, fmt.Errorf("pending: interactive: duplicate waiter for %s", key)
	}
	b.waiters[key] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, key)
		b.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return interactive.Response{}, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return interactive.Response{}, res.err
		}
		return res.resp, nil
	}
}

// Complete unblocks one outstanding Resolve for callID.
func (b *InteractiveBridge) Complete(callID string, payload *structpb.Struct) error {
	if callID == "" {
		return fmt.Errorf("pending: interactive: call_id is required")
	}
	b.mu.Lock()
	ch, ok := b.waiters[callID]
	if !ok {
		b.mu.Unlock()
		return ErrNoWaiter
	}
	delete(b.waiters, callID)
	b.mu.Unlock()

	select {
	case ch <- interactiveResult{resp: interactive.Response{Payload: payload}}:
		return nil
	default:
		return ErrAlreadyResolved
	}
}

var _ interactive.Resolver = (*InteractiveBridge)(nil)
