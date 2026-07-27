package ui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
)

// SegmentSeparator divides adjacent status segments.
//
// Two cells either side rather than one: a status line packs many short
// label/value pairs, and with tight separators the eye cannot find the field
// boundaries. The extra breathing room is what makes it scannable.
const SegmentSeparator = "   │   "

// Segment is one field in a status line.
//
// A segment is a label and a value rather than bare text, because a status bar
// that only shows values is unreadable the first time and a bar that spells
// everything out is too wide. The label is dim and the value is not, so the eye
// lands on what changed.
type Segment struct {
	// Label names the field, e.g. "model". Optional.
	Label string
	// Value is the field's current reading. A segment with no value and no
	// Fill is dropped: a status bar should not advertise fields it has no data
	// for.
	Value string
	// Tone colors the value. Nil uses the theme's ordinary text.
	Tone color.Color
	// Fill, when set, renders the segment at whatever width is left over after
	// every fixed segment is placed. Exactly one segment per line should set
	// it; the first one wins.
	Fill func(width int) string
	// MinWidth is the least a filling segment may be squeezed to before the
	// line gives up on it.
	MinWidth int
}

// empty reports whether this segment has nothing to show.
func (s Segment) empty() bool { return s.Value == "" && s.Fill == nil }

// text renders a fixed segment.
func (s Segment) text(t theme.Theme) string {
	tone := t.C.Text
	if s.Tone != nil {
		tone = s.Tone
	}

	value := New().Fg(tone).Render(s.Value)
	if s.Label == "" {
		return value
	}

	return New().Fg(t.C.TextSubtle).Render(s.Label+" ") + value
}

// StatusLine is one row of segments spanning the full width.
//
// Segments are laid out left to right, separated by a divider, with one
// optional segment absorbing the slack so the line always spans its width and
// reflows as the terminal resizes. When the line cannot fit, segments are
// dropped from the right — the leftmost fields are the ones chosen to matter
// most, so they are the ones that survive.
type StatusLine struct {
	// Segments are laid out from the left edge.
	Segments []Segment
	// Right are pinned to the right edge, so the line spans its full width
	// instead of leaving a wide terminal packed to one side. They are dropped
	// before the left group when space runs out — the left is the ranked side.
	Right []Segment
	Width int
	// Flush drops the line's own one-cell inset, for a line rendered inside a
	// container that already pads it. Without this a status line inside a panel
	// sits one column right of everything else, since it adds its inset on top
	// of the panel's padding.
	Flush bool
}

// Render draws the line as exactly Width cells.
func (l StatusLine) Render(t theme.Theme) string {
	segments := make([]Segment, 0, len(l.Segments))

	for _, s := range l.Segments {
		if !s.empty() {
			segments = append(segments, s)
		}
	}

	if l.Width <= 0 {
		return ""
	}

	sep := New().Fg(t.C.Divider).Render(SegmentSeparator)
	sepWidth := lipgloss.Width(SegmentSeparator)

	// One cell of inset at each edge keeps text off the boundary, unless a
	// container has already provided it.
	pad := theme.Space1
	if l.Flush {
		pad = theme.Space0
	}

	edge := strings.Repeat(" ", pad)
	avail := l.Width - 2*pad

	// A line with nothing on the left still renders its right group: the two
	// groups are independent, and an empty left is a legitimate state rather
	// than a reason to discard the other half of the line.
	if len(segments) == 0 {
		right := renderGroup(t, presentSegments(l.Right), sep)
		gap := max(avail-lipgloss.Width(right), 0)

		return New().Render(edge + strings.Repeat(" ", gap) + right + edge)
	}

	fixed, flexIndex := renderFixed(t, segments)

	// The right group yields one segment at a time rather than all at once.
	//
	// Dropping it wholesale makes the line lurch on resize: a filling segment
	// suddenly gains the entire right group's width, which can take it from
	// too-small-to-draw to enormous across a single column of terminal width.
	// Shedding the rightmost field first keeps each step small.
	rightSegs := presentSegments(l.Right)
	rendered, right := fitBothGroups(t, fixed, flexIndex, segments, rightSegs, sep, avail, sepWidth)

	left := strings.Join(rendered, sep)
	gap := max(avail-lipgloss.Width(left)-lipgloss.Width(right), 0)

	// When a filling segment has taken every spare cell, the space between the
	// two groups is exactly one separator wide — because that is what was
	// reserved for it. Draw the separator there. Leaving it blank is what
	// produced a conspicuous hole between the last left field and the first
	// right one, with no divider to explain it.
	//
	// Without a filling segment the gap is genuine slack pushing the right
	// group to the edge, and a divider stranded in the middle of it would only
	// look lost.
	if flexIndex >= 0 && right != "" && gap == sepWidth {
		return New().Render(edge + left + sep + right + edge)
	}

	return New().Render(edge + left + strings.Repeat(" ", gap) + right + edge)
}

// presentSegments drops the segments with nothing to show.
func presentSegments(in []Segment) []Segment {
	out := make([]Segment, 0, len(in))

	for _, s := range in {
		if !s.empty() {
			out = append(out, s)
		}
	}

	return out
}

// fitBothGroups finds the largest right group the left group still fits beside,
// shedding the rightmost field first.
func fitBothGroups(t theme.Theme, fixed []string, flexIndex int, segments, rightSegs []Segment, sep string, avail, sepWidth int) ([]string, string) {
	for keep := len(rightSegs); keep > 0; keep-- {
		right := renderGroup(t, rightSegs[:keep], sep)

		rendered := fit(fixed, flexIndex, segments, avail-lipgloss.Width(right)-sepWidth, sepWidth)
		if len(rendered) == len(segments) {
			return rendered, right
		}
	}

	return fit(fixed, flexIndex, segments, avail, sepWidth), ""
}

// renderGroup renders a run of segments joined by the separator.
func renderGroup(t theme.Theme, segs []Segment, sep string) string {
	parts := make([]string, 0, len(segs))

	for _, s := range segs {
		parts = append(parts, s.text(t))
	}

	return strings.Join(parts, sep)
}

// renderFixed renders every non-filling segment and reports which segment, if
// any, absorbs the slack.
func renderFixed(t theme.Theme, segments []Segment) ([]string, int) {
	out := make([]string, len(segments))
	flexIndex := -1

	for i, s := range segments {
		if s.Fill != nil && flexIndex < 0 {
			flexIndex = i

			continue
		}

		out[i] = s.text(t)
	}

	return out, flexIndex
}

// fit drops segments from the right until the line fits, then hands whatever
// space is left to the filling segment.
func fit(rendered []string, flexIndex int, segments []Segment, avail, sepWidth int) []string {
	keep := len(rendered)

	width := func(n int) int {
		total := 0
		for i := range n {
			if i != flexIndex {
				total += lipgloss.Width(rendered[i])
			}
		}

		if n > 1 {
			total += (n - 1) * sepWidth
		}

		if flexIndex >= 0 && flexIndex < n {
			total += segments[flexIndex].MinWidth
		}

		return total
	}

	for keep > 0 && width(keep) > avail {
		keep--
	}

	out := rendered[:keep]

	if flexIndex >= 0 && flexIndex < keep {
		slack := avail - width(keep) + segments[flexIndex].MinWidth
		out[flexIndex] = segments[flexIndex].Fill(max(slack, 0))
	}

	return out
}

// Meter renders an inline fill bar of exactly width cells.
//
// It uses the same heavy-against-light stroke the rest of the shell uses for
// fill, so the reading survives a monochrome terminal and does not depend on
// telling one color from another. Color reinforces the measurement; it never
// carries it alone.
func Meter(width int, fill float64, on, off color.Color) string {
	if width <= 0 {
		return ""
	}

	fill = math.Min(math.Max(fill, 0), 1)
	filled := min(int(math.Round(fill*float64(width))), width)

	return New().Fg(on).Render(strings.Repeat("━", filled)) +
		New().Fg(off).Render(strings.Repeat("─", width-filled))
}

// GradientMeter renders a fill bar whose color runs across a ramp along its
// length, rather than recoloring the whole bar as the value changes.
//
// The difference matters: a bar that is uniformly amber tells you the current
// state, while a bar that runs green through amber to red shows you the whole
// scale and where on it you currently sit. The consumed run is drawn in full
// color and the remainder in a muted blend of the same gradient, so the
// boundary stays legible — and it is still a heavy stroke against a light one,
// which is what makes the reading survive a monochrome terminal.
func GradientMeter(t theme.Theme, ramp theme.Ramp, width int, fill float64) string {
	if width <= 0 {
		return ""
	}

	fill = math.Min(math.Max(fill, 0), 1)
	filled := min(int(math.Round(fill*float64(width))), width)

	var b strings.Builder

	for i := range width {
		// A one-cell bar has no length to run a gradient along; sample the
		// start rather than dividing by zero.
		pos := 0.0
		if width > 1 {
			pos = float64(i) / float64(width-1)
		}

		c := ramp.At(t, pos)

		if i < filled {
			b.WriteString(New().Fg(c).Render("━"))

			continue
		}

		b.WriteString(New().Fg(t.Muted(c)).Render("─"))
	}

	return b.String()
}
