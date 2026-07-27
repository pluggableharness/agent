package shell

import (
	"strings"
	"testing"
)

func TestBindingMatches(t *testing.T) {
	t.Parallel()

	b := Binding{Keys: []string{"pgdown", "pgdn"}}

	for _, k := range []string{"pgdown", "pgdn"} {
		if !b.Matches(k) {
			t.Errorf("Matches(%q) = false, want true", k)
		}
	}

	if b.Matches("pgup") {
		t.Error("Matches(pgup) = true, want false")
	}

	if (Binding{}).Matches("anything") {
		t.Error("an empty binding matched a key")
	}
}

func TestBindingHintUsesLabelThenFirstKey(t *testing.T) {
	t.Parallel()

	labeled := Binding{Keys: []string{"down"}, Label: "↓", Help: "scroll"}
	if got, want := labeled.hint(), "↓ scroll"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}

	unlabeled := Binding{Keys: []string{"tab"}, Help: "focus"}
	if got, want := unlabeled.hint(), "tab focus"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
}

// The default map must not bind one key to two things inside a single layer,
// which is what makes the layered precedence total and conflict-free.
func TestDefaultKeyMapHasNoIntraLayerConflicts(t *testing.T) {
	t.Parallel()

	k := DefaultKeyMap()

	layers := map[string][]Binding{
		"global":  {k.Interrupt, k.Quit, k.NextFocus, k.PrevFocus, k.CycleAgent, k.ToggleSidebar},
		"content": {k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom, k.Activate},
		"input":   {k.Submit, k.Newline},
		"overlay": {k.Allow, k.Deny, k.AllowSession, k.Edit, k.Dismiss},
	}

	for name, bindings := range layers {
		seen := map[string]bool{}

		for _, b := range bindings {
			for _, key := range b.Keys {
				if seen[key] {
					t.Errorf("layer %q binds %q twice", name, key)
				}

				seen[key] = true
			}
		}
	}
}

func TestHintsDescribeTheActiveLayer(t *testing.T) {
	t.Parallel()

	k := DefaultKeyMap()

	overlay := k.Hints(LayerOverlay, FocusInput, true, 200)
	for _, want := range []string{"allow", "deny", "edit args"} {
		if !strings.Contains(overlay, want) {
			t.Errorf("overlay hints missing %q: %q", want, overlay)
		}
	}

	// The overlay layer captures the keyboard, so region bindings must not be
	// advertised while it is up.
	if strings.Contains(overlay, "send") {
		t.Errorf("overlay hints leaked an input binding: %q", overlay)
	}

	input := k.Hints(LayerRegion, FocusInput, true, 200)
	if !strings.Contains(input, "send") {
		t.Errorf("input hints missing send: %q", input)
	}

	chat := k.Hints(LayerRegion, FocusMain, true, 200)
	if !strings.Contains(chat, "activate") {
		t.Errorf("chat hints missing activate: %q", chat)
	}

	if strings.Contains(chat, "send") {
		t.Errorf("chat hints advertised the input binding: %q", chat)
	}
}

// The sidebar toggle is only advertised where it can actually do something.
// Whole bindings drop from the end rather than the line being cut mid-word.
func TestHintsDropWholeBindingsToFit(t *testing.T) {
	t.Parallel()

	k := DefaultKeyMap()

	full := k.Hints(LayerRegion, FocusInput, true, 200)
	short := k.Hints(LayerRegion, FocusInput, true, 40)

	if len(short) >= len(full) {
		t.Fatalf("narrow hints were not shortened: %q", short)
	}

	if strings.HasSuffix(short, " ") || strings.Contains(short, "  ·  \u0000") {
		t.Errorf("hints end raggedly: %q", short)
	}

	// What survives must be complete bindings, never a fragment.
	for _, part := range strings.Split(short, "  ·  ") {
		if part == "" {
			t.Errorf("empty hint fragment in %q", short)
		}
	}

	// At least one binding always survives, however little room there is.
	if k.Hints(LayerRegion, FocusInput, true, 1) == "" {
		t.Error("all hints dropped; at least the first should survive")
	}
}

func TestHintsOmitSidebarToggleWhenUnavailable(t *testing.T) {
	t.Parallel()

	k := DefaultKeyMap()

	if got := k.Hints(LayerRegion, FocusInput, false, 200); strings.Contains(got, "sidebar") {
		t.Errorf("hints advertised the sidebar toggle on a too-narrow terminal: %q", got)
	}

	if got := k.Hints(LayerRegion, FocusInput, true, 200); !strings.Contains(got, "sidebar") {
		t.Errorf("hints omitted an available sidebar toggle: %q", got)
	}
}
