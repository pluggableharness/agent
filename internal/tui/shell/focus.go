package shell

// Focus identifies which region owns the keyboard.
//
// Focus targets are regions, not individual nodes: within a focused region an
// action cursor selects among that region's reachable elements. The protocol
// has no concept of focus at all, so this is entirely the shell's model.
type Focus int

const (
	// FocusInput is the composer. It holds focus at startup, because typing is
	// the overwhelmingly common intent and a shell that demands a keystroke
	// before accepting text is hostile.
	FocusInput Focus = iota
	// FocusMain is the conversation transcript.
	FocusMain
	// FocusSidebar is the widget column.
	FocusSidebar
)

// String returns the region name, used in the hotkey hint line.
func (f Focus) String() string {
	switch f {
	case FocusInput:
		return "input"
	case FocusMain:
		return "chat"
	case FocusSidebar:
		return "sidebar"
	default:
		return "unknown"
	}
}

// focusRing returns the focus targets currently reachable by tab, in cycle
// order. A region that is off screen, or on screen with nothing to interact
// with, is omitted rather than being a dead stop in the cycle.
func focusRing(l Layout, sidebarHasContent bool) []Focus {
	ring := []Focus{FocusInput, FocusMain}
	if l.ShowSidebar && sidebarHasContent {
		ring = append(ring, FocusSidebar)
	}

	return ring
}

// cycleFocus advances focus around the ring. A current focus that is no longer
// in the ring — the sidebar closing while focused, say — resolves to the first
// entry rather than trapping the keyboard in a region that is gone.
func cycleFocus(cur Focus, ring []Focus, back bool) Focus {
	if len(ring) == 0 {
		return FocusInput
	}

	idx := -1

	for i, f := range ring {
		if f == cur {
			idx = i

			break
		}
	}

	if idx < 0 {
		return ring[0]
	}

	step := 1
	if back {
		step = -1
	}

	next := (idx + step + len(ring)) % len(ring)

	return ring[next]
}
