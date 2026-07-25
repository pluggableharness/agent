package statebackend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// testSessionMeta returns a minimal, valid SessionMeta with a fresh
// session ID, for tests that only care about a session existing.
func testSessionMeta() SessionMeta {
	return SessionMeta{
		SessionID: NewSessionID(time.Now()),
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: time.Now(),
	}
}

// testProducer returns a minimal, valid producer reference.
func testProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "test-tool",
		Version:  "1.0.0",
	}
}

// testEvent returns a minimal, valid Event with the given ID.
func testEvent(id string) Event {
	return Event{
		ID:            id,
		Timestamp:     time.Now(),
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		Producer:      testProducer(),
		SchemaVersion: "v1",
		Payload:       []byte(`{"ok":true}`),
	}
}

func TestSession_AppendEvent(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("evt-1")
	seq, err := sess.AppendEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if seq != 1 {
		t.Fatalf("sequence = %d, want 1", seq)
	}

	var (
		id, kindText, category, name, version, schemaVersion string
		payload                                              []byte
	)
	row := sess.db.QueryRowContext(context.Background(),
		"SELECT id, kind, producer_category, producer_name, producer_version, schema_version, payload FROM events WHERE sequence = ?", seq)
	if err := row.Scan(&id, &kindText, &category, &name, &version, &schemaVersion, &payload); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if id != ev.ID {
		t.Errorf("id = %q, want %q", id, ev.ID)
	}
	if kindText != "tool_call" {
		t.Errorf("kind = %q, want %q", kindText, "tool_call")
	}
	if category != "tool" || name != ev.Producer.Name || version != ev.Producer.Version {
		t.Errorf("producer = (%q, %q, %q), want (%q, %q, %q)", category, name, version, "tool", ev.Producer.Name, ev.Producer.Version)
	}
	if schemaVersion != ev.SchemaVersion {
		t.Errorf("schema_version = %q, want %q", schemaVersion, ev.SchemaVersion)
	}
	if string(payload) != string(ev.Payload) {
		t.Errorf("payload = %q, want %q", payload, ev.Payload)
	}

	// The producers table must have gained exactly one row for this triple.
	var producerCount int
	if err := sess.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM producers WHERE category = ? AND name = ? AND version = ?", category, name, version).Scan(&producerCount); err != nil {
		t.Fatalf("query producers: %v", err)
	}
	if producerCount != 1 {
		t.Errorf("producers rows for (%q,%q,%q) = %d, want 1", category, name, version, producerCount)
	}
}

func TestSession_AppendEvent_sequential(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	for i := 1; i <= 5; i++ {
		seq, err := sess.AppendEvent(context.Background(), testEvent(fmt.Sprintf("evt-%d", i)))
		if err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
		if seq != int64(i) {
			t.Fatalf("sequence[%d] = %d, want %d", i, seq, i)
		}
	}
}

// TestSession_AppendEvent_concurrentSequencesAreExactlyOneToN is the
// required race test: N goroutines append concurrently on the same
// Session, and the set of returned sequences must be exactly {1..N} with
// no gaps or duplicates — proving the sole-writer connection (SetMaxOpenConns(1))
// serializes concurrent transactions correctly. Run under -race.
func TestSession_AppendEvent_concurrentSequencesAreExactlyOneToN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	const n = 50
	var wg sync.WaitGroup
	seqs := make([]int64, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			seq, err := sess.AppendEvent(context.Background(), testEvent(fmt.Sprintf("evt-%d", idx)))
			seqs[idx] = seq
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		want := int64(i + 1)
		if seq != want {
			t.Fatalf("sequences = %v, want exactly 1..%d with no gaps/dupes (sorted index %d = %d, want %d)", seqs, n, i, seq, want)
		}
	}
}

// TestSession_concurrentReaderDuringWrites exercises WAL mode: a second,
// independent connection to the same file must be able to read while the
// Session's sole-writer connection is actively appending — never blocked
// by, or blocking, the writer (docs/specifications/state-backend.md#ordering--concurrency).
func TestSession_concurrentReaderDuringWrites(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess := createSession(t, st, meta)
	path := st.sessionPath(meta.SessionID)

	const n = 30
	writeErrs := make(chan error, 1)
	go func() {
		defer close(writeErrs)
		for i := range n {
			if _, err := sess.AppendEvent(context.Background(), testEvent(fmt.Sprintf("evt-%d", i))); err != nil {
				writeErrs <- err
				return
			}
		}
	}()

	readerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open (reader): %v", err)
	}
	t.Cleanup(func() { _ = readerDB.Close() })

	for range n {
		var count int
		if err := readerDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
			t.Fatalf("concurrent read during writes: %v", err)
		}
	}

	if err := <-writeErrs; err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}

func TestSession_AppendEvent_duplicateID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("dup-event")
	seq1, err := sess.AppendEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("AppendEvent (first): %v", err)
	}
	if seq1 != 1 {
		t.Fatalf("first sequence = %d, want 1", seq1)
	}

	if _, err := sess.AppendEvent(context.Background(), ev); !errors.Is(err, ErrDuplicateEventID) {
		t.Fatalf("AppendEvent (duplicate id) err = %v, want ErrDuplicateEventID", err)
	}

	// The failed duplicate insert's transaction was rolled back entirely,
	// so it must not have consumed a sequence value — the next append
	// gets 2, not 3.
	seq2, err := sess.AppendEvent(context.Background(), testEvent("not-a-dup"))
	if err != nil {
		t.Fatalf("AppendEvent (second): %v", err)
	}
	if seq2 != 2 {
		t.Errorf("sequence after a failed duplicate append = %d, want 2 (no gap)", seq2)
	}
}

func TestSession_AppendEvent_invalidKind(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("evt-1")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_UNSPECIFIED
	if _, err := sess.AppendEvent(context.Background(), ev); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("AppendEvent (unspecified kind) err = %v, want ErrInvalidKind", err)
	}
}

func TestSession_AppendEvent_missingProducer(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("evt-1")
	ev.Producer = nil
	if _, err := sess.AppendEvent(context.Background(), ev); err == nil {
		t.Fatal("AppendEvent (nil producer) = nil error, want error")
	}
}

// TestSession_KernelProducer_roundTrip is the required end-to-end check for
// the reserved kernel identity: a plan event appended under it must read
// back as the kernel, and must sit alongside a real plugin producer in the
// same session's manifest without either being confused for the other.
func TestSession_KernelProducer_roundTrip(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	items := []PlanItem{
		{TurnID: "t1", ToolCallID: "c1", ProviderName: "ripgrep", ToolName: "search", Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW, DecidedBy: "policy"},
		{TurnID: "t1", ToolCallID: "c2", ProviderName: "fs", ToolName: "write_file", Decision: planv1.PlanDecision_PLAN_DECISION_DENY, DecidedBy: "policy"},
	}
	planEvent := testEvent(NewEventID(time.Now()))
	planEvent.Kind = kernelv1.EventKind_EVENT_KIND_PLAN
	planEvent.Producer = KernelProducer()
	if _, err := sess.AppendPlan(context.Background(), planEvent, items); err != nil {
		t.Fatalf("AppendPlan (kernel producer): %v", err)
	}

	applyEvent := testEvent(NewEventID(time.Now()))
	applyEvent.Kind = kernelv1.EventKind_EVENT_KIND_APPLY
	applyEvent.Producer = KernelProducer()
	if _, err := sess.AppendEvent(context.Background(), applyEvent); err != nil {
		t.Fatalf("AppendEvent (kernel producer, apply): %v", err)
	}

	// A real plugin producer in the same file, to prove the two coexist.
	toolEvent := testEvent(NewEventID(time.Now()))
	if _, err := sess.AppendEvent(context.Background(), toolEvent); err != nil {
		t.Fatalf("AppendEvent (tool producer): %v", err)
	}

	got := collectEvents(t, sess.Events(context.Background()))
	if len(got) != 3 {
		t.Fatalf("Events returned %d events, want 3", len(got))
	}
	for i, ev := range got[:2] {
		if !IsKernelProducer(ev.Producer) {
			t.Errorf("event[%d].Producer = %+v, want the kernel producer", i, ev.Producer)
		}
		if ev.Producer.GetName() != "kernel" || ev.Producer.GetVersion() != "1" || ev.Producer.GetCategory() != commonv1.Category_CATEGORY_UNSPECIFIED {
			t.Errorf("event[%d].Producer = %+v, want %+v", i, ev.Producer, KernelProducer())
		}
	}
	if IsKernelProducer(got[2].Producer) {
		t.Errorf("event[2].Producer = %+v, want the real tool producer", got[2].Producer)
	}
	if got[2].Producer.GetCategory() != commonv1.Category_CATEGORY_TOOL {
		t.Errorf("event[2].Producer.Category = %v, want CATEGORY_TOOL", got[2].Producer.GetCategory())
	}

	// The producers manifest carries the kernel exactly once, deduped like
	// any other producer, alongside the real plugin.
	producers, err := sess.Producers(context.Background())
	if err != nil {
		t.Fatalf("Producers: %v", err)
	}
	if len(producers) != 2 {
		t.Fatalf("Producers = %d entries, want 2 (kernel + tool)", len(producers))
	}
	kernelSeen := 0
	for _, p := range producers {
		if IsKernelProducer(p) {
			kernelSeen++
			if p.GetVersion() != "1" {
				t.Errorf("kernel producer version = %q, want %q", p.GetVersion(), "1")
			}
		}
	}
	if kernelSeen != 1 {
		t.Errorf("kernel producer appears %d times in the manifest, want exactly 1", kernelSeen)
	}

	// The plan items the kernel-produced plan event carried are intact.
	planItems, err := sess.PlanItems(context.Background())
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(planItems) != len(items) {
		t.Fatalf("PlanItems = %d, want %d", len(planItems), len(items))
	}

	// Filtering by kind finds the kernel's own events without any special
	// casing at the query layer.
	kernelEvents := collectEvents(t, sess.EventsMatching(context.Background(), EventQuery{
		Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_PLAN, kernelv1.EventKind_EVENT_KIND_APPLY},
	}))
	if len(kernelEvents) != 2 {
		t.Fatalf("EventsMatching(plan, apply) = %d events, want 2", len(kernelEvents))
	}
}

// TestSession_AppendEvent_kernelProducerKindGate pins the write-path half
// of the reserved kernel identity: it is storable on plan and apply events
// only, and nothing is written when it is refused.
func TestSession_AppendEvent_kernelProducerKindGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    kernelv1.EventKind
		wantErr bool
	}{
		{"plan", kernelv1.EventKind_EVENT_KIND_PLAN, false},
		{"apply", kernelv1.EventKind_EVENT_KIND_APPLY, false},
		{"tool_call", kernelv1.EventKind_EVENT_KIND_TOOL_CALL, true},
		{"message", kernelv1.EventKind_EVENT_KIND_MESSAGE, true},
		{"hook_error", kernelv1.EventKind_EVENT_KIND_HOOK_ERROR, true},
		{"memory_write", kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			sess := createSession(t, st, testSessionMeta())

			ev := testEvent("evt-1")
			ev.Kind = tt.kind
			ev.Producer = KernelProducer()

			_, err := sess.AppendEvent(context.Background(), ev)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidProducer) {
					t.Fatalf("AppendEvent (kernel producer on %v) err = %v, want ErrInvalidProducer", tt.kind, err)
				}
				var count int
				if scanErr := sess.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); scanErr != nil {
					t.Fatalf("count events: %v", scanErr)
				}
				if count != 0 {
					t.Errorf("events table holds %d rows after a refused append, want 0", count)
				}
				return
			}
			if err != nil {
				t.Fatalf("AppendEvent (kernel producer on %v): %v", tt.kind, err)
			}
		})
	}
}

// TestSession_AppendEvent_unspecifiedCategoryStillRejected is the
// regression guard for the invariant the kernel identity must not weaken:
// CATEGORY_UNSPECIFIED remains unwritable for any producer that is not
// exactly the reserved kernel identity, on every kind — including plan.
func TestSession_AppendEvent_unspecifiedCategoryStillRejected(t *testing.T) {
	t.Parallel()

	for _, kind := range []kernelv1.EventKind{
		kernelv1.EventKind_EVENT_KIND_PLAN,
		kernelv1.EventKind_EVENT_KIND_APPLY,
		kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			sess := createSession(t, st, testSessionMeta())

			ev := testEvent("evt-1")
			ev.Kind = kind
			ev.Producer = &commonv1.ProducerRef{Name: "not-the-kernel", Version: "1.0.0"}

			if _, err := sess.AppendEvent(context.Background(), ev); !errors.Is(err, ErrInvalidProducer) {
				t.Fatalf("AppendEvent (unspecified category, %v) err = %v, want ErrInvalidProducer", kind, err)
			}
		})
	}
}

// TestSession_appendEventTx_extraFailureRollsBackEvent tests the
// same-tx-atomicity mechanism AppendMessage and AppendPlan both build on
// directly: if the "extra" step (cost_ledger or plan_items insert) fails,
// the event row itself must not exist either — the whole append is one
// transaction, not "insert the event, then try to insert the rest."
func TestSession_appendEventTx_extraFailureRollsBackEvent(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("rolled-back")
	boom := errors.New("boom")
	_, err := sess.appendEventTx(context.Background(), ev, func(context.Context, *sql.Tx, int64) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("appendEventTx err = %v, want wrapping %v", err, boom)
	}

	var count int
	if scanErr := sess.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE id = ?", ev.ID).Scan(&count); scanErr != nil {
		t.Fatalf("query events: %v", scanErr)
	}
	if count != 0 {
		t.Error("events row exists after a failed same-tx append, want it rolled back")
	}
}

func TestSession_AppendMessage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("msg-event")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_MESSAGE
	cost := CostEntry{
		ProviderName:     "anthropic",
		ModelID:          "claude-x",
		InputTokens:      100,
		OutputTokens:     50,
		CacheWriteTokens: 5,
		CacheReadTokens:  10,
		CostUSD:          0.0123,
	}

	seq, err := sess.AppendMessage(context.Background(), ev, cost)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	var (
		gotEventSeq                                      int64
		gotProvider, gotModel                            string
		gotInput, gotOutput, gotCacheWrite, gotCacheRead int64
		gotCost                                          float64
	)
	row := sess.db.QueryRowContext(context.Background(),
		"SELECT event_sequence, provider_name, model_id, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, cost_usd FROM cost_ledger WHERE event_sequence = ?", seq)
	if err := row.Scan(&gotEventSeq, &gotProvider, &gotModel, &gotInput, &gotOutput, &gotCacheWrite, &gotCacheRead, &gotCost); err != nil {
		t.Fatalf("query cost_ledger: %v", err)
	}
	if gotEventSeq != seq {
		t.Errorf("event_sequence = %d, want %d", gotEventSeq, seq)
	}
	if gotProvider != cost.ProviderName || gotModel != cost.ModelID {
		t.Errorf("provider/model = (%q, %q), want (%q, %q)", gotProvider, gotModel, cost.ProviderName, cost.ModelID)
	}
	if gotInput != cost.InputTokens || gotOutput != cost.OutputTokens || gotCacheWrite != cost.CacheWriteTokens || gotCacheRead != cost.CacheReadTokens {
		t.Errorf("token counts = (%d,%d,%d,%d), want (%d,%d,%d,%d)", gotInput, gotOutput, gotCacheWrite, gotCacheRead, cost.InputTokens, cost.OutputTokens, cost.CacheWriteTokens, cost.CacheReadTokens)
	}
	if gotCost != cost.CostUSD {
		t.Errorf("cost_usd = %v, want %v", gotCost, cost.CostUSD)
	}
}

func TestSession_AppendPlan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("plan-event")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_PLAN
	items := []PlanItem{
		{TurnID: "t1", ToolCallID: "c1", ProviderName: "p", ToolName: "read_file", Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW, DecidedBy: "policy"},
		{TurnID: "t1", ToolCallID: "c2", ProviderName: "p", ToolName: "write_file", Decision: planv1.PlanDecision_PLAN_DECISION_ASK, DecidedBy: "operator"},
	}

	seq, err := sess.AppendPlan(context.Background(), ev, items)
	if err != nil {
		t.Fatalf("AppendPlan: %v", err)
	}

	rows, err := sess.db.QueryContext(context.Background(), "SELECT tool_name, decision, decided_by FROM plan_items WHERE event_sequence = ? ORDER BY sequence", seq)
	if err != nil {
		t.Fatalf("query plan_items: %v", err)
	}
	defer rows.Close()

	var got []PlanItem
	for rows.Next() {
		var toolName, decisionText, decidedBy string
		if err := rows.Scan(&toolName, &decisionText, &decidedBy); err != nil {
			t.Fatalf("scan: %v", err)
		}
		decision, err := decodePlanDecision(decisionText)
		if err != nil {
			t.Fatalf("decodePlanDecision(%q): %v", decisionText, err)
		}
		got = append(got, PlanItem{ToolName: toolName, Decision: decision, DecidedBy: decidedBy})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != len(items) {
		t.Fatalf("plan_items rows = %d, want %d", len(got), len(items))
	}
	for i, item := range items {
		if got[i].ToolName != item.ToolName || got[i].Decision != item.Decision || got[i].DecidedBy != item.DecidedBy {
			t.Errorf("plan_items[%d] = %+v, want %+v", i, got[i], item)
		}
	}
}

func TestSession_AppendPlan_invalidDecisionRollsBackEvent(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	ev := testEvent("plan-event")
	ev.Kind = kernelv1.EventKind_EVENT_KIND_PLAN
	items := []PlanItem{
		{TurnID: "t1", ToolCallID: "c1", ProviderName: "p", ToolName: "read_file", Decision: planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED, DecidedBy: "policy"},
	}

	if _, err := sess.AppendPlan(context.Background(), ev, items); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("AppendPlan (invalid decision) err = %v, want ErrInvalidDecision", err)
	}

	var count int
	if scanErr := sess.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE id = ?", ev.ID).Scan(&count); scanErr != nil {
		t.Fatalf("query events: %v", scanErr)
	}
	if count != 0 {
		t.Error("events row exists after AppendPlan's plan_items insert failed, want it rolled back")
	}
}

func TestSession_SetStatus_roundTrip(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess := createSession(t, st, meta)

	// Truncated to millisecond precision: formatTimestamp's on-disk layout
	// is millisecond precision, so a nanosecond-precision time.Now() would
	// never compare equal after the round trip.
	ended := time.Now().UTC().Truncate(time.Millisecond)
	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, &ended); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := querySessionMeta(context.Background(), sess.db, meta.SessionID)
	if err != nil {
		t.Fatalf("querySessionMeta: %v", err)
	}
	if got.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Errorf("Status = %v, want COMPLETED", got.Status)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(ended) {
		t.Errorf("EndedAt = %v, want %v", got.EndedAt, ended)
	}
}

func TestSession_SetStatus_stillRunningLeavesEndedAtNil(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess := createSession(t, st, meta)

	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_RUNNING, nil); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := querySessionMeta(context.Background(), sess.db, meta.SessionID)
	if err != nil {
		t.Fatalf("querySessionMeta: %v", err)
	}
	if got.EndedAt != nil {
		t.Errorf("EndedAt = %v, want nil", got.EndedAt)
	}
}

func TestSession_SetStatus_invalidStatus(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess := createSession(t, st, testSessionMeta())

	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED, nil); err == nil {
		t.Fatal("SetStatus (unspecified) = nil error, want error")
	}
}

func TestSession_SetStatus_notFound(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess := createSession(t, st, meta)

	if _, err := sess.db.ExecContext(context.Background(), "DELETE FROM session_meta WHERE session_id = ?", meta.SessionID); err != nil {
		t.Fatalf("delete session_meta: %v", err)
	}

	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStatus (no row) err = %v, want ErrNotFound", err)
	}
}

func TestSession_Close_idempotent(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close (second) = %v, want nil (idempotent)", err)
	}
}

func TestSession_errClosedAfterClose(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	sess, err := st.Create(context.Background(), testSessionMeta())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := sess.AppendEvent(context.Background(), testEvent("e1")); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendEvent after Close err = %v, want ErrClosed", err)
	}
	if _, err := sess.AppendMessage(context.Background(), testEvent("e2"), CostEntry{}); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendMessage after Close err = %v, want ErrClosed", err)
	}
	if _, err := sess.AppendPlan(context.Background(), testEvent("e3"), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendPlan after Close err = %v, want ErrClosed", err)
	}
	if err := sess.SetStatus(context.Background(), sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("SetStatus after Close err = %v, want ErrClosed", err)
	}
}
