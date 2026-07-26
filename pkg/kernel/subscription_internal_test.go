package kernel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// waitClosed fails the test if ch is not closed within a short budget.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: not closed within the deadline", what)
	}
}

// TestRunSubscription_releasesContextWhenStreamEndsOnItsOwn is the
// regression test for both Client.Subscribe and Client.ReadEvents. Each
// derives a per-subscription context.WithCancel from the caller's ctx and
// stores only the cancel func on the Subscription — so when the receive
// goroutine used to return without calling it, a stream that ended on its
// own left that derived context attached to its parent until the parent
// itself was canceled.
//
// ReadEvents makes this the documented normal path: its stream is finite
// and its doc comment tells a plugin author the goroutine exits without
// them calling Close at all. A plugin issuing repeated ReadEvents calls
// under one long-lived context would accumulate a cancel entry per call.
func TestRunSubscription_releasesContextWhenStreamEndsOnItsOwn(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// A finite stream: two events, then a natural EOF. Close is never
	// called, exactly as ReadEvents' contract permits.
	remaining := 2
	recv := func() (int, error) {
		if remaining == 0 {
			return 0, io.EOF
		}
		remaining--
		return remaining, nil
	}

	var delivered int
	go runSubscription(cancel, done, recv, func(int) { delivered++ })

	waitClosed(t, done, "subscription done channel")

	if delivered != 2 {
		t.Errorf("handler calls = %d, want 2", delivered)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("subscription context Err() = %v, want context.Canceled — "+
			"a stream that ends on its own must still release its derived context", err)
	}
}

// TestRunSubscription_closeStillWorksAfterNaturalEnd pins that the
// deferred cancel does not break Close's cancel-then-wait: cancel is
// documented-safe to call repeatedly, and done is already closed, so Close
// must return promptly rather than blocking or panicking.
func TestRunSubscription_closeStillWorksAfterNaturalEnd(t *testing.T) {
	t.Parallel()

	_, cancel := context.WithCancel(context.Background())
	sub := &Subscription{cancel: cancel, done: make(chan struct{})}

	recv := func() (int, error) { return 0, io.EOF }
	go runSubscription(cancel, sub.done, recv, func(int) {})

	waitClosed(t, sub.done, "subscription done channel")

	for range 3 {
		if err := sub.Close(); err != nil {
			t.Fatalf("Close after a natural stream end: %v", err)
		}
	}
}

// TestRunSubscription_stopsOnStreamError covers the other exit: a
// mid-stream transport failure ends delivery without surfacing an error
// from the goroutine, and still releases the context.
func TestRunSubscription_stopsOnStreamError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	var delivered int
	recv := func() (int, error) {
		if delivered == 0 {
			return 1, nil
		}
		return 0, errors.New("transport failure")
	}

	go runSubscription(cancel, done, recv, func(int) { delivered++ })

	waitClosed(t, done, "subscription done channel")

	if delivered != 1 {
		t.Errorf("handler calls = %d, want 1", delivered)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("subscription context Err() = %v, want context.Canceled", err)
	}
}
