package shell

import "testing"

func sameFocus(a, b []Focus) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestFocusString(t *testing.T) {
	t.Parallel()

	tests := map[Focus]string{
		FocusInput:   "input",
		FocusMain:    "chat",
		FocusSidebar: "sidebar",
		Focus(99):    "unknown",
	}

	for f, want := range tests {
		if got := f.String(); got != want {
			t.Errorf("Focus(%d).String() = %q, want %q", f, got, want)
		}
	}
}

// A region that is off screen, or on screen with nothing to interact with, is
// omitted rather than being a dead stop in the tab cycle.
func TestFocusRingOmitsUnreachableRegions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sidebar    bool
		hasContent bool
		want       []Focus
	}{
		{"sidebar hidden", false, true, []Focus{FocusInput, FocusMain}},
		{"sidebar shown but empty", true, false, []Focus{FocusInput, FocusMain}},
		{"sidebar shown with content", true, true, []Focus{FocusInput, FocusMain, FocusSidebar}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := focusRing(Layout{ShowSidebar: tc.sidebar}, tc.hasContent)
			if !sameFocus(got, tc.want) {
				t.Fatalf("focusRing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCycleFocusWrapsBothWays(t *testing.T) {
	t.Parallel()

	ring := []Focus{FocusInput, FocusMain, FocusSidebar}

	forward := []Focus{FocusMain, FocusSidebar, FocusInput}
	cur := FocusInput

	for i, want := range forward {
		cur = cycleFocus(cur, ring, false)
		if cur != want {
			t.Fatalf("forward step %d = %v, want %v", i, cur, want)
		}
	}

	backward := []Focus{FocusSidebar, FocusMain, FocusInput}
	cur = FocusInput

	for i, want := range backward {
		cur = cycleFocus(cur, ring, true)
		if cur != want {
			t.Fatalf("backward step %d = %v, want %v", i, cur, want)
		}
	}
}

// The sidebar closing while focused must not trap the keyboard in a region
// that is no longer there.
func TestCycleFocusRecoversFromAVanishedRegion(t *testing.T) {
	t.Parallel()

	ring := []Focus{FocusInput, FocusMain}

	if got := cycleFocus(FocusSidebar, ring, false); got != FocusInput {
		t.Fatalf("cycling from a vanished region = %v, want %v", got, FocusInput)
	}
}

func TestCycleFocusEmptyRing(t *testing.T) {
	t.Parallel()

	if got := cycleFocus(FocusMain, nil, false); got != FocusInput {
		t.Fatalf("empty ring = %v, want %v", got, FocusInput)
	}
}
