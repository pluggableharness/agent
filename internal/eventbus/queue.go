package eventbus

import "sync"

// queueCompactThreshold is the backing-array capacity above which pop
// reclaims a shrunk slice once the drained (already-popped) prefix
// dominates it, rather than only ever slicing forward. Below this
// capacity, the wasted prefix is small enough not to bother — compacting
// is itself an allocation-plus-copy, so it isn't free.
const queueCompactThreshold = 64

// queue is an unbounded, FIFO, concurrency-safe holding area for one
// Subscription's not-yet-delivered Events. It is deliberately not a
// channel: a channel needs a fixed capacity (which would reintroduce
// blocking or dropping, the exact tradeoff this package's design
// rejected — see doc.go), whereas a mutex-guarded slice plus a
// non-blocking wake signal grows without bound and never blocks push.
type queue struct {
	mu    sync.Mutex
	items []Event

	// signal wakes a blocked deliverLoop when push adds to a previously
	// empty queue. Buffered at 1 and sent via a non-blocking select, so
	// push never waits on a slow or absent reader — multiple pending
	// pushes coalesce into a single pending signal, which is fine since
	// deliverLoop always drains the queue fully before waiting again.
	signal chan struct{}
}

// newQueue returns an empty queue, ready to use.
func newQueue() *queue {
	return &queue{signal: make(chan struct{}, 1)}
}

// push appends ev to the back of q and wakes a waiting deliverLoop. push
// never blocks and never fails — this is the "unbounded, never-blocking,
// never-dropping" half of the delivery contract (doc.go).
func (q *queue) push(ev Event) {
	q.mu.Lock()
	q.items = append(q.items, ev)
	q.mu.Unlock()

	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// pop removes and returns the front of q, reporting false if q is empty.
func (q *queue) pop() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return Event{}, false
	}

	ev := q.items[0]
	q.items[0] = Event{} // release any reference the popped Event's Payload held, ahead of the compaction check below
	q.items = q.items[1:]

	// Slicing forward on every pop never releases the original backing
	// array — a queue that stayed busy for a while would otherwise retain
	// an ever-growing array purely from the drained prefix, even though
	// every item in that prefix has already been delivered. Once the
	// array is large enough to be worth the copy, and the drained prefix
	// dominates what's left, reallocate a right-sized slice instead.
	if cap(q.items) > queueCompactThreshold && len(q.items) < cap(q.items)/4 {
		compacted := make([]Event, len(q.items))
		copy(compacted, q.items)
		q.items = compacted
	}

	return ev, true
}

// len reports the number of not-yet-delivered Events currently queued.
func (q *queue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
