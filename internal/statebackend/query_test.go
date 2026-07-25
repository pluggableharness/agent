package statebackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

func TestSession_Meta_roundTrip(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess := createSession(t, st, meta)

	got, err := sess.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if got.SessionID != meta.SessionID || got.Profile != meta.Profile || got.Status != meta.Status {
		t.Errorf("Meta = %+v, want matching %+v", got, meta)
	}
}

func TestSession_Meta_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.Meta(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("Meta after Close err = %v, want ErrClosed", err)
	}
}

// TestSession_Events_roundTrip is the required round-trip test: events
// with varied payloads (empty, large ~1MB, arbitrary bytes) must read back
// via Events() byte-identical, in exact sequence order.
func TestSession_Events_roundTrip(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	large := make([]byte, 1<<20) // ~1MB
	for i := range large {
		large[i] = byte(i % 251)
	}

	payloads := [][]byte{
		{},                             // empty
		[]byte("hello"),                // ordinary text
		{0x00, 0xFF, 0x01, 0x02, 0x03}, // arbitrary bytes, including NUL and high bytes
		large,                          // ~1MB
	}

	wantIDs := make([]string, len(payloads))
	for i, p := range payloads {
		ev := testEvent(fmt.Sprintf("evt-%d", i))
		ev.Payload = p
		if _, err := sess.AppendEvent(context.Background(), ev); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
		wantIDs[i] = ev.ID
	}

	var got []Event
	for ev, err := range sess.Events(context.Background()) {
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		got = append(got, ev)
	}

	if len(got) != len(payloads) {
		t.Fatalf("got %d events, want %d", len(got), len(payloads))
	}
	for i, ev := range got {
		if ev.Sequence != int64(i+1) {
			t.Errorf("event[%d].Sequence = %d, want %d", i, ev.Sequence, i+1)
		}
		if ev.ID != wantIDs[i] {
			t.Errorf("event[%d].ID = %q, want %q", i, ev.ID, wantIDs[i])
		}
		if !bytes.Equal(ev.Payload, payloads[i]) {
			t.Errorf("event[%d].Payload = %d bytes, want %d bytes (byte-identical)", i, len(ev.Payload), len(payloads[i]))
		}
	}
}

func TestSession_Events_empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	var got []Event
	for ev, err := range sess.Events(context.Background()) {
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		got = append(got, ev)
	}
	if len(got) != 0 {
		t.Errorf("Events (no events appended) = %d results, want 0", len(got))
	}
}

func TestSession_Events_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	count := 0
	var gotErr error
	for _, err := range sess.Events(context.Background()) {
		count++
		gotErr = err
	}
	if count != 1 {
		t.Fatalf("Events after Close yielded %d pairs, want exactly 1", count)
	}
	if !errors.Is(gotErr, ErrClosed) {
		t.Errorf("Events after Close err = %v, want ErrClosed", gotErr)
	}
}

// TestSession_Events_decodeErrorSurfaces plants a row with an
// unrecognized kind directly (bypassing AppendEvent's own validation) to
// force scanEvent's decode path to fail, verifying Events() surfaces the
// error through the pair's error side and stops iterating rather than
// silently skipping the bad row.
func TestSession_Events_decodeErrorSurfaces(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	const q = `INSERT INTO events (id, timestamp, kind, producer_category, producer_name, producer_version, schema_version, payload) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := sess.db.ExecContext(context.Background(), q, "evt-1", formatTimestamp(time.Now()), "not_a_kind", "tool", "p", "1", "v1", []byte("x")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	count := 0
	var gotErr error
	for _, err := range sess.Events(context.Background()) {
		count++
		gotErr = err
	}
	if count != 1 {
		t.Fatalf("Events yielded %d pairs, want exactly 1 (stops on first decode error)", count)
	}
	if !errors.Is(gotErr, ErrInvalidKind) {
		t.Errorf("Events err = %v, want ErrInvalidKind", gotErr)
	}
}

func TestSession_Events_stopsOnEarlyBreak(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	for i := range 5 {
		if _, err := sess.AppendEvent(context.Background(), testEvent(fmt.Sprintf("evt-%d", i))); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	count := 0
	for range sess.Events(context.Background()) {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("iteration count = %d, want 2 (stopped early)", count)
	}
}

// collectEvents drains an events iterator, failing the test on the first
// error the pair's error side carries.
func collectEvents(t *testing.T, seq iter.Seq2[Event, error]) []Event {
	t.Helper()
	var got []Event
	for ev, err := range seq {
		if err != nil {
			t.Fatalf("events iteration: %v", err)
		}
		got = append(got, ev)
	}
	return got
}

// eventsMatchingKinds is the realistic mix of kinds every EventsMatching
// test seeds, in append order — so sequence N holds eventsMatchingKinds[N-1].
var eventsMatchingKinds = []kernelv1.EventKind{
	kernelv1.EventKind_EVENT_KIND_MESSAGE,       // sequence 1
	kernelv1.EventKind_EVENT_KIND_TOOL_CALL,     // sequence 2
	kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,   // sequence 3
	kernelv1.EventKind_EVENT_KIND_TOOL_CALL,     // sequence 4
	kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,   // sequence 5
	kernelv1.EventKind_EVENT_KIND_PLAN,          // sequence 6
	kernelv1.EventKind_EVENT_KIND_APPLY,         // sequence 7
	kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE,  // sequence 8
	kernelv1.EventKind_EVENT_KIND_MESSAGE,       // sequence 9
	kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE, // sequence 10
}

// seedMixedEvents creates a session and appends eventsMatchingKinds to it,
// returning the session.
func seedMixedEvents(t *testing.T) *Session {
	t.Helper()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())
	for i, kind := range eventsMatchingKinds {
		ev := testEvent(fmt.Sprintf("evt-%02d", i))
		ev.Kind = kind
		ev.Payload = []byte(fmt.Sprintf("payload-%02d", i))
		if _, err := sess.AppendEvent(context.Background(), ev); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}
	return sess
}

func int64Ptr(v int64) *int64 { return &v }
func int32Ptr(v int32) *int32 { return &v }

// TestSession_EventsMatching_filters covers every combination of the three
// EventQuery filters set and unset against one realistic mix of kinds,
// asserting the exact matching sequences — which also pins
// sequence-ascending order, since every want below is ascending.
func TestSession_EventsMatching_filters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query EventQuery
		want  []int64
	}{
		{"zero value matches everything", EventQuery{}, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"kinds only, single", EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_RESULT}}, []int64{3, 5}},
		{
			"kinds only, several",
			EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_PLAN, kernelv1.EventKind_EVENT_KIND_APPLY}},
			[]int64{6, 7},
		},
		{
			"kinds order does not affect result order",
			EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE, kernelv1.EventKind_EVENT_KIND_MESSAGE}},
			[]int64{1, 9, 10},
		},
		{
			"duplicate kinds are ignored",
			EventQuery{Kinds: []kernelv1.EventKind{
				kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
				kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
				kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
			}},
			[]int64{2, 4},
		},
		{"kinds matching nothing", EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_HOOK_ERROR}}, nil},
		{"from_sequence only", EventQuery{FromSequence: int64Ptr(8)}, []int64{8, 9, 10}},
		{"from_sequence at 1 is everything", EventQuery{FromSequence: int64Ptr(1)}, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"from_sequence past the end", EventQuery{FromSequence: int64Ptr(99)}, nil},
		{"limit only", EventQuery{Limit: int32Ptr(3)}, []int64{1, 2, 3}},
		{"limit zero", EventQuery{Limit: int32Ptr(0)}, nil},
		{"limit larger than the log", EventQuery{Limit: int32Ptr(500)}, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{
			"kinds + from_sequence",
			EventQuery{
				Kinds:        []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_CALL, kernelv1.EventKind_EVENT_KIND_TOOL_RESULT},
				FromSequence: int64Ptr(4),
			},
			[]int64{4, 5},
		},
		{
			"kinds + limit",
			EventQuery{
				Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_CALL, kernelv1.EventKind_EVENT_KIND_TOOL_RESULT},
				Limit: int32Ptr(3),
			},
			[]int64{2, 3, 4},
		},
		{"from_sequence + limit", EventQuery{FromSequence: int64Ptr(5), Limit: int32Ptr(2)}, []int64{5, 6}},
		{
			"kinds + from_sequence + limit",
			EventQuery{
				Kinds:        []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_MESSAGE, kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE, kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE},
				FromSequence: int64Ptr(2),
				Limit:        int32Ptr(2),
			},
			[]int64{8, 9},
		},
		{
			"the last N events of one kind, the kernel-callbacks.md#readevents motivating case",
			EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_RESULT}, FromSequence: int64Ptr(4)},
			[]int64{5},
		},
	}

	// One seeded session for the whole table: every case here is a
	// read-only query, so they share a session rather than each standing up
	// its own sqlite file — the parent's createSession cleanup runs after
	// every parallel subtest has finished.
	sess := seedMixedEvents(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := collectEvents(t, sess.EventsMatching(context.Background(), tt.query))

			if len(got) != len(tt.want) {
				t.Fatalf("EventsMatching returned %d events, want %d", len(got), len(tt.want))
			}
			for i, ev := range got {
				if ev.Sequence != tt.want[i] {
					t.Errorf("event[%d].Sequence = %d, want %d", i, ev.Sequence, tt.want[i])
				}
				if i > 0 && got[i-1].Sequence >= ev.Sequence {
					t.Errorf("results not sequence-ascending at index %d: %d then %d", i, got[i-1].Sequence, ev.Sequence)
				}
				if wantKind := eventsMatchingKinds[ev.Sequence-1]; ev.Kind != wantKind {
					t.Errorf("event[%d].Kind = %v, want %v", i, ev.Kind, wantKind)
				}
			}
		})
	}
}

// TestSession_EventsMatching_zeroQueryEqualsEvents proves the equivalence
// Events now depends on by construction: the same session read both ways
// must yield identical Event values, field for field.
func TestSession_EventsMatching_zeroQueryEqualsEvents(t *testing.T) {
	t.Parallel()
	sess := seedMixedEvents(t)

	viaEvents := collectEvents(t, sess.Events(context.Background()))
	viaMatching := collectEvents(t, sess.EventsMatching(context.Background(), EventQuery{}))

	if len(viaEvents) != len(eventsMatchingKinds) {
		t.Fatalf("Events returned %d events, want %d", len(viaEvents), len(eventsMatchingKinds))
	}
	if len(viaMatching) != len(viaEvents) {
		t.Fatalf("EventsMatching(zero) returned %d events, Events returned %d", len(viaMatching), len(viaEvents))
	}
	for i := range viaEvents {
		a, b := viaEvents[i], viaMatching[i]
		if a.Sequence != b.Sequence || a.ID != b.ID || a.Kind != b.Kind || a.SchemaVersion != b.SchemaVersion {
			t.Errorf("event[%d]: Events = %+v, EventsMatching(zero) = %+v", i, a, b)
		}
		if !a.Timestamp.Equal(b.Timestamp) {
			t.Errorf("event[%d].Timestamp: Events = %v, EventsMatching(zero) = %v", i, a.Timestamp, b.Timestamp)
		}
		if !bytes.Equal(a.Payload, b.Payload) {
			t.Errorf("event[%d].Payload differs between Events and EventsMatching(zero)", i)
		}
		if a.Producer.GetCategory() != b.Producer.GetCategory() || a.Producer.GetName() != b.Producer.GetName() || a.Producer.GetVersion() != b.Producer.GetVersion() {
			t.Errorf("event[%d].Producer: Events = %+v, EventsMatching(zero) = %+v", i, a.Producer, b.Producer)
		}
	}
}

func TestSession_EventsMatching_invalidKindRejected(t *testing.T) {
	t.Parallel()
	sess := seedMixedEvents(t)

	q := EventQuery{Kinds: []kernelv1.EventKind{
		kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		kernelv1.EventKind_EVENT_KIND_UNSPECIFIED,
	}}

	count := 0
	var gotErr error
	for _, err := range sess.EventsMatching(context.Background(), q) {
		count++
		gotErr = err
	}
	if count != 1 {
		t.Fatalf("EventsMatching yielded %d pairs, want exactly 1", count)
	}
	if !errors.Is(gotErr, ErrInvalidKind) {
		t.Errorf("EventsMatching err = %v, want ErrInvalidKind", gotErr)
	}
}

func TestSession_EventsMatching_negativeLimitRejected(t *testing.T) {
	t.Parallel()
	sess := seedMixedEvents(t)

	count := 0
	var gotErr error
	for _, err := range sess.EventsMatching(context.Background(), EventQuery{Limit: int32Ptr(-1)}) {
		count++
		gotErr = err
	}
	if count != 1 {
		t.Fatalf("EventsMatching yielded %d pairs, want exactly 1", count)
	}
	if gotErr == nil {
		t.Error("EventsMatching (negative limit) err = nil, want an error")
	}
}

func TestSession_EventsMatching_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	count := 0
	var gotErr error
	for _, err := range sess.EventsMatching(context.Background(), EventQuery{Limit: int32Ptr(1)}) {
		count++
		gotErr = err
	}
	if count != 1 {
		t.Fatalf("EventsMatching after Close yielded %d pairs, want exactly 1", count)
	}
	if !errors.Is(gotErr, ErrClosed) {
		t.Errorf("EventsMatching after Close err = %v, want ErrClosed", gotErr)
	}
}

func TestSession_EventsMatching_stopsOnEarlyBreak(t *testing.T) {
	t.Parallel()
	sess := seedMixedEvents(t)

	count := 0
	for range sess.EventsMatching(context.Background(), EventQuery{FromSequence: int64Ptr(2)}) {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Errorf("iteration count = %d, want 3 (stopped early)", count)
	}
}

// TestBuildEventsQuery_deterministic pins the SQL an EventQuery renders to:
// identical inputs must produce a byte-identical statement every time
// (determinism.md — never a map-iteration-ordered IN list), and the
// placeholder list must match the bound argument count exactly.
func TestBuildEventsQuery_deterministic(t *testing.T) {
	t.Parallel()

	q := EventQuery{
		Kinds: []kernelv1.EventKind{
			kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,
			kernelv1.EventKind_EVENT_KIND_MESSAGE,
			kernelv1.EventKind_EVENT_KIND_TOOL_RESULT, // duplicate
			kernelv1.EventKind_EVENT_KIND_PLAN,
		},
		FromSequence: int64Ptr(4),
		Limit:        int32Ptr(2),
	}

	wantSQL := eventsSelect + " WHERE kind IN (?, ?, ?) AND sequence >= ? ORDER BY sequence LIMIT ?"
	wantArgs := []any{"tool_result", "message", "plan", int64(4), int32(2)}

	for range 20 {
		gotSQL, gotArgs, err := buildEventsQuery(q)
		if err != nil {
			t.Fatalf("buildEventsQuery: %v", err)
		}
		if gotSQL != wantSQL {
			t.Fatalf("SQL = %q, want %q", gotSQL, wantSQL)
		}
		if len(gotArgs) != len(wantArgs) {
			t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
		}
		for i := range wantArgs {
			if gotArgs[i] != wantArgs[i] {
				t.Fatalf("args[%d] = %v, want %v", i, gotArgs[i], wantArgs[i])
			}
		}
	}
}

func TestBuildEventsQuery_zeroValue(t *testing.T) {
	t.Parallel()

	gotSQL, gotArgs, err := buildEventsQuery(EventQuery{})
	if err != nil {
		t.Fatalf("buildEventsQuery: %v", err)
	}
	// Byte-identical to the unfiltered statement Events used before it was
	// reimplemented on top of EventsMatching.
	if want := eventsSelect + " ORDER BY sequence"; gotSQL != want {
		t.Errorf("SQL = %q, want %q", gotSQL, want)
	}
	if len(gotArgs) != 0 {
		t.Errorf("args = %v, want none", gotArgs)
	}
}

func TestSession_Producers_dedupAndOrder(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	toolProducer := testProducer() // category=tool, name=test-tool, version=1.0.0

	ev1 := testEvent("evt-1")
	ev1.Producer = toolProducer
	if _, err := sess.AppendEvent(context.Background(), ev1); err != nil {
		t.Fatalf("AppendEvent[1]: %v", err)
	}

	// Same producer again: must not duplicate the producers row.
	ev2 := testEvent("evt-2")
	ev2.Producer = toolProducer
	if _, err := sess.AppendEvent(context.Background(), ev2); err != nil {
		t.Fatalf("AppendEvent[2]: %v", err)
	}

	ev3 := testEvent("evt-3")
	ev3.Producer = &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_MEMORY, Name: "recall-plugin", Version: "2.0.0"}
	if _, err := sess.AppendEvent(context.Background(), ev3); err != nil {
		t.Fatalf("AppendEvent[3]: %v", err)
	}

	producers, err := sess.Producers(context.Background())
	if err != nil {
		t.Fatalf("Producers: %v", err)
	}
	if len(producers) != 2 {
		t.Fatalf("Producers = %d entries, want 2 (deduped)", len(producers))
	}
	// Deterministic order: category, name, version — "memory" sorts before "tool".
	if producers[0].GetCategory().String() == producers[1].GetCategory().String() {
		t.Errorf("Producers not distinguishable by category: %+v", producers)
	}
	if producers[0].GetName() != "recall-plugin" || producers[1].GetName() != "test-tool" {
		t.Errorf("Producers order = [%q, %q], want [%q, %q]", producers[0].GetName(), producers[1].GetName(), "recall-plugin", "test-tool")
	}
}

// TestSession_Producers_decodeErrorSurfaces plants a producers row with an
// unrecognized category directly, verifying Producers() surfaces the
// decode error instead of returning a partially-decoded slice.
func TestSession_Producers_decodeErrorSurfaces(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	seq, err := sess.AppendEvent(context.Background(), testEvent("evt-1"))
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	const q = `INSERT INTO producers (category, name, version, first_seen_sequence) VALUES (?, ?, ?, ?)`
	if _, err := sess.db.ExecContext(context.Background(), q, "not_a_category", "p", "1", seq); err != nil {
		t.Fatalf("insert producers: %v", err)
	}

	if _, err := sess.Producers(context.Background()); err == nil {
		t.Fatal("Producers (unrecognized category row) = nil error, want error")
	}
}

func TestSession_Producers_empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	producers, err := sess.Producers(context.Background())
	if err != nil {
		t.Fatalf("Producers: %v", err)
	}
	if len(producers) != 0 {
		t.Errorf("Producers (no events) = %d, want 0", len(producers))
	}
}

func TestSession_Producers_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.Producers(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("Producers after Close err = %v, want ErrClosed", err)
	}
}

func TestSession_TotalCostUSD_sumsAndZero(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	zero, err := sess.TotalCostUSD(context.Background())
	if err != nil {
		t.Fatalf("TotalCostUSD (no rows): %v", err)
	}
	if zero != 0 {
		t.Errorf("TotalCostUSD (no rows) = %v, want 0", zero)
	}

	costs := []float64{0.01, 0.02, 0.005}
	for i, c := range costs {
		ev := testEvent(fmt.Sprintf("msg-%d", i))
		ev.Kind = kernelv1.EventKind_EVENT_KIND_MESSAGE
		if _, err := sess.AppendMessage(context.Background(), ev, CostEntry{ProviderName: "p", ModelID: "m", CostUSD: c}); err != nil {
			t.Fatalf("AppendMessage[%d]: %v", i, err)
		}
	}

	total, err := sess.TotalCostUSD(context.Background())
	if err != nil {
		t.Fatalf("TotalCostUSD: %v", err)
	}
	want := 0.01 + 0.02 + 0.005
	if diff := total - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("TotalCostUSD = %v, want %v", total, want)
	}
}

func TestSession_TotalCostUSD_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.TotalCostUSD(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("TotalCostUSD after Close err = %v, want ErrClosed", err)
	}
}

func TestSession_CostLedger_orderedContents(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	entries := []CostEntry{
		{ProviderName: "anthropic", ModelID: "m1", InputTokens: 10, OutputTokens: 5, CostUSD: 0.01},
		{ProviderName: "anthropic", ModelID: "m2", InputTokens: 20, OutputTokens: 15, CostUSD: 0.02},
	}
	for i, c := range entries {
		ev := testEvent(fmt.Sprintf("msg-%d", i))
		ev.Kind = kernelv1.EventKind_EVENT_KIND_MESSAGE
		if _, err := sess.AppendMessage(context.Background(), ev, c); err != nil {
			t.Fatalf("AppendMessage[%d]: %v", i, err)
		}
	}

	got, err := sess.CostLedger(context.Background())
	if err != nil {
		t.Fatalf("CostLedger: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("CostLedger = %d entries, want %d", len(got), len(entries))
	}
	for i, want := range entries {
		if got[i] != want {
			t.Errorf("CostLedger[%d] = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestSession_CostLedger_empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	got, err := sess.CostLedger(context.Background())
	if err != nil {
		t.Fatalf("CostLedger: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("CostLedger (no rows) = %d, want 0", len(got))
	}
}

func TestSession_CostLedger_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.CostLedger(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("CostLedger after Close err = %v, want ErrClosed", err)
	}
}

func TestSession_PlanItems_orderedContents(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	items := []PlanItem{
		{TurnID: "t1", ToolCallID: "c1", ProviderName: "p", ToolName: "read_file", Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW, DecidedBy: "policy"},
		{TurnID: "t1", ToolCallID: "c2", ProviderName: "p", ToolName: "write_file", Decision: planv1.PlanDecision_PLAN_DECISION_DENY, DecidedBy: "operator"},
	}
	ev := testEvent("plan-1")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_PLAN
	if _, err := sess.AppendPlan(context.Background(), ev, items); err != nil {
		t.Fatalf("AppendPlan: %v", err)
	}

	got, err := sess.PlanItems(context.Background())
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(got) != len(items) {
		t.Fatalf("PlanItems = %d entries, want %d", len(got), len(items))
	}
	for i, want := range items {
		if got[i] != want {
			t.Errorf("PlanItems[%d] = %+v, want %+v", i, got[i], want)
		}
	}
}

// TestSession_PlanItems_decodeErrorSurfaces plants a plan_items row with
// an unrecognized decision directly, verifying PlanItems() surfaces the
// decode error instead of returning a partially-decoded slice.
func TestSession_PlanItems_decodeErrorSurfaces(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("plan-1")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_PLAN
	seq, err := sess.AppendEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	const q = `INSERT INTO plan_items (event_sequence, turn_id, tool_call_id, provider_name, tool_name, decision, decided_by) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := sess.db.ExecContext(context.Background(), q, seq, "t1", "c1", "p", "tool", "not_a_decision", "policy"); err != nil {
		t.Fatalf("insert plan_items: %v", err)
	}

	if _, err := sess.PlanItems(context.Background()); err == nil {
		t.Fatal("PlanItems (unrecognized decision row) = nil error, want error")
	}
}

func TestSession_PlanItems_empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	got, err := sess.PlanItems(context.Background())
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("PlanItems (no rows) = %d, want 0", len(got))
	}
}

func TestSession_PlanItems_errClosed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.PlanItems(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("PlanItems after Close err = %v, want ErrClosed", err)
	}
}

// TestSession_Meta_reflectsSetStatus is a small cross-check that Meta and
// SetStatus (session.go) agree on session_meta's contents.
func TestSession_Meta_reflectsSetStatus(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ended := time.Now().UTC().Truncate(time.Millisecond)
	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_FAILED, &ended); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := sess.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if got.Status != sessionv1.SessionStatus_SESSION_STATUS_FAILED {
		t.Errorf("Status = %v, want FAILED", got.Status)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(ended) {
		t.Errorf("EndedAt = %v, want %v", got.EndedAt, ended)
	}
}
