package shell

import "strings"

// input is the composer's editable buffer.
//
// It is deliberately small rather than a full editor: enough to type, correct,
// and recall a prompt, with no dependency on a terminal so every editing rule
// is unit-testable. Text is held as runes so cursor motion is
// grapheme-approximate rather than byte-indexed, which is what keeps multi-byte
// input from corrupting on backspace.
type input struct {
	text   []rune
	cursor int

	history []string
	// histIdx walks history from the end; len(history) means "not browsing",
	// which is the state a fresh keystroke always returns to.
	histIdx int
	// draft preserves what was typed before history browsing started.
	draft string
}

func newInput() *input { return &input{} }

// Value returns the current buffer contents.
func (i *input) Value() string { return string(i.text) }

// Lines reports how many display lines the buffer needs.
func (i *input) Lines() int { return strings.Count(string(i.text), "\n") + 1 }

// Insert adds runes at the cursor.
func (i *input) Insert(rs ...string) {
	for _, s := range rs {
		for _, r := range s {
			i.text = append(i.text, 0)
			copy(i.text[i.cursor+1:], i.text[i.cursor:])
			i.text[i.cursor] = r
			i.cursor++
		}
	}

	i.stopBrowsing()
}

// Backspace deletes the rune before the cursor.
func (i *input) Backspace() {
	if i.cursor == 0 {
		return
	}

	i.text = append(i.text[:i.cursor-1], i.text[i.cursor:]...)
	i.cursor--
	i.stopBrowsing()
}

// Left moves the cursor one rune left.
func (i *input) Left() {
	if i.cursor > 0 {
		i.cursor--
	}
}

// Right moves the cursor one rune right.
func (i *input) Right() {
	if i.cursor < len(i.text) {
		i.cursor++
	}
}

// Home moves the cursor to the start of the buffer.
func (i *input) Home() { i.cursor = 0 }

// End moves the cursor to the end of the buffer.
func (i *input) End() { i.cursor = len(i.text) }

// Empty reports whether the buffer has no content.
func (i *input) Empty() bool { return len(i.text) == 0 }

// OnFirstLine reports whether the cursor sits on the buffer's first line,
// which is what makes "up" mean history rather than cursor motion.
func (i *input) OnFirstLine() bool {
	return !strings.Contains(string(i.text[:i.cursor]), "\n")
}

// OnLastLine reports whether the cursor sits on the buffer's last line.
func (i *input) OnLastLine() bool {
	return !strings.Contains(string(i.text[i.cursor:]), "\n")
}

// Submit returns the buffer, records it in history, and clears the composer.
// An all-whitespace buffer returns ok false and is neither sent nor recorded.
func (i *input) Submit() (string, bool) {
	v := strings.TrimSpace(string(i.text))
	if v == "" {
		return "", false
	}

	i.history = append(i.history, v)
	i.text = nil
	i.cursor = 0
	i.draft = ""
	i.histIdx = len(i.history)

	return v, true
}

// HistoryPrev recalls the previous entry, preserving the in-progress draft on
// the first step back so browsing away and back is non-destructive.
func (i *input) HistoryPrev() {
	if len(i.history) == 0 || i.histIdx == 0 {
		return
	}

	if i.histIdx == len(i.history) {
		i.draft = string(i.text)
	}

	i.histIdx--
	i.set(i.history[i.histIdx])
}

// HistoryNext walks forward, restoring the preserved draft past the newest
// entry.
func (i *input) HistoryNext() {
	if i.histIdx >= len(i.history) {
		return
	}

	i.histIdx++
	if i.histIdx == len(i.history) {
		i.set(i.draft)

		return
	}

	i.set(i.history[i.histIdx])
}

func (i *input) set(s string) {
	i.text = []rune(s)
	i.cursor = len(i.text)
}

// stopBrowsing returns the buffer to "editing a draft" state, so a keystroke
// during history browsing keeps what is on screen instead of snapping back.
func (i *input) stopBrowsing() { i.histIdx = len(i.history) }

// CursorPos reports the cursor's line and column within the buffer, both
// zero-based.
//
// The shell turns this into an absolute screen position and hands it to Bubble
// Tea as the real terminal cursor, which is why the buffer renders no caret
// glyph of its own: a drawn caret and a real cursor would both be visible.
func (i *input) CursorPos() (line, col int) {
	for _, r := range i.text[:i.cursor] {
		if r == '\n' {
			line++
			col = 0

			continue
		}

		col++
	}

	return line, col
}

// render draws the buffer. Placeholder text stands in when the buffer is empty
// and the composer has focus, so the pane never looks broken.
func (i *input) render(placeholder string) string {
	if i.Empty() {
		return placeholder
	}

	return string(i.text)
}
