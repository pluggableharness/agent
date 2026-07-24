package eventbus

import (
	"context"
	"testing"
	"time"
)

// waitTimeout is the bound every test in this package uses for waiting on
// asynchronous delivery — long enough that legitimate scheduling delay
// never trips it, short enough that a real deadlock fails the test
// promptly instead of hanging the suite.
const waitTimeout = 5 * time.Second

// recvOrTimeout receives one value from ch, failing t if none arrives
// within waitTimeout.
func recvOrTimeout[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for delivery")
		var zero T
		return zero
	}
}

func TestSubscription_deliversInPublishOrder(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan int, 10)
	sub, err := b.Subscribe(context.Background(), "topic", func(_ context.Context, ev Event) {
		got <- ev.Payload.(int)
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	for i := range 10 {
		if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: i}); err != nil {
			t.Fatalf("Publish(%d): %v", i, err)
		}
	}

	for i := range 10 {
		if v := recvOrTimeout(t, got); v != i {
			t.Fatalf("delivery %d: Payload = %d, want %d", i, v, i)
		}
	}
}

func TestSubscription_close_stopsDelivery(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan struct{}, 10)
	sub, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {
		got <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := b.Publish(context.Background(), Event{Topic: "topic"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-got:
		t.Fatal("handler invoked after Close")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSubscription_close_idempotent(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	sub, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	for range 3 {
		if err := sub.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

func TestSubscription_close_concurrent(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	sub, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	done := make(chan struct{})
	for range 5 {
		go func() {
			_ = sub.Close()
			done <- struct{}{}
		}()
	}
	for range 5 {
		recvOrTimeout(t, done)
	}
}

func TestSubscription_ctxCancel_stopsDelivery(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	sub, err := b.Subscribe(ctx, "topic", func(context.Context, Event) {
		got <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	cancel()
	<-sub.done // deliverLoop's own exit signal — proves ctx cancellation alone (no explicit Close) unwinds the goroutine

	if err := b.Publish(context.Background(), Event{Topic: "topic"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-got:
		t.Fatal("handler invoked after ctx cancellation")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSubscription_handlerPanic_doesNotKillDelivery(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan int, 2)
	sub, err := b.Subscribe(context.Background(), "topic", func(_ context.Context, ev Event) {
		n := ev.Payload.(int)
		if n == 0 {
			panic("boom")
		}
		got <- n
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: 0}); err != nil {
		t.Fatalf("Publish(0): %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: 1}); err != nil {
		t.Fatalf("Publish(1): %v", err)
	}

	if v := recvOrTimeout(t, got); v != 1 {
		t.Fatalf("delivery after panic: Payload = %d, want 1", v)
	}
}
