package shell

import "testing"

func TestInsertAndBackspace(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("h", "i")

	if got := in.Value(); got != "hi" {
		t.Fatalf("Value = %q, want %q", got, "hi")
	}

	in.Backspace()

	if got := in.Value(); got != "h" {
		t.Fatalf("after backspace = %q, want %q", got, "h")
	}

	in.Backspace()
	in.Backspace() // past the start is a no-op, not a panic

	if !in.Empty() {
		t.Fatalf("expected empty buffer, got %q", in.Value())
	}
}

// Multi-byte input must not corrupt on backspace, which is why the buffer is
// held as runes rather than bytes.
func TestMultiByteEditing(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("héllo→")
	in.Backspace()

	if got := in.Value(); got != "héllo" {
		t.Fatalf("multi-byte backspace corrupted the buffer: %q", got)
	}
}

func TestCursorMovementAndMidBufferInsert(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("ac")
	in.Left()
	in.Insert("b")

	if got := in.Value(); got != "abc" {
		t.Fatalf("mid-buffer insert = %q, want %q", got, "abc")
	}

	in.Home()
	in.Insert(">")

	if got := in.Value(); got != ">abc" {
		t.Fatalf("insert at home = %q, want %q", got, ">abc")
	}

	in.End()
	in.Insert("<")

	if got := in.Value(); got != ">abc<" {
		t.Fatalf("insert at end = %q, want %q", got, ">abc<")
	}

	// Motion past either boundary is a no-op.
	for range 10 {
		in.Left()
	}

	in.Left()

	for range 20 {
		in.Right()
	}

	in.Insert("!")

	if got := in.Value(); got != ">abc<!" {
		t.Fatalf("boundary motion misbehaved: %q", got)
	}
}

func TestLinesCountsNewlines(t *testing.T) {
	t.Parallel()

	in := newInput()
	if got := in.Lines(); got != 1 {
		t.Fatalf("empty buffer Lines = %d, want 1", got)
	}

	in.Insert("a\nb\nc")

	if got := in.Lines(); got != 3 {
		t.Fatalf("Lines = %d, want 3", got)
	}
}

func TestSubmitTrimsRecordsAndClears(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("  hello  ")

	got, ok := in.Submit()
	if !ok || got != "hello" {
		t.Fatalf("Submit = (%q, %v), want (%q, true)", got, ok, "hello")
	}

	if !in.Empty() {
		t.Fatalf("Submit left %q in the buffer", in.Value())
	}
}

// An all-whitespace buffer is neither sent nor recorded in history.
func TestSubmitRejectsBlank(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("   \n  ")

	if got, ok := in.Submit(); ok {
		t.Fatalf("blank Submit returned (%q, true), want ok false", got)
	}

	if len(in.history) != 0 {
		t.Fatalf("blank submission entered history: %v", in.history)
	}

	// A rejected submission keeps the buffer: nothing was sent, so nothing
	// should be lost.
	if in.Empty() {
		t.Fatal("blank Submit cleared the buffer, discarding what was typed")
	}
}

func TestHistoryWalksBothWays(t *testing.T) {
	t.Parallel()

	in := newInput()

	for _, s := range []string{"first", "second"} {
		in.Insert(s)
		in.Submit()
	}

	in.HistoryPrev()

	if got := in.Value(); got != "second" {
		t.Fatalf("first HistoryPrev = %q, want %q", got, "second")
	}

	in.HistoryPrev()

	if got := in.Value(); got != "first" {
		t.Fatalf("second HistoryPrev = %q, want %q", got, "first")
	}

	in.HistoryPrev() // past the oldest is a no-op

	if got := in.Value(); got != "first" {
		t.Fatalf("over-walking history = %q, want %q", got, "first")
	}

	in.HistoryNext()

	if got := in.Value(); got != "second" {
		t.Fatalf("HistoryNext = %q, want %q", got, "second")
	}
}

// Browsing away from an in-progress draft and back must not destroy it.
func TestHistoryPreservesDraft(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("recalled")
	in.Submit()

	in.Insert("draft in progress")
	in.HistoryPrev()

	if got := in.Value(); got != "recalled" {
		t.Fatalf("HistoryPrev = %q, want %q", got, "recalled")
	}

	in.HistoryNext()

	if got := in.Value(); got != "draft in progress" {
		t.Fatalf("draft was not restored: %q", got)
	}
}

// Typing during history browsing keeps what is on screen rather than snapping
// back to the draft.
func TestTypingStopsHistoryBrowsing(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("recalled")
	in.Submit()

	in.HistoryPrev()
	in.Insert("!")

	if got := in.Value(); got != "recalled!" {
		t.Fatalf("edit during browsing = %q, want %q", got, "recalled!")
	}

	in.HistoryNext()

	if got := in.Value(); got != "recalled!" {
		t.Fatalf("HistoryNext after editing discarded the edit: %q", got)
	}
}

func TestHistoryOnEmptyHistoryIsNoOp(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.HistoryPrev()
	in.HistoryNext()

	if !in.Empty() {
		t.Fatalf("history navigation on an empty history produced %q", in.Value())
	}
}

// "up" means history only when the cursor is on the first line; otherwise it
// is ordinary cursor motion.
func TestLinePositionPredicates(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("one\ntwo")

	if !in.OnLastLine() {
		t.Error("cursor at end should be on the last line")
	}

	if in.OnFirstLine() {
		t.Error("cursor at end of a two-line buffer should not be on the first line")
	}

	in.Home()

	if !in.OnFirstLine() {
		t.Error("cursor at home should be on the first line")
	}

	if in.OnLastLine() {
		t.Error("cursor at home of a two-line buffer should not be on the last line")
	}
}

// The buffer draws no caret of its own: the shell places the real terminal
// cursor instead, and a drawn caret would be visible alongside it.
func TestRenderDrawsNoCaret(t *testing.T) {
	t.Parallel()

	in := newInput()
	in.Insert("ab")
	in.Left()

	if got := in.render(""); got != "ab" {
		t.Fatalf("render = %q, want %q", got, "ab")
	}
}

func TestRenderShowsPlaceholderOnlyWhenEmpty(t *testing.T) {
	t.Parallel()

	in := newInput()
	if got := in.render("type here"); got != "type here" {
		t.Fatalf("empty render = %q, want the placeholder", got)
	}

	in.Insert("x")

	if got := in.render("type here"); got != "x" {
		t.Fatalf("non-empty render = %q, want %q", got, "x")
	}
}

func TestCursorPosTracksLineAndColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		back       int
		wantLine   int
		wantColumn int
	}{
		{"empty", "", 0, 0, 0},
		{"end of one line", "abc", 0, 0, 3},
		{"mid first line", "abc", 2, 0, 1},
		{"start of second line", "ab\n", 0, 1, 0},
		{"mid second line", "ab\ncd", 1, 1, 1},
		{"third line", "a\nb\ncd", 0, 2, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := newInput()
			in.Insert(tc.text)

			for range tc.back {
				in.Left()
			}

			line, col := in.CursorPos()
			if line != tc.wantLine || col != tc.wantColumn {
				t.Fatalf("CursorPos() = (%d,%d), want (%d,%d)", line, col, tc.wantLine, tc.wantColumn)
			}
		})
	}
}
