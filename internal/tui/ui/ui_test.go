package ui_test

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
	"github.com/pluggableharness/agent/internal/tui/ui"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func plain(s string) string { return ansiPattern.ReplaceAllString(s, "") }

func TestFitPadsAndTruncatesToExactWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"pads short", "ab", 5, "ab   "},
		{"exact", "abcde", 5, "abcde"},
		{"truncates long", "abcdefgh", 5, "abcde"},
		{"empty pads", "", 3, "   "},
		{"zero width", "abc", 0, ""},
		{"negative width", "abc", -2, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ui.Fit(tc.in, tc.width); got != tc.want {
				t.Fatalf("Fit(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

// Fit must measure display width, not byte length, or styled content would be
// truncated by the length of its escape sequences.
func TestFitIsANSIAware(t *testing.T) {
	t.Parallel()

	styled := lipgloss.NewStyle().Bold(true).Render("abc")

	got := ui.Fit(styled, 6)
	if w := lipgloss.Width(got); w != 6 {
		t.Fatalf("styled Fit width = %d, want 6", w)
	}

	if !strings.Contains(plain(got), "abc") {
		t.Fatalf("styled Fit lost its content: %q", plain(got))
	}
}

// A tab measures as zero cells but a terminal advances to the next tab stop
// when drawing one, so an unexpanded tab paints wider than it measures.
func TestExpandTabs(t *testing.T) {
	t.Parallel()

	if lipgloss.Width("\t") != 0 {
		t.Skip("lipgloss now measures tabs; the expansion rationale needs revisiting")
	}

	got := ui.ExpandTabs("a\tb")
	if want := "a" + strings.Repeat(" ", ui.TabWidth) + "b"; got != want {
		t.Fatalf("ExpandTabs = %q, want %q", got, want)
	}

	// A string with no tab is returned unchanged.
	if got := ui.ExpandTabs("plain"); got != "plain" {
		t.Fatalf("ExpandTabs(%q) = %q", "plain", got)
	}

	// Fit expands on the way through, so measured and painted widths agree.
	if w := lipgloss.Width(ui.Fit("a\tb", 10)); w != 10 {
		t.Fatalf("Fit of tabbed content = %d cells, want 10", w)
	}
}

func TestFitBlockCoversExactlyWidthByHeight(t *testing.T) {
	t.Parallel()

	got := ui.FitBlock("one\ntwo", 6, 4, ui.New())
	lines := strings.Split(got, "\n")

	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}

	for i, l := range lines {
		if w := lipgloss.Width(l); w != 6 {
			t.Errorf("line %d width = %d, want 6: %q", i, w, l)
		}
	}
}

func TestFitBlockTruncatesExcessLines(t *testing.T) {
	t.Parallel()

	got := ui.FitBlock("a\nb\nc\nd", 3, 2, ui.New())
	if lines := strings.Split(got, "\n"); len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
}

func TestOverlayPreservesTheFrameAroundTheBlock(t *testing.T) {
	t.Parallel()

	frame := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
	}, "\n")

	got := ui.Overlay(frame, "XX\nYY", 4, 1)
	lines := strings.Split(got, "\n")

	want := []string{"aaaaaaaaaa", "bbbbXXbbbb", "ccccYYcccc"}
	for i, w := range want {
		if plain(lines[i]) != w {
			t.Errorf("line %d = %q, want %q", i, plain(lines[i]), w)
		}
	}
}

// A block that would extend past the frame is clipped, never allowed to widen
// a row — an over-wide row wraps and shifts everything below it.
func TestOverlayClipsRatherThanOverflowing(t *testing.T) {
	t.Parallel()

	frame := "aaaaaaaaaa"

	got := ui.Overlay(frame, "XXXXXXXX", 6, 0)
	if w := lipgloss.Width(got); w != 10 {
		t.Fatalf("overlaid row width = %d, want 10: %q", w, plain(got))
	}

	// Entirely off the right edge: the frame is returned untouched.
	if got := ui.Overlay(frame, "XX", 20, 0); got != frame {
		t.Fatalf("off-frame overlay changed the frame: %q", plain(got))
	}
}

func TestOverlayIgnoresRowsOutsideTheFrame(t *testing.T) {
	t.Parallel()

	frame := "aaaa\nbbbb"

	if got := ui.Overlay(frame, "XX", 0, 5); got != frame {
		t.Fatalf("below-frame overlay changed the frame: %q", plain(got))
	}

	if got := ui.Overlay(frame, "XX", 0, -3); got != frame {
		t.Fatalf("above-frame overlay changed the frame: %q", plain(got))
	}
}

func TestPanelRendersExactOuterDimensions(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	for _, size := range [][2]int{{20, 5}, {40, 3}, {12, 8}, {3, 2}} {
		w, h := size[0], size[1]

		got := ui.Panel{Title: "title", Body: "body\nmore", Width: w, Height: h}.Render(th)
		lines := strings.Split(got, "\n")

		if len(lines) != h {
			t.Errorf("%dx%d: got %d lines, want %d", w, h, len(lines), h)

			continue
		}

		for i, l := range lines {
			if lw := lipgloss.Width(l); lw != w {
				t.Errorf("%dx%d line %d: width %d: %q", w, h, i, lw, plain(l))
			}
		}
	}
}

func TestPanelShowsItsTitleAndBody(t *testing.T) {
	t.Parallel()

	got := plain(ui.Panel{Title: "git", Body: "branch: main", Width: 30, Height: 4}.Render(theme.Dark()))

	for _, want := range []string{"git", "branch: main"} {
		if !strings.Contains(got, want) {
			t.Errorf("panel missing %q:\n%s", want, got)
		}
	}
}

// A focused panel must be visually distinct, and it must not change size doing
// it — a border weight change would reflow the layout on every focus move.
func TestPanelFocusChangesStyleNotGeometry(t *testing.T) {
	t.Parallel()

	th := theme.Dark()
	base := ui.Panel{Title: "t", Body: "b", Width: 20, Height: 4}
	focused := base
	focused.Focused = true

	unfocusedOut := base.Render(th)
	focusedOut := focused.Render(th)

	if unfocusedOut == focusedOut {
		t.Fatal("focused panel rendered identically to unfocused")
	}

	if plain(unfocusedOut) != plain(focusedOut) {
		t.Fatalf("focus changed panel geometry:\n%s\n---\n%s", plain(unfocusedOut), plain(focusedOut))
	}
}

// The caption sits in the bottom border against the right corner, opposite the
// title, and neither displaces the other.
func TestPanelCaptionSitsBottomRight(t *testing.T) {
	t.Parallel()

	got := plain(ui.Panel{
		Title:   "Code",
		Caption: "claude-opus-5",
		Body:    "prompt",
		Width:   40,
		Height:  3,
	}.Render(theme.Dark()))

	lines := strings.Split(got, "\n")

	if !strings.Contains(lines[0], "Code") {
		t.Errorf("title not in the top border: %q", lines[0])
	}

	last := lines[len(lines)-1]
	if !strings.Contains(last, "claude-opus-5") {
		t.Fatalf("caption not in the bottom border: %q", last)
	}

	// Right-aligned means exactly one border cell trails the label.
	if !strings.HasSuffix(last, "claude-opus-5 ─╯") {
		t.Errorf("caption not against the right corner: %q", last)
	}

	if strings.Contains(lines[0], "claude-opus-5") {
		t.Errorf("caption leaked into the top border: %q", lines[0])
	}
}

// A caption that cannot fit whole is dropped rather than clipped: an
// abbreviated model name names no model.
func TestPanelDropsCaptionRatherThanClipIt(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	for w := 8; w <= 40; w++ {
		got := plain(ui.Panel{Caption: "claude-opus-5", Body: "b", Width: w, Height: 3}.Render(th))

		last := strings.Split(got, "\n")[2]
		if strings.Contains(last, "claude-opus-5") {
			continue
		}

		// Whatever survives must be border, never a fragment of the caption.
		if strings.ContainsAny(last, "abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Errorf("width %d: clipped caption fragment: %q", w, last)
		}
	}
}

// Too small to frame, the panel still covers its cells rather than letting the
// terminal show through.
func TestPanelDegradesWhenTooSmallToFrame(t *testing.T) {
	t.Parallel()

	got := ui.Panel{Title: "t", Body: "body", Width: 2, Height: 1}.Render(theme.Dark())
	if w := lipgloss.Width(got); w != 2 {
		t.Fatalf("degenerate panel width = %d, want 2", w)
	}
}
func TestBadgeRendersItsText(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	got := ui.Badge(th, "ready", th.C.Success)
	if !strings.Contains(plain(got), "ready") {
		t.Fatalf("badge lost its text: %q", plain(got))
	}
}

// The utility builder is the whole point of the package: each method sets one
// property and they compose.
func TestStyleUtilitiesCompose(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	got := ui.New().
		Fg(th.C.Text).
		Bg(th.C.BackgroundPanel).
		Px(theme.Space2).
		W(20).
		Bold().
		Italic().
		Underline().
		Align(lipgloss.Center).
		Render("hi")

	if w := lipgloss.Width(got); w != 20 {
		t.Fatalf("composed width = %d, want 20", w)
	}

	if !strings.Contains(plain(got), "hi") {
		t.Fatalf("composed style lost its content: %q", plain(got))
	}
}

func TestStylePaddingHelpers(t *testing.T) {
	t.Parallel()

	if got := plain(ui.New().P(theme.Space1).Render("x")); !strings.Contains(got, " x ") {
		t.Errorf("P did not pad horizontally: %q", got)
	}

	if got := plain(ui.New().Py(theme.Space1).Render("x")); !strings.Contains(got, "\n") {
		t.Errorf("Py did not pad vertically: %q", got)
	}

	if got := ui.New().H(3).Render("x"); lipgloss.Height(got) != 3 {
		t.Errorf("H did not set height: %d", lipgloss.Height(got))
	}

	if got := ui.New().MaxW(3).Render("abcdef"); lipgloss.Width(got) > 3 {
		t.Errorf("MaxW did not clip: %q", got)
	}
}

func TestFromAndLipRoundTrip(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Bold(true)

	if got := ui.From(base).Render("x"); got != base.Render("x") {
		t.Fatalf("From changed rendering: %q vs %q", got, base.Render("x"))
	}

	if !ui.From(base).Lip().GetBold() {
		t.Fatal("Lip did not return the wrapped style")
	}
}

func TestWidthMatchesLipgloss(t *testing.T) {
	t.Parallel()

	if ui.Width("abc") != lipgloss.Width("abc") {
		t.Fatal("ui.Width disagrees with lipgloss.Width")
	}
}

func TestStatusLineFillsItsWidth(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	for _, w := range []int{20, 40, 80, 120} {
		got := ui.StatusLine{
			Width: w,
			Segments: []ui.Segment{
				{Label: "model", Value: "opus"},
				{Label: "cost", Value: "$0.42"},
			},
		}.Render(th)

		if lipgloss.Width(got) != w {
			t.Errorf("width %d: line rendered %d cells", w, lipgloss.Width(got))
		}
	}
}

// Segments with nothing to show are omitted: a status bar should not advertise
// fields it has no data for.
func TestStatusLineDropsEmptySegments(t *testing.T) {
	t.Parallel()

	got := plain(ui.StatusLine{
		Width: 60,
		Segments: []ui.Segment{
			{Label: "model", Value: "opus"},
			{Label: "branch", Value: ""},
			{Label: "cost", Value: "$1"},
		},
	}.Render(theme.Dark()))

	if strings.Contains(got, "branch") {
		t.Fatalf("empty segment was rendered: %q", got)
	}

	for _, want := range []string{"model", "opus", "cost", "$1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %q", want, got)
		}
	}
}

// A line that will not fit drops from the right, so the leftmost fields — the
// ones ranked most important — survive a narrow terminal.
func TestStatusLineDropsFromTheRight(t *testing.T) {
	t.Parallel()

	line := ui.StatusLine{
		Width: 26,
		Segments: []ui.Segment{
			{Label: "model", Value: "claude-opus-5"},
			{Label: "cost", Value: "$0.42"},
			{Label: "elapsed", Value: "22m00s"},
		},
	}

	got := plain(line.Render(theme.Dark()))

	if !strings.Contains(got, "claude-opus-5") {
		t.Errorf("leftmost segment was dropped: %q", got)
	}

	if strings.Contains(got, "22m00s") {
		t.Errorf("rightmost segment survived a too-narrow line: %q", got)
	}

	if lipgloss.Width(line.Render(theme.Dark())) != 26 {
		t.Error("truncated line does not fill its width")
	}
}

// The filling segment absorbs whatever is left, so the line reflows with the
// terminal rather than leaving a ragged gap.
func TestStatusLineFillSegmentAbsorbsSlack(t *testing.T) {
	t.Parallel()

	widths := map[int]int{}

	for _, w := range []int{60, 90, 120} {
		ui.StatusLine{
			Width: w,
			Segments: []ui.Segment{
				{Label: "model", Value: "opus"},
				{
					MinWidth: 10,
					Fill: func(width int) string {
						widths[w] = width

						return strings.Repeat("=", width)
					},
				},
			},
		}.Render(theme.Dark())
	}

	if widths[60] >= widths[90] || widths[90] >= widths[120] {
		t.Fatalf("fill segment did not grow with the line: %v", widths)
	}
}

func TestStatusLineHandlesDegenerateInput(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	if got := (ui.StatusLine{Width: 0}).Render(th); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}

	if got := (ui.StatusLine{Width: 10}).Render(th); lipgloss.Width(got) != 10 {
		t.Errorf("no segments: width %d, want 10", lipgloss.Width(got))
	}
}

func TestMeterFillsProportionally(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	prev := -1

	for i := range 11 {
		got := plain(ui.Meter(20, float64(i)/10, th.C.Success, th.C.Border))

		if lipgloss.Width(got) != 20 {
			t.Fatalf("meter width = %d, want 20", lipgloss.Width(got))
		}

		filled := strings.Count(got, "━")
		if filled < prev {
			t.Errorf("fill %d0%%: %d cells, fewer than previous %d", i, filled, prev)
		}

		prev = filled
	}

	if strings.Count(plain(ui.Meter(20, 0, th.C.Success, th.C.Border)), "━") != 0 {
		t.Error("an empty meter drew filled cells")
	}

	if strings.Count(plain(ui.Meter(20, 1, th.C.Success, th.C.Border)), "━") != 20 {
		t.Error("a full meter did not fill every cell")
	}
}

func TestMeterClampsAndHandlesZeroWidth(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	if got := ui.Meter(0, 0.5, th.C.Success, th.C.Border); got != "" {
		t.Errorf("zero-width meter = %q", got)
	}

	for _, fill := range []float64{-5, 9} {
		if w := lipgloss.Width(ui.Meter(10, fill, th.C.Success, th.C.Border)); w != 10 {
			t.Errorf("fill %v: width %d, want 10", fill, w)
		}
	}
}

// A wide terminal must not leave the bar packed to one side.
func TestStatusLineSpansWithARightGroup(t *testing.T) {
	t.Parallel()

	line := ui.StatusLine{
		Width:    80,
		Segments: []ui.Segment{{Label: "model", Value: "opus"}},
		Right:    []ui.Segment{{Label: "cache", Value: "89%"}},
	}

	got := plain(line.Render(theme.Dark()))

	if lipgloss.Width(got) != 80 {
		t.Fatalf("line width = %d, want 80", lipgloss.Width(got))
	}

	if !strings.HasPrefix(got, " model") {
		t.Errorf("left group not at the left edge: %q", got)
	}

	if !strings.HasSuffix(got, "89% ") {
		t.Errorf("right group not at the right edge: %q", got)
	}
}

// The right group yields first when space runs out: the left is the ranked side.
func TestStatusLineDropsTheRightGroupWhenCrowded(t *testing.T) {
	t.Parallel()

	got := plain(ui.StatusLine{
		Width:    30,
		Segments: []ui.Segment{{Label: "model", Value: "claude-opus-5"}},
		Right:    []ui.Segment{{Label: "elapsed", Value: "22m00s"}},
	}.Render(theme.Dark()))

	if strings.Contains(got, "22m00s") {
		t.Errorf("right group survived a crowded line: %q", got)
	}

	if !strings.Contains(got, "claude-opus-5") {
		t.Errorf("left group was dropped instead: %q", got)
	}
}

// Empty right-hand segments are omitted like any other.
func TestStatusLineRightGroupDropsEmpties(t *testing.T) {
	t.Parallel()

	got := plain(ui.StatusLine{
		Width:    60,
		Segments: []ui.Segment{{Label: "repo", Value: "agent"}},
		Right:    []ui.Segment{{Label: "pr", Value: ""}},
	}.Render(theme.Dark()))

	if strings.Contains(got, "pr") {
		t.Errorf("empty right segment was rendered: %q", got)
	}
}

func TestFieldsAlignsValuesIntoAColumn(t *testing.T) {
	t.Parallel()

	got := plain(ui.Fields(theme.Dark(), []ui.Field{
		{Label: "pr", Value: "#11"},
		{Label: "branch", Value: "main"},
	}, 40))

	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	// Values start at the same column regardless of label length.
	if strings.Index(lines[0], "#11") != strings.Index(lines[1], "main") {
		t.Fatalf("values not aligned:\n%s", got)
	}
}

// Fields with no value are dropped, on the same principle as status segments.
func TestFieldsDropsEmptyValues(t *testing.T) {
	t.Parallel()

	got := plain(ui.Fields(theme.Dark(), []ui.Field{
		{Label: "repo", Value: "agent"},
		{Label: "pr", Value: ""},
	}, 40))

	if strings.Contains(got, "pr") {
		t.Errorf("empty field rendered: %q", got)
	}

	if ui.Fields(theme.Dark(), nil, 40) != "" {
		t.Error("empty field list produced output")
	}
}

// A wide value takes its own line rather than being squeezed beside a label.
func TestFieldsWideValuesStack(t *testing.T) {
	t.Parallel()

	got := plain(ui.Fields(theme.Dark(), []ui.Field{
		{Label: "dir", Value: "~/code/aiagent", Wide: true},
	}, 40))

	if len(strings.Split(got, "\n")) != 2 {
		t.Fatalf("wide field did not stack:\n%s", got)
	}
}

// A label column wider than half the panel is a wall, not a column, so
// everything stacks instead.
func TestFieldsStackWhenLabelsCrowdTheWidth(t *testing.T) {
	t.Parallel()

	got := plain(ui.Fields(theme.Dark(), []ui.Field{
		{Label: "averylonglabel", Value: "x"},
	}, 16))

	if len(strings.Split(got, "\n")) != 2 {
		t.Fatalf("expected stacking on a narrow panel:\n%s", got)
	}
}

func TestFieldsClipsRatherThanOverflowing(t *testing.T) {
	t.Parallel()

	got := plain(ui.Fields(theme.Dark(), []ui.Field{
		{Label: "repo", Value: strings.Repeat("x", 200)},
	}, 30))

	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > 30 {
			t.Errorf("line overflowed: %d cells", lipgloss.Width(line))
		}
	}
}

// The gradient runs across the bar's length rather than recoloring the whole
// bar as the value changes: the point is to show the scale, not just the state.
func TestGradientMeterVariesAlongItsLength(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	got := ui.GradientMeter(th, theme.DefaultGaugeRamp, 24, 1)
	if lipgloss.Width(got) != 24 {
		t.Fatalf("meter width = %d, want 24", lipgloss.Width(got))
	}

	// Every cell is filled, so any color difference is the gradient itself.
	colors := regexp.MustCompile(`38;2;(\d+);(\d+);(\d+)`).FindAllStringSubmatch(got, -1)
	if len(colors) < 24 {
		t.Fatalf("got %d colored cells, want 24", len(colors))
	}

	if colors[0][0] == colors[len(colors)-1][0] {
		t.Error("first and last cell share a color; the bar is not a gradient")
	}
}

func TestGradientMeterFillsProportionally(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	prev := -1

	for i := range 11 {
		got := plain(ui.GradientMeter(th, theme.DefaultGaugeRamp, 20, float64(i)/10))

		if lipgloss.Width(got) != 20 {
			t.Fatalf("width = %d, want 20", lipgloss.Width(got))
		}

		filled := strings.Count(got, "━")
		if filled < prev {
			t.Errorf("fill %d0%%: %d cells, fewer than previous %d", i, filled, prev)
		}

		prev = filled
	}
}

func TestGradientMeterHandlesDegenerateWidths(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	if got := ui.GradientMeter(th, theme.DefaultGaugeRamp, 0, 0.5); got != "" {
		t.Errorf("zero width = %q", got)
	}

	// A one-cell bar has no length to run a gradient along and must not divide
	// by zero.
	if w := lipgloss.Width(ui.GradientMeter(th, theme.DefaultGaugeRamp, 1, 0.5)); w != 1 {
		t.Errorf("one-cell meter width = %d", w)
	}
}

// A path keeps its tail, which is the part that identifies it.
func TestClipLeftKeepsTheTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"/home/steven/code/aiagent", 12, "…ode/aiagent"},
		{"short", 20, "short"},
		{"exactfit", 8, "exactfit"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
	}

	for _, tc := range tests {
		got := ui.ClipLeft(tc.in, tc.width)
		if got != tc.want {
			t.Errorf("ClipLeft(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}

		if tc.width > 0 && lipgloss.Width(got) > tc.width {
			t.Errorf("ClipLeft(%q, %d) overflowed: %d cells", tc.in, tc.width, lipgloss.Width(got))
		}
	}
}

// A line with an empty left group still renders its right group: the two are
// independent, and an empty left is a legitimate state.
func TestStatusLineRendersRightWithNoLeftSegments(t *testing.T) {
	t.Parallel()

	got := plain(ui.StatusLine{
		Width: 40,
		Right: []ui.Segment{{Label: "state", Value: "ready"}},
	}.Render(theme.Dark()))

	if !strings.Contains(got, "ready") {
		t.Fatalf("right group discarded with an empty left: %q", got)
	}

	if lipgloss.Width(got) != 40 {
		t.Errorf("width = %d, want 40", lipgloss.Width(got))
	}
}

// When a filling segment has taken every spare cell, the space between the
// groups is exactly one separator wide — so draw the separator rather than
// leaving a conspicuous hole.
func TestStatusLineDrawsTheSeparatorBesideAFillSegment(t *testing.T) {
	t.Parallel()

	got := plain(ui.StatusLine{
		Width: 60,
		Segments: []ui.Segment{{
			MinWidth: 10,
			Fill:     func(w int) string { return strings.Repeat("=", w) },
		}},
		Right: []ui.Segment{{Label: "cost", Value: "$1"}},
	}.Render(theme.Dark()))

	if !strings.Contains(got, "="+strings.TrimRight(ui.SegmentSeparator, " ")) {
		t.Fatalf("no separator between the fill and the right group: %q", got)
	}
}

// The right group sheds one field at a time. Dropping it wholesale makes a
// filling segment lurch by the entire group's width on a single column of
// resize.
func TestStatusLineShedsRightSegmentsOneAtATime(t *testing.T) {
	t.Parallel()

	right := []ui.Segment{
		{Label: "cache", Value: "89%"},
		{Label: "cost", Value: "$0.42"},
		{Label: "elapsed", Value: "22m00s"},
	}

	seen := map[int]bool{}

	for w := 40; w <= 120; w++ {
		got := plain(ui.StatusLine{
			Width:    w,
			Segments: []ui.Segment{{MinWidth: 12, Fill: func(n int) string { return strings.Repeat("=", n) }}},
			Right:    right,
		}.Render(theme.Dark()))

		kept := 0
		for _, s := range []string{"cache", "cost", "elapsed"} {
			if strings.Contains(got, s) {
				kept++
			}
		}

		seen[kept] = true
	}

	// Every intermediate count should occur somewhere in the range; an
	// all-or-nothing group would only ever show 0 or 3.
	for _, want := range []int{1, 2} {
		if !seen[want] {
			t.Errorf("right group never rendered exactly %d segments; it is dropping wholesale", want)
		}
	}
}
