package shell

import "github.com/pluggableharness/agent/internal/tui/theme"

// Layout geometry constants. These are the breakpoints documented in
// docs/first-party/frontends/tui.md; changing one here means changing it there.
const (
	sidebarMinWidth   = 26
	sidebarMaxWidth   = 38
	sidebarBreakpoint = 100
	sidebarFloorWidth = 64

	// The header and footer are bordered boxes rather than single lines, which
	// costs two rows each in chrome. They are dropped outright on a short
	// terminal rather than degrading to an unboxed line: one visual language is
	// worth more than one extra row of transcript.
	chromePanelHeight = 3

	hintsMinHeight  = 16
	topBarMinHeight = 12

	// statusMinHeight is where the volatile-state line fits beneath the
	// composer. It is a single row: static session data lives in the sidebar,
	// which has room for it, so the space beside the composer is spent only on
	// what actually changes during a turn.
	statusMinHeight = 9

	inputMinHeight = 1
	inputMaxHeight = 6

	// panelChrome is the rows and columns a panel spends on its own border.
	panelChrome = 2
	// panelPadding is the horizontal padding inside a panel, per side.
	panelPadding = theme.Space1
)

// Layout is the solved geometry for one frame: which regions are on screen and
// the outer box each one occupies.
//
// Every dimension here is an *outer* size, borders included. Interior content
// sizes come from the Inner helpers, so a caller never open-codes the chrome
// arithmetic and panes can never disagree about how wide their content is.
//
// Regions are dropped in a fixed order as space runs out — sidebar, then hotkey
// hints, then top bar. main_chat and input_bar are never dropped: a shell that
// can show neither input nor output is not a shell.
type Layout struct {
	Width  int
	Height int

	ShowTopBar  bool
	ShowHints   bool
	ShowSidebar bool

	// SidebarAvailable reports whether the terminal is wide enough for the
	// sidebar to be toggled on at all. Below the floor width its content folds
	// into main_chat instead, per the protocol's allowance that a frontend may
	// reinterpret placement for its own layout.
	SidebarAvailable bool

	// Outer panel boxes.
	MainWidth    int
	SidebarWidth int
	BodyHeight   int

	// InputHeight is the composer's content lines; ComposerHeight is its outer
	// box, chrome included.
	InputHeight    int
	ComposerHeight int

	// ShowStatus is whether the volatile-state line fits beneath the composer.
	ShowStatus bool
}

// FoldSidebar reports whether sidebar content should be folded into main_chat.
// True only when the terminal is too narrow for the sidebar to exist as a pane
// at all — a merely-toggled-off sidebar keeps its content, reachable by
// toggling it back on.
func (l Layout) FoldSidebar() bool { return !l.SidebarAvailable }

// MainInnerWidth is the content width inside the main panel.
func (l Layout) MainInnerWidth() int { return max(l.MainWidth-panelChrome-2*panelPadding, 1) }

// MainInnerHeight is the content height inside the main panel.
func (l Layout) MainInnerHeight() int { return max(l.BodyHeight-panelChrome, 1) }

// SidebarInnerWidth is the content width inside a sidebar panel.
func (l Layout) SidebarInnerWidth() int { return max(l.SidebarWidth-panelChrome-2*panelPadding, 1) }

// StatusInset is the margin either side of the status line.
//
// It is two cells rather than the panels' one because the status line has no
// border of its own: a panel spends a gutter, a border, and a pad before its
// text starts, so an unboxed line needs the same total to sit on the same
// column. Without it the status line starts two columns left of everything
// above and below it and reads as slightly loose.
const StatusInset = theme.Space2

// StatusWidth is the width the status line renders into, inset either side so
// its text aligns with the content inside the panels around it.
func (l Layout) StatusWidth() int { return max(l.Width-2*StatusInset, 1) }

// ComposerTop is the row the composer's top border occupies.
//
// The frame stacks its bands in this order, and the cursor has to be placed
// against the same arithmetic. Deriving it here rather than recomputing it at
// the point of use is what stops the two from drifting apart — they already did
// once, when the header grew from a single line into a bordered box and the
// cursor kept counting it as one row.
func (l Layout) ComposerTop() int {
	top := l.BodyHeight
	if l.ShowTopBar {
		top += chromePanelHeight
	}

	return top
}

// ChromeInnerWidth is the content width inside a header or footer panel.
func (l Layout) ChromeInnerWidth() int {
	return max(l.Width-2*theme.Gutter-panelChrome-2*panelPadding, 1)
}

// ComposerInnerWidth is the content width inside the composer panel.
func (l Layout) ComposerInnerWidth() int {
	return max(l.Width-2*theme.Gutter-panelChrome-2*panelPadding, 1)
}

// Solve computes the layout for a terminal of the given size.
//
// inputLines is the number of lines the composer currently needs, and
// sidebarOpen is the operator's toggle state, which only matters between the
// floor and the breakpoint — above the breakpoint the sidebar is always shown,
// below the floor it is never available.
func Solve(width, height, inputLines int, sidebarOpen bool) Layout {
	l := Layout{Width: width, Height: height}

	l.InputHeight = clamp(inputLines, inputMinHeight, inputMaxHeight)
	l.ComposerHeight = l.InputHeight + panelChrome
	l.ShowHints = height >= hintsMinHeight
	l.ShowTopBar = height >= topBarMinHeight

	l.SidebarAvailable = width >= sidebarFloorWidth
	switch {
	case width >= sidebarBreakpoint:
		l.ShowSidebar = true
	case l.SidebarAvailable:
		l.ShowSidebar = sidebarOpen
	default:
		l.ShowSidebar = false
	}

	// Horizontal: a gutter at each screen edge, and a gap between the main
	// panel and the sidebar column when both are present.
	bodyWidth := max(width-2*theme.Gutter, 1)

	if l.ShowSidebar {
		l.SidebarWidth = min(clamp(width*3/10, sidebarMinWidth, sidebarMaxWidth), width*2/5)
		l.MainWidth = max(bodyWidth-l.SidebarWidth-theme.Space1, 1)
	} else {
		l.MainWidth = bodyWidth
	}

	l.ShowStatus = height >= statusMinHeight

	used := l.ComposerHeight
	if l.ShowStatus {
		used++
	}

	// The header and footer are boxes, not lines: each costs its border rows
	// as well as its content row.
	if l.ShowTopBar {
		used += chromePanelHeight
	}

	if l.ShowHints {
		used += chromePanelHeight
	}

	l.BodyHeight = max(height-used, 1)

	return l
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
