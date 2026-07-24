package eventbus

import "testing"

func TestQueue_popEmpty(t *testing.T) {
	t.Parallel()

	q := newQueue()
	if _, ok := q.pop(); ok {
		t.Fatal("pop on empty queue: ok = true, want false")
	}
	if got := q.len(); got != 0 {
		t.Fatalf("len = %d, want 0", got)
	}
}

func TestQueue_fifoOrder(t *testing.T) {
	t.Parallel()

	q := newQueue()
	for i := range 5 {
		q.push(Event{Topic: "t", Payload: i})
	}
	if got := q.len(); got != 5 {
		t.Fatalf("len = %d, want 5", got)
	}

	for i := range 5 {
		ev, ok := q.pop()
		if !ok {
			t.Fatalf("pop %d: ok = false, want true", i)
		}
		if ev.Payload != i {
			t.Fatalf("pop %d: Payload = %v, want %d", i, ev.Payload, i)
		}
	}
	if _, ok := q.pop(); ok {
		t.Fatal("pop after draining: ok = true, want false")
	}
}

func TestQueue_signalWakes(t *testing.T) {
	t.Parallel()

	q := newQueue()
	select {
	case <-q.signal:
		t.Fatal("signal fired before any push")
	default:
	}

	q.push(Event{Topic: "t"})
	select {
	case <-q.signal:
	default:
		t.Fatal("signal did not fire after push onto an empty queue")
	}

	// Multiple pushes before a drain coalesce into one pending signal —
	// the reader is expected to drain fully on each wake, not count
	// signals 1:1 with pushes.
	q.push(Event{Topic: "t"})
	q.push(Event{Topic: "t"})
	select {
	case <-q.signal:
	default:
		t.Fatal("signal did not fire after a second push")
	}
	select {
	case <-q.signal:
		t.Fatal("a second pending signal fired — pushes should coalesce into one")
	default:
	}
}

func TestQueue_compactsAfterDraining(t *testing.T) {
	t.Parallel()

	q := newQueue()
	const n = queueCompactThreshold * 2
	for i := range n {
		q.push(Event{Topic: "t", Payload: i})
	}

	before := cap(q.items)
	if before <= queueCompactThreshold {
		t.Fatalf("cap before draining = %d, want > %d for this test to be meaningful", before, queueCompactThreshold)
	}

	// Drain past the point where the remaining items are a small fraction
	// of the backing array's capacity — pop's compaction should kick in
	// and shrink the backing array rather than just slicing forward.
	for range n - 1 {
		if _, ok := q.pop(); !ok {
			t.Fatal("pop: ok = false while items should remain")
		}
	}

	after := cap(q.items)
	if after >= before {
		t.Errorf("cap after draining = %d, want < %d (backing array should have been reclaimed)", after, before)
	}
	if got := q.len(); got != 1 {
		t.Fatalf("len after draining = %d, want 1", got)
	}
}
