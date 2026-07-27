package shell

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// Layer is a keymap precedence level. Bindings resolve highest layer first and
// a layer that handles a key stops propagation, so a binding in a lower layer
// can never steal a key the active context has claimed.
type Layer int

const (
	// LayerOverlay is active only while overlay content is showing. Overlay is
	// modal: it captures the keyboard entirely.
	LayerOverlay Layer = iota
	// LayerRegion is the focused region's own bindings.
	LayerRegion
	// LayerGlobal is always active and always lowest precedence.
	LayerGlobal
)

// Binding is one keyboard binding: the keys that trigger it and the label the
// hotkey hint line shows for it.
type Binding struct {
	Keys []string
	Help string
	// Label is the key name shown in hints, defaulting to the first key.
	Label string
}

// Matches reports whether a pressed key triggers this binding.
func (b Binding) Matches(key string) bool { return slices.Contains(b.Keys, key) }

// hint renders this binding as "key label" for the hotkey hint line.
func (b Binding) hint() string {
	label := b.Label
	if label == "" && len(b.Keys) > 0 {
		label = b.Keys[0]
	}

	return label + " " + b.Help
}

// KeyMap is the shell's complete binding set.
//
// There is no protocol-level keybinding registration, so a widget cannot claim
// a key. Widgets expose affordances as ActionNodes and reach the keyboard
// through the action cursor instead. That is a deliberate limitation: it keeps
// this map total and conflict-free, at the cost of widgets not binding
// accelerators of their own.
type KeyMap struct {
	// Global.
	Interrupt     Binding
	Quit          Binding
	NextFocus     Binding
	PrevFocus     Binding
	CycleAgent    Binding
	ToggleSidebar Binding

	// main_chat and sidebar.
	Up       Binding
	Down     Binding
	PageUp   Binding
	PageDown Binding
	Top      Binding
	Bottom   Binding
	Activate Binding

	// input_bar.
	Submit      Binding
	Newline     Binding
	HistoryPrev Binding
	HistoryNext Binding

	// overlay.
	Allow        Binding
	Deny         Binding
	AllowSession Binding
	Edit         Binding
	Dismiss      Binding
}

// DefaultKeyMap returns the shell's built-in bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Interrupt: Binding{Keys: []string{"ctrl+c"}, Help: "interrupt"},
		Quit:      Binding{Keys: []string{"ctrl+d"}, Help: "quit"},
		NextFocus: Binding{Keys: []string{"tab"}, Help: "focus"},
		// shift+tab belongs to the agent switcher, which is the convention
		// operators arrive with. The focus ring is at most three entries, so
		// cycling forward reaches everything and a backward binding buys
		// nothing worth the key. PrevFocus keeps its field so a future
		// configuration can bind it.
		PrevFocus:     Binding{},
		CycleAgent:    Binding{Keys: []string{"shift+tab"}, Help: "agent"},
		ToggleSidebar: Binding{Keys: []string{"ctrl+b"}, Help: "sidebar"},

		Up:       Binding{Keys: []string{"up", "k"}, Label: "↑", Help: "up"},
		Down:     Binding{Keys: []string{"down", "j"}, Label: "↓", Help: "down"},
		PageUp:   Binding{Keys: []string{"pgup"}, Help: "page up"},
		PageDown: Binding{Keys: []string{"pgdown", "pgdn"}, Label: "pgdn", Help: "page down"},
		Top:      Binding{Keys: []string{"home"}, Help: "top"},
		Bottom:   Binding{Keys: []string{"end"}, Help: "live"},
		Activate: Binding{Keys: []string{"enter"}, Help: "activate"},

		Submit: Binding{Keys: []string{"enter"}, Help: "send"},
		// shift+enter is the binding operators expect, but a bare terminal
		// cannot distinguish it from enter — both are CR. Bubble Tea requests
		// key disambiguation (Kitty keyboard / modifyOtherKeys) at startup,
		// which makes it available on terminals that support the negotiation.
		// alt+enter and ctrl+j are kept as fallbacks for those that do not:
		// ctrl+j is literally line feed and works everywhere.
		Newline: Binding{
			Keys:  []string{"shift+enter", "alt+enter", "ctrl+j"},
			Label: "shift+enter",
			Help:  "newline",
		},
		HistoryPrev: Binding{Keys: []string{"up"}, Label: "↑", Help: "history"},
		HistoryNext: Binding{Keys: []string{"down"}, Label: "↓", Help: "history"},

		Allow:        Binding{Keys: []string{"y"}, Help: "allow"},
		Deny:         Binding{Keys: []string{"n"}, Help: "deny"},
		AllowSession: Binding{Keys: []string{"a"}, Help: "allow session"},
		Edit:         Binding{Keys: []string{"e"}, Help: "edit args"},
		Dismiss:      Binding{Keys: []string{"esc"}, Help: "dismiss"},
	}
}

// Hints returns the bindings the hotkey hint line should advertise for the
// currently active layer, dropping whole bindings from the end until they fit
// in width. This is what makes hotkey_hints meaningful rather than a static
// legend: it always describes the keyboard as it is right now.
//
// Whole bindings go rather than the string being cut, because a hint truncated
// mid-word ("ctrl+c inter") is worse than an absent one — it looks like a
// rendering fault and tells the operator nothing.
func (k KeyMap) Hints(layer Layer, focus Focus, sidebarAvailable bool, width int) string {
	var bindings []Binding

	switch layer {
	case LayerOverlay:
		bindings = []Binding{k.Allow, k.Deny, k.AllowSession, k.Edit, k.Dismiss}
	case LayerRegion, LayerGlobal:
		bindings = k.regionHints(focus, sidebarAvailable)
	}

	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		parts = append(parts, b.hint())
	}

	for len(parts) > 1 && lipgloss.Width(strings.Join(parts, hintSeparator)) > width {
		parts = parts[:len(parts)-1]
	}

	return strings.Join(parts, hintSeparator)
}

// hintSeparator divides adjacent key hints.
const hintSeparator = "  ·  "

func (k KeyMap) regionHints(focus Focus, sidebarAvailable bool) []Binding {
	var bindings []Binding

	switch focus {
	case FocusInput:
		bindings = []Binding{k.Submit, k.Newline, k.CycleAgent, k.NextFocus}
	case FocusMain, FocusSidebar:
		bindings = []Binding{k.Up, k.Down, k.Activate, k.Bottom, k.CycleAgent, k.NextFocus}
	}

	if sidebarAvailable {
		bindings = append(bindings, k.ToggleSidebar)
	}

	return append(bindings, k.Interrupt)
}
