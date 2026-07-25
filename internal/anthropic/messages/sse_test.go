package messages

import (
	"errors"
	"strings"
	"testing"
)

// scanAll drains s, returning every decoded event and the terminal error
// (nil at a clean EOF).
func scanAll(t *testing.T, s *Scanner) ([]StreamEvent, error) {
	t.Helper()
	var events []StreamEvent
	for s.Next() {
		events = append(events, s.Event())
	}
	return events, s.Err()
}

func TestScanner_singleEvent(t *testing.T) {
	t.Parallel()

	raw := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != eventContentBlockDelta || ev.Index != 0 || ev.Delta == nil || ev.Delta.Text != "Hello" {
		t.Fatalf("decoded event = %+v", ev)
	}
}

func TestScanner_multipleEventsInOrder(t *testing.T) {
	t.Parallel()

	raw := "data: {\"type\":\"ping\"}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"a\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"b\"}}\n\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{eventPing, eventContentBlockDelta, eventContentBlockDelta}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for i, w := range want {
		if events[i].Type != w {
			t.Errorf("event %d: type = %q, want %q", i, events[i].Type, w)
		}
	}
	if events[1].Delta.Text != "a" || events[2].Delta.Text != "b" {
		t.Fatalf("delta text mismatch: %+v", events)
	}
}

func TestScanner_pingSurfaced(t *testing.T) {
	t.Parallel()

	// Scanner surfaces ping rather than filtering it — the translator
	// (events.go) is where ping is dropped, so this is the seam that
	// proves the split.
	events, err := scanAll(t, NewScanner(strings.NewReader("data: {\"type\":\"ping\"}\n\n")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != eventPing {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanner_commentLinesIgnored(t *testing.T) {
	t.Parallel()

	raw := ": this is a comment\n" +
		"data: {\"type\":\"ping\"}\n" +
		": another comment\n" +
		"\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != eventPing {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanner_extraBlankLinesDoNotProduceEmptyEvents(t *testing.T) {
	t.Parallel()

	raw := "\n\n\ndata: {\"type\":\"ping\"}\n\n\n\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != eventPing {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanner_multiLineDataConcatenatesWithNewline(t *testing.T) {
	t.Parallel()

	// SSE joins repeated data: lines with "\n" before parsing; JSON
	// tolerates the resulting whitespace between tokens, so this must
	// still decode to {"type":"ping"}.
	raw := "data: {\"type\":\n" + "data: \"ping\"}\n\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != eventPing {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanner_trailingEventWithoutBlankLine(t *testing.T) {
	t.Parallel()

	// The stream ends immediately after the final event's data, with no
	// terminating blank line before EOF.
	raw := "data: {\"type\":\"ping\"}"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != eventPing {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanner_unparseableDataIsAnError(t *testing.T) {
	t.Parallel()

	s := NewScanner(strings.NewReader("data: {not valid json\n\n"))
	if s.Next() {
		t.Fatalf("Next() = true for unparseable data, want false")
	}
	if s.Err() == nil {
		t.Fatalf("Err() = nil, want a decode error")
	}
}

func TestScanner_cleanEOFReturnsNilErr(t *testing.T) {
	t.Parallel()

	s := NewScanner(strings.NewReader(""))
	if s.Next() {
		t.Fatalf("Next() = true for empty input, want false")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestScanner_oversizedEventPastDefaultLineCap(t *testing.T) {
	t.Parallel()

	// bufio.Scanner's default 64 KiB line cap would silently truncate
	// (and error on) a data: line this large; NewScanner raises the
	// buffer specifically so this must still decode cleanly.
	big := strings.Repeat("A", 200*1024)
	raw := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"" + big + "\"}}\n\n"
	events, err := scanAll(t, NewScanner(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.ContentBlock == nil || len(ev.ContentBlock.Data) != len(big) {
		t.Fatalf("redacted_thinking data length = %d, want %d", len(ev.ContentBlock.Data), len(big))
	}
}

func TestScanner_readError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	s := NewScanner(&erroringReader{err: wantErr})
	if s.Next() {
		t.Fatalf("Next() = true, want false")
	}
	if err := s.Err(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Err() = %v, want wrapping %v", err, wantErr)
	}
}

// erroringReader always returns err on Read, simulating a transport-level
// failure mid-stream.
type erroringReader struct {
	err error
}

func (r *erroringReader) Read([]byte) (int, error) {
	return 0, r.err
}
