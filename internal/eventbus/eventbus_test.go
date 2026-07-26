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

	got := make(chan Event, 2)
	sub, err := b.Subscribe(context.Background(), "topic.a", func(_ context.Context, ev Event) { got <- ev })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	// The non-matching topic.b must never reach a topic.a subscriber. Prove
	// it deterministically instead of sleeping: publish topic.b, then a
	// topic.a sentinel that does match. Delivery is FIFO per subscription, so
	// if topic.b had leaked it would arrive first — requiring the sentinel to
	// be the first (and only) delivery rules that out.
	if err := b.Publish(context.Background(), Event{Topic: "topic.b"}); err != nil {
		t.Fatalf("Publish(topic.b): %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "topic.a", Payload: "sentinel"}); err != nil {
		t.Fatalf("Publish(topic.a): %v", err)
	}

	if ev := recvOrTimeout(t, got); ev.Topic != "topic.a" || ev.Payload != "sentinel" {
		t.Fatalf("first delivery = %+v, want the topic.a sentinel (topic.b must not match)", ev)
	}
	assertNoPending(t, got)
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

	// sub.Close returned before this Publish, so the delivery goroutine is
	// gone and deregistered — the closed subscriber cannot receive. Assert
	// instantly rather than sleeping.
	assertNoPending(t, got)

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

// TestBus_close_stopsWildcardOnlySubscriptions is the regression test for
// a Subscription registered under nothing but wildcard filters. Such a
// subscription lives only in b.wildcards, never in b.subs, so a Close that
// collected from b.subs alone left its delivery goroutine running while
// reporting a complete shutdown. The kernel-callback Subscribe RPC accepts
// exactly these filters ("*", "kernel.*"), so this is a reachable shape,
// not a synthetic one.
func TestBus_close_stopsWildcardOnlySubscriptions(t *testing.T) {
	t.Parallel()

	b := New()
	sub, err := b.SubscribeFilters(context.Background(), []string{"kernel.*"}, func(context.Context, Event) {})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close waits for each Subscription's delivery goroutine to exit, so
	// by the time it returns sub.done must already be closed. Asserting on
	// done rather than NumGoroutine keeps this deterministic.
	select {
	case <-sub.done:
	default:
		t.Fatal("Bus.Close returned with a wildcard-only subscription still open; its delivery goroutine leaked")
	}
}

// TestBus_close_closesEachSubscriptionOnce covers a Subscription reachable
// through both registries at once — one exact filter and one wildcard —
// which Close must collect exactly once rather than once per registration.
func TestBus_close_closesEachSubscriptionOnce(t *testing.T) {
	t.Parallel()

	b := New()
	sub, err := b.SubscribeFilters(context.Background(), []string{"kernel.event.plan", "kernel.*"}, func(context.Context, Event) {})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-sub.done:
	default:
		t.Fatal("Bus.Close left a mixed exact+wildcard subscription open")
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
	if !hasMetric(rm, "pluggableharness.eventbus.events.published") {
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

func TestBus_subscribeFilters_wildcardMatch(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Event, 4)
	sub, err := b.SubscribeFilters(context.Background(), []string{"plugin.tool.github.*"}, func(_ context.Context, ev Event) {
		got <- ev
	})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.github.file_changed", Payload: "a"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.gitlab.file_changed", Payload: "b"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.github.pr_opened", Payload: "c"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// A matching sentinel published last must be the third delivery: the
	// gitlab event (b) must be filtered out, so requiring a, c, z in order
	// proves it was — no sleep needed to "confirm" b never arrives.
	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.github.sentinel", Payload: "z"}); err != nil {
		t.Fatalf("Publish(sentinel): %v", err)
	}

	first := recvOrTimeout(t, got)
	second := recvOrTimeout(t, got)
	third := recvOrTimeout(t, got)
	if first.Payload != "a" || second.Payload != "c" || third.Payload != "z" {
		t.Fatalf("got payloads %v, %v, %v; want a, c, z (gitlab event must not match the github.* filter)", first.Payload, second.Payload, third.Payload)
	}
	assertNoPending(t, got)
}

func TestBus_subscribeFilters_mixedExactAndWildcard(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Event, 4)
	sub, err := b.SubscribeFilters(context.Background(), []string{"kernel.event.tool_call", "plugin.tool.github.*"}, func(_ context.Context, ev Event) {
		got <- ev
	})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "kernel.event.tool_call"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.github.file_changed"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.Publish(context.Background(), Event{Topic: "kernel.event.message"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// kernel.event.message matches neither filter. A matching sentinel
	// published after it must therefore be the third delivery; if message had
	// leaked it would take that slot instead — caught deterministically
	// rather than by sleeping.
	if err := b.Publish(context.Background(), Event{Topic: "plugin.tool.github.sentinel"}); err != nil {
		t.Fatalf("Publish(sentinel): %v", err)
	}

	first := recvOrTimeout(t, got)
	second := recvOrTimeout(t, got)
	third := recvOrTimeout(t, got)
	if third.Topic != "plugin.tool.github.sentinel" {
		t.Fatalf("deliveries were %q, %q, %q; want the sentinel third (kernel.event.message matches neither filter)", first.Topic, second.Topic, third.Topic)
	}
	assertNoPending(t, got)
}

func TestBus_subscribeFilters_overlappingFiltersDeliverOnce(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Event, 4)
	// "kernel.*" and "kernel.event.*" both match "kernel.event.tool_call" —
	// a single Subscription registered under both MUST still be invoked
	// exactly once per Publish, not once per matching filter.
	sub, err := b.SubscribeFilters(context.Background(), []string{"kernel.*", "kernel.event.*"}, func(_ context.Context, ev Event) {
		got <- ev
	})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	if err := b.Publish(context.Background(), Event{Topic: "kernel.event.tool_call"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// kernel.event.sentinel also matches both filters. tool_call must be
	// delivered exactly once, so the sentinel is the second delivery; a
	// duplicate would take that slot instead — caught without a sleep.
	if err := b.Publish(context.Background(), Event{Topic: "kernel.event.sentinel"}); err != nil {
		t.Fatalf("Publish(sentinel): %v", err)
	}

	if ev := recvOrTimeout(t, got); ev.Topic != "kernel.event.tool_call" {
		t.Fatalf("first delivery = %q, want kernel.event.tool_call", ev.Topic)
	}
	if ev := recvOrTimeout(t, got); ev.Topic != "kernel.event.sentinel" {
		t.Fatalf("second delivery = %q, want the sentinel (kernel.event.tool_call was delivered more than once)", ev.Topic)
	}
	assertNoPending(t, got)
}

func TestBus_subscribeFilters_validation(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	if _, err := b.SubscribeFilters(context.Background(), nil, func(context.Context, Event) {}); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("SubscribeFilters(nil filters) = %v, want ErrEmptyTopic", err)
	}
	if _, err := b.SubscribeFilters(context.Background(), []string{"a", ""}, func(context.Context, Event) {}); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("SubscribeFilters(one empty filter) = %v, want ErrEmptyTopic", err)
	}
	if _, err := b.SubscribeFilters(context.Background(), []string{"a.*"}, nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("SubscribeFilters(nil handler) = %v, want ErrNilHandler", err)
	}
}

func TestBus_remove_prunesWildcardEntries(t *testing.T) {
	t.Parallel()

	b := New()
	t.Cleanup(func() { _ = b.Close() })

	sub, err := b.SubscribeFilters(context.Background(), []string{"plugin.tool.github.*"}, func(context.Context, Event) {})
	if err != nil {
		t.Fatalf("SubscribeFilters: %v", err)
	}
	if len(b.wildcards) != 1 {
		t.Fatalf("b.wildcards has %d entries after Subscribe, want 1", len(b.wildcards))
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("sub.Close: %v", err)
	}

	b.mu.RLock()
	remaining := len(b.wildcards)
	b.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("b.wildcards has %d entries after Close, want 0", remaining)
	}
}
