package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Style is a chainable utility builder. Each method sets exactly one property
// and returns a new Style, so appearance composes at the point of use:
//
//	ui.New().Bg(t.C.BackgroundPanel).Fg(t.C.TextMuted).Px(theme.Space1).Render(s)
//
// Values are expected to come from the theme's token set and spacing scale
// rather than from literals — that constraint is the whole point.
type Style struct{ s lipgloss.Style }

// New returns an empty utility style.
func New() Style { return Style{s: lipgloss.NewStyle()} }

// From wraps an existing Lip Gloss style so painter-derived styles can be
// extended with utilities without being rebuilt.
func From(s lipgloss.Style) Style { return Style{s: s} }

// Fg sets the foreground color.
func (u Style) Fg(c color.Color) Style { return Style{s: u.s.Foreground(c)} }

// Bg sets the background color.
func (u Style) Bg(c color.Color) Style { return Style{s: u.s.Background(c)} }

// P sets padding on all four sides.
func (u Style) P(n int) Style { return Style{s: u.s.Padding(n, n)} }

// Px sets horizontal padding.
func (u Style) Px(n int) Style { return Style{s: u.s.PaddingLeft(n).PaddingRight(n)} }

// Py sets vertical padding.
func (u Style) Py(n int) Style { return Style{s: u.s.PaddingTop(n).PaddingBottom(n)} }

// W sets an exact width, padding or wrapping content to fit.
func (u Style) W(n int) Style { return Style{s: u.s.Width(n)} }

// H sets an exact height, padding with blank lines to fit.
func (u Style) H(n int) Style { return Style{s: u.s.Height(n)} }

// MaxW clips content to a width without padding it out to that width.
func (u Style) MaxW(n int) Style { return Style{s: u.s.MaxWidth(n)} }

// Bold enables bold text.
func (u Style) Bold() Style { return Style{s: u.s.Bold(true)} }

// Italic enables italic text.
func (u Style) Italic() Style { return Style{s: u.s.Italic(true)} }

// Underline enables underlined text.
func (u Style) Underline() Style { return Style{s: u.s.Underline(true)} }

// Align sets horizontal alignment within the style's width.
func (u Style) Align(p lipgloss.Position) Style { return Style{s: u.s.AlignHorizontal(p)} }

// Render applies the style to a string.
func (u Style) Render(s string) string { return u.s.Render(s) }

// Lip returns the underlying Lip Gloss style, for the rare call that needs a
// property this builder deliberately does not expose.
func (u Style) Lip() lipgloss.Style { return u.s }

// Width reports the display width of a rendered string, counting grapheme
// widths and ignoring ANSI escapes.
func Width(s string) int { return lipgloss.Width(s) }

// TabWidth is how many spaces a tab expands to.
const TabWidth = 4

// ExpandTabs replaces tabs with spaces.
//
// This is not cosmetic. Width measurement counts a tab as zero cells, but a
// terminal advances the cursor to the next tab stop when it draws one, so any
// content containing a tab paints wider than it measures — which overflows its
// pane and corrupts every row to its right. Producer content routinely contains
// tabs (Go source, diffs), so expansion happens on the way in, before anything
// measures or wraps.
func ExpandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}

	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", TabWidth))
}

// Fit forces a single line to exactly width cells, truncating what overflows
// and padding what falls short. It is ANSI-aware, so styled content keeps its
// escapes intact.
func Fit(line string, width int) string {
	if width <= 0 {
		return ""
	}

	line = ExpandTabs(line)

	w := ansi.StringWidth(line)
	if w > width {
		return ansi.Truncate(line, width, "")
	}

	return line + strings.Repeat(" ", width-w)
}

// Clip truncates a line to width without padding it out, keeping ANSI escapes
// intact. Use it where a shorter string is acceptable but a padded one is not.
func Clip(line string, width int) string {
	if width <= 0 {
		return ""
	}

	line = ExpandTabs(line)
	if ansi.StringWidth(line) <= width {
		return line
	}

	return ansi.Truncate(line, width, "")
}

// ClipLeft truncates a line from the left, keeping its tail and marking the cut
// with an ellipsis.
//
// Paths and repository names carry their meaning at the end: given too little
// room, "…/aiagent/internal/tui" tells you where you are and
// "/home/steven/code/…" does not. Clip from whichever end preserves the part
// that identifies the thing.
func ClipLeft(line string, width int) string {
	if width <= 0 {
		return ""
	}

	line = ExpandTabs(line)

	w := ansi.StringWidth(line)
	if w <= width {
		return line
	}

	if width <= 1 {
		return "…"
	}

	return "…" + ansi.TruncateLeft(line, w-width+1, "")
}

// FitBlock forces a multi-line block to exactly width by height cells, so a
// pane's interior always covers every cell it claims. Uncovered cells are what
// let the terminal's own background show through and break the illusion of a
// full-screen application.
func FitBlock(block string, width, height int, fill Style) string {
	lines := strings.Split(block, "\n")

	out := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}

		out = append(out, fill.Render(Fit(line, width)))
	}

	return strings.Join(out, "\n")
}

// Overlay splices a block on top of a background frame at (x, y), preserving
// the frame around it.
//
// Lip Gloss's Canvas and Layer types cannot do this: Canvas.Compose draws every
// layer at the full canvas bounds and Layer.Draw ignores its own X and Y, so a
// later layer erases the frame beneath it instead of sitting on it. Splicing
// row by row with ANSI-aware truncation is what actually keeps the background
// visible.
func Overlay(frame, block string, x, y int) string {
	frameRows := strings.Split(frame, "\n")
	blockRows := strings.Split(block, "\n")

	frameWidth := lipgloss.Width(frame)
	x = max(x, 0)

	// Clip rather than overflow. A block wider than the space left of x would
	// push each spliced row past the terminal width, and an over-wide row wraps
	// and shifts everything below it.
	blockWidth := min(lipgloss.Width(block), max(frameWidth-x, 0))
	if blockWidth == 0 {
		return frame
	}

	for i, blockRow := range blockRows {
		row := y + i
		if row < 0 || row >= len(frameRows) {
			continue
		}

		base := frameRows[row]

		left := ansi.Truncate(base, x, "")
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0))
		right := ansi.TruncateLeft(base, x+blockWidth, "")

		frameRows[row] = left + Fit(blockRow, blockWidth) + right
	}

	return strings.Join(frameRows, "\n")
}
