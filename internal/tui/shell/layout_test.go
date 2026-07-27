package shell

import (
	"testing"

	"github.com/pluggableharness/agent/internal/tui/theme"
)

func TestSolveWideTerminalShowsEveryRegion(t *testing.T) {
	t.Parallel()

	l := Solve(120, 40, 1, false)

	if !l.ShowTopBar || !l.ShowHints || !l.ShowSidebar {
		t.Fatalf("wide terminal dropped a region: %+v", l)
	}

	if l.SidebarWidth < sidebarMinWidth || l.SidebarWidth > sidebarMaxWidth {
		t.Errorf("sidebar width %d outside [%d,%d]", l.SidebarWidth, sidebarMinWidth, sidebarMaxWidth)
	}

	// Panels plus both gutters plus the gap between them fill the terminal
	// exactly; a mismatch would leave an uncovered column.
	if got := l.MainWidth + l.SidebarWidth + theme.Space1 + 2*theme.Gutter; got != l.Width {
		t.Errorf("widths do not sum to the terminal: %d != %d", got, l.Width)
	}

	// The header and footer boxes, the status line, and the composer's outer
	// box all take rows out of the body.
	chrome := 2*chromePanelHeight + 1 + l.ComposerHeight

	if want := 40 - chrome; l.BodyHeight != want {
		t.Errorf("BodyHeight = %d, want %d", l.BodyHeight, want)
	}
}

// Interior sizes must account for border and padding, so no caller open-codes
// the chrome arithmetic.
func TestInnerSizesSubtractChrome(t *testing.T) {
	t.Parallel()

	l := Solve(120, 40, 1, false)

	if got, want := l.MainInnerWidth(), l.MainWidth-panelChrome-2*panelPadding; got != want {
		t.Errorf("MainInnerWidth() = %d, want %d", got, want)
	}

	if got, want := l.MainInnerHeight(), l.BodyHeight-panelChrome; got != want {
		t.Errorf("MainInnerHeight() = %d, want %d", got, want)
	}

	if l.ComposerHeight != l.InputHeight+panelChrome {
		t.Errorf("ComposerHeight = %d, want InputHeight+%d", l.ComposerHeight, panelChrome)
	}
}

// Above the breakpoint the sidebar is always shown; between the floor and the
// breakpoint it follows the operator's toggle; below the floor it is gone.
func TestSidebarBreakpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		width         int
		open          bool
		wantShown     bool
		wantAvailable bool
	}{
		{"wide ignores toggle off", 120, false, true, true},
		{"wide ignores toggle on", 120, true, true, true},
		{"medium closed", 80, false, false, true},
		{"medium opened", 80, true, true, true},
		{"at breakpoint", sidebarBreakpoint, false, true, true},
		{"just below breakpoint", sidebarBreakpoint - 1, false, false, true},
		{"at floor", sidebarFloorWidth, true, true, true},
		{"below floor cannot open", sidebarFloorWidth - 1, true, false, false},
		{"very narrow", 30, true, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := Solve(tc.width, 40, 1, tc.open)

			if l.ShowSidebar != tc.wantShown {
				t.Errorf("ShowSidebar = %v, want %v", l.ShowSidebar, tc.wantShown)
			}

			if l.SidebarAvailable != tc.wantAvailable {
				t.Errorf("SidebarAvailable = %v, want %v", l.SidebarAvailable, tc.wantAvailable)
			}

			// Folding is exactly the case where the sidebar cannot exist as a
			// pane, not merely the case where it is toggled off.
			if got, want := l.FoldSidebar(), !tc.wantAvailable; got != want {
				t.Errorf("FoldSidebar() = %v, want %v", got, want)
			}
		})
	}
}

func TestSidebarNeverExceedsFortyPercent(t *testing.T) {
	t.Parallel()

	for w := sidebarBreakpoint; w <= 400; w += 7 {
		l := Solve(w, 40, 1, true)
		if l.SidebarWidth > w*2/5 {
			t.Fatalf("width %d: sidebar %d exceeds 40%% (%d)", w, l.SidebarWidth, w*2/5)
		}
	}
}

// Regions are dropped in a fixed order as height runs out: hints first, then
// the top bar. main_chat and input_bar are never dropped.
func TestHeightDegradationOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		height     int
		wantTopBar bool
		wantHints  bool
	}{
		{"roomy", 40, true, true},
		{"at hints minimum", hintsMinHeight, true, true},
		{"below hints minimum", hintsMinHeight - 1, true, false},
		{"at top bar minimum", topBarMinHeight, true, false},
		{"below top bar minimum", topBarMinHeight - 1, false, false},
		{"pathological", 1, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := Solve(120, tc.height, 1, false)

			if l.ShowTopBar != tc.wantTopBar {
				t.Errorf("ShowTopBar = %v, want %v", l.ShowTopBar, tc.wantTopBar)
			}

			if l.ShowHints != tc.wantHints {
				t.Errorf("ShowHints = %v, want %v", l.ShowHints, tc.wantHints)
			}

			// The two load-bearing regions always survive.
			if l.BodyHeight < 1 {
				t.Errorf("BodyHeight = %d, want at least 1", l.BodyHeight)
			}

			if l.InputHeight < inputMinHeight {
				t.Errorf("InputHeight = %d, want at least %d", l.InputHeight, inputMinHeight)
			}
		})
	}
}

// The status line is a single row of volatile state and goes when height runs
// short — static session data lives in the sidebar, so nothing is lost with it.
func TestStatusLineDegradesByHeight(t *testing.T) {
	t.Parallel()

	if !Solve(120, 40, 1, false).ShowStatus {
		t.Error("no status line on a tall terminal")
	}

	if !Solve(120, statusMinHeight, 1, false).ShowStatus {
		t.Error("status line missing at its threshold height")
	}

	if Solve(120, statusMinHeight-1, 1, false).ShowStatus {
		t.Error("status line survived below its threshold")
	}
}

// However many rows the chrome takes, the body never collapses.
func TestBodySurvivesEveryHeight(t *testing.T) {
	t.Parallel()

	for h := 1; h <= 60; h++ {
		l := Solve(120, h, 1, false)
		if l.BodyHeight < 1 {
			t.Fatalf("height %d gave BodyHeight %d", h, l.BodyHeight)
		}
	}
}

func TestInputHeightIsClamped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lines int
		want  int
	}{
		{0, inputMinHeight},
		{1, 1},
		{3, 3},
		{inputMaxHeight, inputMaxHeight},
		{inputMaxHeight + 10, inputMaxHeight},
	}

	for _, tc := range tests {
		if got := Solve(120, 40, tc.lines, false).InputHeight; got != tc.want {
			t.Errorf("Solve(lines=%d).InputHeight = %d, want %d", tc.lines, got, tc.want)
		}
	}
}

func TestMainWidthNeverCollapsesBelowOne(t *testing.T) {
	t.Parallel()

	for _, w := range []int{0, 1, 5, 30} {
		if got := Solve(w, 24, 1, true).MainWidth; got < 1 {
			t.Errorf("width %d gave MainWidth %d", w, got)
		}
	}
}
