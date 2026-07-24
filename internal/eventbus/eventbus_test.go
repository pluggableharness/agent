package eventbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

func TestNew_defaults(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	if b.logger == nil {
		t.Error("logger = nil, want slog.Default()")
	}
	if b.telemetry == nil {
		t.Error("telemetry = nil, want a default Provider")
	}
	if b.queueWarnThreshold != defaultQueueWarnThreshold {
		t.Errorf("queueWarnThreshold = %d, want %d", b.queueWarnThreshold, defaultQueueWarnThreshold)
	}
}

func TestNew_options(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	prov, err := telemetry.New(context.Background(), telemetry.Config{}, fake.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })

	b := New(WithLogger(logger), WithTelemetry(prov), WithQueueWarnThreshold(7))
	t.Cleanup(func() { _ = b.Close() })

	if b.logger != logger {
		t.Error("WithLogger did not take effect")
	}
	if b.telemetry != prov {
		t.Error("WithTelemetry did not take effect")
	}
	if b.queueWarnThreshold != 7 {
		t.Errorf("queueWarnThreshold = %d, want 7", b.queueWarnThreshold)
	}
}

func TestNew_optionsIgnoreNil(t *testing.T) {
	t.Parallel()

	b := New(WithLogger(nil), WithTelemetry(nil))
	t.Cleanup(func() { _ = b.Close() })

	if b.logger == nil {
		t.Error("WithLogger(nil) left logger nil")
	}
	if b.telemetry == nil {
		t.Error("WithTelemetry(nil) left telemetry nil")
	}
}

func TestBus_publish_fanOutToMultipleSubscribers(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	const n = 5
	chans := make([]chan Event, n)
	for i := range n {
		chans[i] = make(chan Event, 1)
		ch := chans[i]
		sub, err := b.Subscribe(context.Background(), "topic", func(_ context.Context, ev Event) {
			ch <- ev
		})
		if err != nil {
			t.Fatalf("Subscribe(%d): %v", i, err)
		}
		t.Cleanup(func() { _ = sub.Close() })
	}

	if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: "hello"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i, ch := range chans {
		ev := recvOrTimeout(t, ch)
		if ev.Payload != "hello" {
			t.Errorf("subscriber %d: Payload = %v, want hello", i, ev.Payload)
		}
	}
}

func TestBus_publish_onlyMatchingTopic(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Event, 1)
	sub, err := b.Subscribe(context.Background(), "topic.a", func(_ context.Context, ev Event) { got <- ev })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "topic.b"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case ev := <-got:
		t.Fatalf("subscriber to topic.a received %+v, want nothing", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBus_publish_noSubscribersIsNotAnError(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "nobody.listening"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestBus_publish_validation(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	if err := b.Publish(context.Background(), Event{}); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("Publish(empty topic) = %v, want ErrEmptyTopic", err)
	}
}

func TestBus_subscribe_validation(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	if _, err := b.Subscribe(context.Background(), "", func(context.Context, Event) {}); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("Subscribe(empty topic) = %v, want ErrEmptyTopic", err)
	}
	if _, err := b.Subscribe(context.Background(), "topic", nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("Subscribe(nil handler) = %v, want ErrNilHandler", err)
	}
}

func TestBus_unsubscribeDuringPublish(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Event, 1)
	sub, err := b.Subscribe(context.Background(), "topic", func(_ context.Context, ev Event) { got <- ev })
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
	case ev := <-got:
		t.Fatalf("closed subscriber received %+v, want nothing", ev)
	case <-time.After(100 * time.Millisecond):
	}

	b.mu.RLock()
	_, stillPresent := b.subs["topic"]
	b.mu.RUnlock()
	if stillPresent {
		t.Error("bus registry still holds an empty topic entry after its only subscriber closed")
	}
}

func TestBus_closed_rejectsPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	b := New()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := b.Publish(context.Background(), Event{Topic: "topic"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish after Close = %v, want ErrClosed", err)
	}
	if _, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {}); !errors.Is(err, ErrClosed) {
		t.Errorf("Subscribe after Close = %v, want ErrClosed", err)
	}
}

func TestBus_close_idempotent(t *testing.T) {
	t.Parallel()

	b := New()
	for range 3 {
		if err := b.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

func TestBus_close_stopsOpenSubscriptions(t *testing.T) {
	t.Parallel()

	b := New()
	got := make(chan struct{}, 1)
	_, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) { got <- struct{}{} })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	before := runtime.NumGoroutine()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(waitTimeout)
	for runtime.NumGoroutine() > before-1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := runtime.NumGoroutine(); got > before-1 {
		t.Errorf("NumGoroutine after Close = %d, want <= %d (delivery goroutine should have exited)", got, before-1)
	}
}

// TestBus_publish_neverBlocksOnSlowSubscriber is the unbounded/non-blocking
// proof: a subscriber whose handler blocks indefinitely must not delay
// Publish's return, and must not prevent a second, healthy subscriber
// from being delivered to promptly.
func TestBus_publish_neverBlocksOnSlowSubscriber(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	// block is closed (unblocking the slow handler below) before this
	// subscription's own Close is called, later in the test body —
	// Subscription.Close waits for an in-flight handler to return
	// (doc.go), so closing it while the handler is still blocked on
	// block would deadlock the test itself, not the package under test.
	block := make(chan struct{})

	slow, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {
		<-block // never returns until the test unblocks it
	})
	if err != nil {
		t.Fatalf("Subscribe(slow): %v", err)
	}

	fastGot := make(chan struct{}, 1)
	fast, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {
		fastGot <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe(fast): %v", err)
	}
	t.Cleanup(func() { _ = fast.Close() })

	publishDone := make(chan struct{})
	go func() {
		_ = b.Publish(context.Background(), Event{Topic: "topic"})
		close(publishDone)
	}()

	select {
	case <-publishDone:
	case <-time.After(waitTimeout):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	recvOrTimeout(t, fastGot) // the fast subscriber must not be starved by the slow one

	close(block)     // release the slow handler...
	_ = slow.Close() // ...only now is it safe to wait for its goroutine to exit
	_ = fast.Close()
}

func TestBus_queueWarnThreshold(t *testing.T) {
	t.Parallel()

	b := New(WithQueueWarnThreshold(3))
	t.Cleanup(func() { _ = b.Close() })

	// block is closed (unblocking the handler below) before Close is
	// called on its subscription, later in the test body — see
	// TestBus_publish_neverBlocksOnSlowSubscriber's comment for why the
	// order matters.
	block := make(chan struct{})

	sub, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {
		<-block // holds delivery so events pile up in the queue
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// The first event is popped immediately by deliverLoop and blocks
	// inside the handler, so it never contributes to queue depth; publish
	// enough more to cross the threshold among the ones left queued.
	for i := range 5 {
		if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: i}); err != nil {
			t.Fatalf("Publish(%d): %v", i, err)
		}
	}

	deadline := time.Now().Add(waitTimeout)
	for sub.queue.len() < 3 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := sub.queue.len(); got < 3 {
		t.Fatalf("queue depth = %d, want >= 3 for this test to be meaningful", got)
	}
	if !sub.warned {
		t.Error("warned = false after crossing the threshold, want true")
	}

	close(block)
	_ = sub.Close()
}

func TestBus_publish_recordsSpanAndMetric(t *testing.T) {
	t.Parallel()

	backend := fake.New()
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "eventbus_test"
	prov, err := telemetry.New(context.Background(), cfg, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })

	b := New(WithTelemetry(prov))
	t.Cleanup(func() { _ = b.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "tool.result"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := prov.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	spans := backend.Spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	if spans[0].Name != "eventbus.publish" {
		t.Errorf("span name = %q, want eventbus.publish", spans[0].Name)
	}

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !hasMetric(rm, "pluggableharness.agent.eventbus.events.published") {
		t.Error("eventbus.events.published metric was not recorded")
	}
}

// TestBus_concurrentPublishAndSubscribe exercises concurrent Publish,
// Subscribe, and Subscription.Close under -race, following the
// statebackend package's WaitGroup-coordinated, per-index-slice shape
// (go-testing.md).
func TestBus_concurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	const (
		publishers  = 20
		eventsEach  = 20
		subscribers = 10
	)

	var (
		mu        sync.Mutex
		delivered int
	)
	subs := make([]*Subscription, subscribers)
	for i := range subscribers {
		sub, err := b.Subscribe(context.Background(), "topic", func(context.Context, Event) {
			mu.Lock()
			delivered++
			mu.Unlock()
		})
		if err != nil {
			t.Fatalf("Subscribe(%d): %v", i, err)
		}
		subs[i] = sub
	}

	var wg sync.WaitGroup
	for p := range publishers {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := range eventsEach {
				if err := b.Publish(context.Background(), Event{Topic: "topic", Payload: fmt.Sprintf("%d-%d", p, i)}); err != nil {
					t.Errorf("Publish: %v", err)
				}
			}
		}(p)
	}
	// Half the subscribers unsubscribe concurrently with publishing —
	// Close must never race with Publish's fan-out (queue.push is
	// concurrency-safe regardless of whether the subscription is mid-close).
	for i := range subscribers / 2 {
		wg.Add(1)
		go func(sub *Subscription) {
			defer wg.Done()
			_ = sub.Close()
		}(subs[i])
	}
	wg.Wait()

	for _, sub := range subs {
		_ = sub.Close()
	}
}

// hasMetric reports whether rm contains an instrument named name — this
// package doesn't need to assert specific values, only that eventbus.go
// actually calls Add rather than merely holding an unused instrument
// reference (usage_test.go in internal/telemetry has the fuller
// find-and-assert-value pattern this borrows from).
func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}
