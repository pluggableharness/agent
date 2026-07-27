package shell

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/region"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func plain(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// recorder captures the actions a model emits so tests can assert on what the
// shell would have sent to the kernel.
type recorder struct{ actions []Action }

func (r *recorder) emit(a Action) { r.actions = append(r.actions, a) }

func (r *recorder) last(t *testing.T) Action {
	t.Helper()

	if len(r.actions) == 0 {
		t.Fatal("no action was emitted")
	}

	return r.actions[len(r.actions)-1]
}

// newTestModel returns a model sized to a wide terminal with an emitter wired.
func newTestModel(t *testing.T) (*Model, *recorder) {
	t.Helper()

	rec := &recorder{}
	m := New(WithEmitter(rec.emit))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	return m, rec
}

func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()

	for _, k := range keys {
		m.Update(key(k))
	}
}

// key builds a keypress whose String() matches what the shell binds against.
// Printable single characters carry Text; everything else is a named key.
func key(s string) tea.KeyPressMsg {
	if len([]rune(s)) == 1 && s != " " {
		r := []rune(s)[0]

		return tea.KeyPressMsg{Code: r, Text: s}
	}

	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	case "shift+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	case "ctrl+j":
		return tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+b":
		return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
}

func placeMsg(r renderv1.Region, text string, seq uint64) PlaceMsg {
	return PlaceMsg{
		Producer: region.Producer{Category: "tool", Name: "fs"},
		Sequence: seq,
		Content: &renderv1.PlacedContent{
			Region:  r,
			Content: render.Tree(render.Text(text)),
		},
	}
}

// The key helper must produce the strings the keymap actually binds, otherwise
// every keyboard test below would be vacuous.
func TestKeyHelperMatchesBindings(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"ctrl+c": "ctrl+c", "ctrl+d": "ctrl+d", "ctrl+b": "ctrl+b",
		"tab": "tab", "shift+tab": "shift+tab", "enter": "enter",
		"alt+enter": "alt+enter", "up": "up", "down": "down",
		"esc": "esc", "backspace": "backspace", "y": "y", "a": "a",
		"shift+enter": "shift+enter", "ctrl+j": "ctrl+j",
	}

	for in, want := range tests {
		if got := key(in).String(); got != want {
			t.Errorf("key(%q).String() = %q, want %q", in, got, want)
		}
	}
}

func TestTypingAndSubmitting(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	press(t, m, "h", "i")

	if got := m.input.Value(); got != "hi" {
		t.Fatalf("input = %q, want %q", got, "hi")
	}

	press(t, m, "enter")

	got, ok := rec.last(t).(SubmitPrompt)
	if !ok {
		t.Fatalf("emitted %T, want SubmitPrompt", rec.last(t))
	}

	if got.Text != "hi" {
		t.Fatalf("SubmitPrompt.Text = %q, want %q", got.Text, "hi")
	}
}

func TestAltEnterInsertsNewlineInsteadOfSubmitting(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	press(t, m, "a", "alt+enter", "b")

	if got := m.input.Value(); got != "a\nb" {
		t.Fatalf("input = %q, want %q", got, "a\nb")
	}

	if len(rec.actions) != 0 {
		t.Fatalf("alt+enter submitted: %+v", rec.actions)
	}

	// The composer growing must be reflected in the solved layout.
	if m.Layout().InputHeight != 2 {
		t.Fatalf("InputHeight = %d, want 2", m.Layout().InputHeight)
	}
}

func TestFocusCyclesAndSkipsEmptySidebar(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	if m.Focus() != FocusInput {
		t.Fatalf("startup focus = %v, want input", m.Focus())
	}

	press(t, m, "tab")

	if m.Focus() != FocusMain {
		t.Fatalf("after tab = %v, want main", m.Focus())
	}

	// The sidebar is on screen but empty, so it is not in the ring.
	press(t, m, "tab")

	if m.Focus() != FocusInput {
		t.Fatalf("after second tab = %v, want input (empty sidebar skipped)", m.Focus())
	}

	// Give the sidebar content and it joins the ring.
	m.Update(placeMsg(renderv1.Region_REGION_SIDEBAR, "git", 1))
	press(t, m, "tab", "tab")

	if m.Focus() != FocusSidebar {
		t.Fatalf("with sidebar content, focus = %v, want sidebar", m.Focus())
	}
}

func TestStreamingDeltasAccumulateAndSettle(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	m.Update(DeltaMsg{TargetID: "t1", Text: "hello "})
	m.Update(DeltaMsg{TargetID: "t1", Text: "world"})

	streams := m.Store().Streams()
	if len(streams) != 1 || streams[0].Text != "hello world" {
		t.Fatalf("deltas did not accumulate: %+v", streams)
	}

	if got := plain(m.View().Content); !strings.Contains(got, "hello world") {
		t.Fatalf("streamed text not painted: %q", got)
	}

	m.Update(SettledMsg{TargetID: "t1"})

	if len(m.Store().Streams()) != 0 {
		t.Fatal("SettledMsg left the live buffer in place; content would appear twice")
	}
}

// A finished render replaces the live buffer rather than appearing beside it.
func TestPlaceClearsLiveStreams(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(DeltaMsg{TargetID: "t1", Text: "partial"})
	m.Update(placeMsg(renderv1.Region_REGION_MAIN_CHAT, "final", 1))

	if len(m.Store().Streams()) != 0 {
		t.Fatal("a settled render left the streaming buffer live")
	}

	got := plain(m.View().Content)
	if !strings.Contains(got, "final") {
		t.Fatalf("final content missing: %q", got)
	}

	if strings.Contains(got, "partial") {
		t.Fatalf("streamed text painted twice: %q", got)
	}
}

func TestOverlayIsModalAndRestoresFocus(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "tab") // focus main

	m.Update(PermissionMsg{ItemID: "item_1", Title: "Allow write_file?"})

	if m.activeLayer() != LayerOverlay {
		t.Fatal("overlay did not take the keymap layer")
	}

	// While modal, region bindings must not reach the shell.
	press(t, m, "tab")

	if m.Focus() != FocusMain {
		t.Fatalf("tab changed focus while modal: %v", m.Focus())
	}

	if got := plain(m.View().Content); !strings.Contains(got, "Allow write_file?") {
		t.Fatalf("overlay not painted: %q", got)
	}

	press(t, m, "y")

	if m.activeLayer() != LayerRegion {
		t.Fatal("overlay stayed up after a decision")
	}

	if m.Focus() != FocusMain {
		t.Fatalf("focus not restored after overlay: %v", m.Focus())
	}
}

func TestPlanDecisionScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       string
		wantAllow bool
		wantScope DecisionScope
	}{
		{"allow once", "y", true, ScopeOnce},
		{"deny once", "n", false, ScopeOnce},
		{"allow for session", "a", true, ScopeSession},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, rec := newTestModel(t)
			m.Update(PermissionMsg{ItemID: "item_1", Title: "?"})
			press(t, m, tc.key)

			got, ok := rec.last(t).(Decision)
			if !ok {
				t.Fatalf("emitted %T, want Decision", rec.last(t))
			}

			if got.ItemID != "item_1" || got.Allow != tc.wantAllow || got.Scope != tc.wantScope {
				t.Fatalf("Decision = %+v, want allow=%v scope=%v", got, tc.wantAllow, tc.wantScope)
			}
		})
	}
}

// Dismissing must never resolve a pending decision on the operator's behalf.
func TestEscapeDoesNotResolveAPendingDecision(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	m.Update(PermissionMsg{ItemID: "item_1", Title: "?"})
	press(t, m, "esc")

	if len(rec.actions) != 0 {
		t.Fatalf("esc emitted %+v, want nothing", rec.actions)
	}

	if m.activeLayer() != LayerOverlay {
		t.Fatal("esc dismissed a pending decision overlay")
	}
}

// An operator deciding whether to allow a tool call needs to see the
// transcript that led to it, so the overlay composites over the frame rather
// than blanking it.
func TestOverlayPreservesTheFrameBeneathIt(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(placeMsg(renderv1.Region_REGION_MAIN_CHAT, "earlier transcript line", 1))
	m.Update(placeMsg(renderv1.Region_REGION_SIDEBAR, "sidebar widget", 2))
	m.Update(StatusMsg{Session: "session-01", Model: "claude-opus-5"})
	m.Update(PermissionMsg{ItemID: "i", Title: "Allow?"})

	got := plain(m.View().Content)

	for _, want := range []string{"Allow?", "earlier transcript line", "sidebar widget", "session-01"} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay frame missing %q:\n%s", want, got)
		}
	}
}

// The sidebar column must actually paint its content in the wide layout, not
// merely reserve space for it.
func TestSidebarContentPaintsInWideLayout(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(placeMsg(renderv1.Region_REGION_SIDEBAR, "branch: main", 1))

	if !m.Layout().ShowSidebar {
		t.Fatalf("sidebar not shown at width 120: %+v", m.Layout())
	}

	if got := plain(m.View().Content); !strings.Contains(got, "branch: main") {
		t.Fatalf("sidebar content missing from the frame:\n%s", got)
	}
}

// When a provider supplied no preview, the raw input is shown rather than an
// empty prompt.
func TestOverlayFallsBackToRawInput(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(PermissionMsg{ItemID: "i", Title: "Allow?", RawInput: `{"path":"/etc/hosts"}`})

	if got := plain(m.View().Content); !strings.Contains(got, "/etc/hosts") {
		t.Fatalf("raw input fallback not painted: %q", got)
	}
}

func TestActionTriggerDispatchesUnchanged(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	m.Update(PlaceMsg{
		Producer: region.Producer{Category: "widget", Name: "w"},
		Sequence: 1,
		Content: &renderv1.PlacedContent{
			Region:  renderv1.Region_REGION_MAIN_CHAT,
			Content: render.Tree(render.Action("act_1", "Compact", "compact_context", nil, "builtin")),
		},
	})

	press(t, m, "tab") // focus main
	press(t, m, "enter")

	got, ok := rec.last(t).(Trigger)
	if !ok {
		t.Fatalf("emitted %T, want Trigger", rec.last(t))
	}

	if got.NodeID != "act_1" || got.ToolName != "compact_context" || got.Provider != "builtin" {
		t.Fatalf("Trigger = %+v; tool name, provider and node id must pass through unchanged", got)
	}
}

func TestActivatingACollapsibleTogglesLocallyAndSendsNothing(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	m.Update(PlaceMsg{
		Producer: region.Producer{Category: "tool", Name: "fs"},
		Sequence: 1,
		Content: &renderv1.PlacedContent{
			Region:  renderv1.Region_REGION_MAIN_CHAT,
			Content: render.Tree(render.CollapsedByDefault("summary", render.Text("hidden body"))),
		},
	})

	press(t, m, "tab")

	if got := plain(m.View().Content); strings.Contains(got, "hidden body") {
		t.Fatalf("collapsed content was visible: %q", got)
	}

	press(t, m, "enter")

	if len(rec.actions) != 0 {
		t.Fatalf("toggling a collapsible sent %+v to the kernel", rec.actions)
	}

	if got := plain(m.View().Content); !strings.Contains(got, "hidden body") {
		t.Fatalf("collapsible did not expand: %q", got)
	}
}

func TestInterruptThenQuit(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	press(t, m, "ctrl+c")

	if _, ok := rec.last(t).(Interrupt); !ok {
		t.Fatalf("first ctrl+c emitted %T, want Interrupt", rec.last(t))
	}

	if m.quitting {
		t.Fatal("first ctrl+c quit immediately")
	}

	_, cmd := m.Update(key("ctrl+c"))

	if !m.quitting || cmd == nil {
		t.Fatal("second ctrl+c did not quit")
	}
}

// An interrupt followed by ordinary typing must not quit on the next ctrl+c.
func TestTypingDisarmsTheQuitSequence(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "ctrl+c", "x", "ctrl+c")

	if m.quitting {
		t.Fatal("quit sequence survived an intervening keystroke")
	}
}

func TestCtrlDQuitsOnlyOnAnEmptyComposer(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "x", "ctrl+d")

	if m.quitting {
		t.Fatal("ctrl+d quit with text in the composer")
	}

	press(t, m, "backspace", "ctrl+d")

	if !m.quitting {
		t.Fatal("ctrl+d did not quit on an empty composer")
	}
}

func TestSidebarToggle(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40}) // between floor and breakpoint

	if m.Layout().ShowSidebar {
		t.Fatal("sidebar shown by default on a medium terminal")
	}

	press(t, m, "ctrl+b")

	if !m.Layout().ShowSidebar {
		t.Fatal("ctrl+b did not open the sidebar")
	}

	press(t, m, "ctrl+b")

	if m.Layout().ShowSidebar {
		t.Fatal("ctrl+b did not close the sidebar")
	}
}

// Below the floor width the sidebar cannot exist as a pane, so its content
// folds into main_chat rather than being dropped.
func TestNarrowTerminalFoldsSidebarContentIntoMainChat(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(placeMsg(renderv1.Region_REGION_SIDEBAR, "git status", 1))
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 24})

	if !m.Layout().FoldSidebar() {
		t.Fatal("expected the layout to fold the sidebar at width 50")
	}

	if got := plain(m.View().Content); !strings.Contains(got, "git status") {
		t.Fatalf("folded sidebar content was dropped: %q", got)
	}
}

func TestNoticesAreSurfaced(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(NoticeMsg{Text: "already decided elsewhere", Level: NoticeError})

	if got := plain(m.View().Content); !strings.Contains(got, "already decided elsewhere") {
		t.Fatalf("notice not painted: %q", got)
	}
}

// A late decision the kernel rejected must clear the overlay and say why,
// rather than leaving the UI looking hung.
func TestDismissOverlayMsgClearsAndExplains(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(PermissionMsg{ItemID: "i", Title: "?"})
	m.Update(DismissOverlayMsg{Reason: "already decided elsewhere"})

	if m.activeLayer() != LayerRegion {
		t.Fatal("overlay survived a kernel-side dismissal")
	}

	if got := plain(m.View().Content); !strings.Contains(got, "already decided elsewhere") {
		t.Fatalf("dismissal reason not surfaced: %q", got)
	}
}

func TestStatusMsgPaintsInTopBar(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(StatusMsg{Session: "session-01", Model: "claude-opus-5", Status: "ready"})

	header := headerRows(t, m)

	// Session and run state are the header's right-hand group; a line with an
	// empty left group still renders them.
	for _, want := range []string{"session-01", "ready"} {
		if !strings.Contains(header, want) {
			t.Errorf("header missing %q:\n%s", want, header)
		}
	}
}

func TestViewIsAltScreenAndSurvivesQuitting(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	if !m.View().AltScreen {
		t.Error("view did not declare AltScreen; the shell is a full-screen takeover")
	}

	m.quitting = true

	if v := m.View(); v.Content != "" || !v.AltScreen {
		t.Errorf("quitting view = %+v, want empty content with AltScreen set", v)
	}
}

func TestModelWithoutEmitterDoesNotPanic(t *testing.T) {
	t.Parallel()

	m := New()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(t, m, "h", "enter", "ctrl+c")

	// Reaching here without a panic is the assertion: a rendering-only model
	// has nowhere to send, and that must be a supported configuration.
	if m.input.Value() != "" {
		t.Fatalf("submit did not clear the composer: %q", m.input.Value())
	}
}

func TestInitReturnsNoCommand(t *testing.T) {
	t.Parallel()

	if cmd := New().Init(); cmd != nil {
		t.Fatal("Init returned a command; the event source runs outside the program")
	}
}

func TestDemoSourceEmitsItsScriptAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	var got []tea.Msg

	done := make(chan error, 1)

	go func() {
		done <- DemoSource{}.Run(ctx, func(m tea.Msg) { got = append(got, m) })
	}()

	// The source blocks on ctx after emitting its script; canceling is the
	// documented way it ends, and it must report that as success rather than
	// as an error.
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run returned %v, want nil on cancellation", err)
	}

	if len(demoScript()) == 0 {
		t.Fatal("demo script is empty; the skeleton would show nothing")
	}
}

// The demo fixture must exercise every region the shell lays out, since that
// is the whole reason it exists ahead of the kernel bridge.
func TestDemoScriptCoversTheMainRegions(t *testing.T) {
	t.Parallel()

	seen := map[renderv1.Region]bool{}

	for _, msg := range demoScript() {
		if p, ok := msg.(PlaceMsg); ok {
			seen[p.Content.GetRegion()] = true
		}
	}

	for _, want := range []renderv1.Region{
		renderv1.Region_REGION_MAIN_CHAT,
		renderv1.Region_REGION_SIDEBAR,
	} {
		if !seen[want] {
			t.Errorf("demo script never places content in %v", want)
		}
	}
}

// Every frame must be exactly Height rows of exactly Width cells.
//
// This is the invariant that makes the shell a full-screen application rather
// than text printed into someone else's window: an uncovered cell shows the
// terminal's own background, and a row wider than the terminal wraps and
// shifts everything below it. It is also the cheapest way to catch a layout
// arithmetic slip, which is otherwise only visible by eye.
func TestFrameCoversTheTerminalExactly(t *testing.T) {
	t.Parallel()

	sizes := [][2]int{
		{120, 30}, {100, 24}, {80, 24}, {72, 20},
		{64, 18}, {52, 16}, {40, 12}, {30, 9}, {20, 6},
	}

	for _, size := range sizes {
		w, h := size[0], size[1]

		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()

			for _, withOverlay := range []bool{false, true} {
				m := New()
				m.Update(tea.WindowSizeMsg{Width: w, Height: h})

				for _, msg := range demoScript() {
					m.Update(msg)
				}

				if !withOverlay {
					m.Update(key("y")) // resolve the demo's permission prompt
				}

				rows := strings.Split(m.View().Content, "\n")
				if len(rows) != h {
					t.Fatalf("overlay=%v: got %d rows, want %d", withOverlay, len(rows), h)
				}

				for i, r := range rows {
					if got := lipgloss.Width(r); got != w {
						t.Errorf("overlay=%v row %d: width %d, want %d: %q",
							withOverlay, i, got, w, plain(r))
					}
				}
			}
		})
	}
}

// Content carrying tabs must not overflow its pane. A tab measures as zero
// cells but a terminal advances to the next tab stop when drawing one, so an
// unexpanded tab silently pushes a row past the terminal width.
func TestTabbedContentDoesNotOverflow(t *testing.T) {
	t.Parallel()

	m := New()
	m.Update(tea.WindowSizeMsg{Width: 72, Height: 20})
	m.Update(PlaceMsg{
		Producer: region.Producer{Category: "tool", Name: "fs"},
		Sequence: 1,
		Content: &renderv1.PlacedContent{
			Region:  renderv1.Region_REGION_MAIN_CHAT,
			Content: render.Tree(render.Code("go", "func main() {\n\tif x {\n\t\treturn\n\t}\n}")),
		},
	})

	frame := m.View().Content
	if strings.ContainsRune(frame, '\t') {
		t.Fatal("frame still contains a raw tab; width math cannot be trusted")
	}

	for i, r := range strings.Split(frame, "\n") {
		if got := lipgloss.Width(r); got != 72 {
			t.Errorf("row %d width %d, want 72: %q", i, got, plain(r))
		}
	}
}

// shift+enter is the binding operators reach for. A bare terminal cannot
// distinguish it from enter, so fallbacks exist — but all of them must insert a
// newline and grow the composer rather than submitting.
func TestNewlineBindingsInsertAndGrowTheComposer(t *testing.T) {
	t.Parallel()

	for _, k := range []string{"shift+enter", "alt+enter", "ctrl+j"} {
		t.Run(k, func(t *testing.T) {
			t.Parallel()

			m, rec := newTestModel(t)
			press(t, m, "a")
			m.Update(key(k))
			press(t, m, "b")

			if got := m.input.Value(); got != "a\nb" {
				t.Fatalf("%s: input = %q, want %q", k, got, "a\nb")
			}

			if len(rec.actions) != 0 {
				t.Fatalf("%s submitted instead of inserting a newline: %+v", k, rec.actions)
			}

			// The composer must actually grow, and the body must shrink to
			// make room for it.
			if got := m.Layout().InputHeight; got != 2 {
				t.Fatalf("%s: InputHeight = %d, want 2", k, got)
			}

			if m.Layout().ComposerHeight != 2+panelChrome {
				t.Fatalf("%s: ComposerHeight = %d, want %d", k, m.Layout().ComposerHeight, 2+panelChrome)
			}
		})
	}
}

// A plain enter still submits; the newline bindings must not have swallowed it.
func TestPlainEnterStillSubmits(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)
	press(t, m, "h", "i", "enter")

	if _, ok := rec.last(t).(SubmitPrompt); !ok {
		t.Fatalf("enter emitted %T, want SubmitPrompt", rec.last(t))
	}
}

// The composer grows only to its cap, then scrolls internally rather than
// eating the whole screen.
func TestComposerGrowthIsCapped(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	for range 20 {
		m.Update(key("shift+enter"))
	}

	if got := m.Layout().InputHeight; got != inputMaxHeight {
		t.Fatalf("InputHeight = %d, want the cap %d", got, inputMaxHeight)
	}
}

// The wheel must scroll this transcript. Without claiming it, the terminal
// scrolls its own scrollback behind the alt screen and the app only appears to
// have handled the gesture.
func TestWheelScrollsTheTranscript(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	for i := range 60 {
		m.Update(placeMsg(renderv1.Region_REGION_MAIN_CHAT, fmt.Sprintf("line %d", i), uint64(i)))
	}

	// Paint once so the model learns how far the content can scroll.
	_ = m.View()

	if !m.pinned {
		t.Fatal("expected to start pinned to the live tail")
	}

	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})

	if m.pinned {
		t.Fatal("scrolling up did not detach from the live tail")
	}

	if m.scroll != m.maxScroll-wheelStep {
		t.Fatalf("scroll = %d, want %d", m.scroll, m.maxScroll-wheelStep)
	}

	// Scrolling back to the bottom re-attaches, so new content follows again.
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if !m.pinned {
		t.Fatal("scrolling back to the bottom did not re-attach to the live tail")
	}
}

func TestWheelDoesNotScrollPastTheTop(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(placeMsg(renderv1.Region_REGION_MAIN_CHAT, "only line", 1))
	_ = m.View()

	for range 10 {
		m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	}

	if m.scroll < 0 {
		t.Fatalf("scroll went negative: %d", m.scroll)
	}
}

// The view must declare the takeover properties, since each of them is what
// stops some part of the terminal from behaving as if the app were not there.
func TestViewClaimsTheTerminal(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(StatusMsg{Session: "session-01"})

	v := m.View()

	if !v.AltScreen {
		t.Error("AltScreen not set; the app would print into the user's scrollback")
	}

	if v.MouseMode != tea.MouseModeCellMotion {
		t.Error("mouse not claimed; the wheel would scroll the terminal instead of the transcript")
	}

	if v.BackgroundColor == nil || v.ForegroundColor == nil {
		t.Error("view did not set terminal default colors")
	}

	if !strings.Contains(v.WindowTitle, "session-01") {
		t.Errorf("WindowTitle = %q, want it to name the session", v.WindowTitle)
	}
}

// Before any turn there is no usage report, and the gauge must be absent
// rather than reading zero — a confident 0% is a lie.
func TestContextGaugeAbsentUntilUsageArrives(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	if strings.Contains(plain(m.View().Content), "context") {
		t.Fatal("context shown before any usage was reported")
	}

	if _, ok := m.contextFill(); ok {
		t.Fatal("contextFill claimed to know pressure with no usage")
	}

	// A ceiling the kernel has not resolved is equally unknown.
	m.Update(UsageMsg{UsedTokens: 100, EffectiveCeiling: 0})

	if strings.Contains(plain(m.View().Content), "context") {
		t.Fatal("context shown against a zero ceiling")
	}
}

// Pressure is measured against the effective ceiling, which is what remains
// after the kernel reserves room for output and tool schemas.
func TestContextFillDividesByTheEffectiveCeiling(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(UsageMsg{UsedTokens: 50_000, EffectiveCeiling: 200_000})

	got, ok := m.contextFill()
	if !ok {
		t.Fatal("pressure unknown after a usage report")
	}

	if got != 0.25 {
		t.Fatalf("fill = %v, want 0.25", got)
	}

	if got := plain(m.View().Content); !strings.Contains(got, "25%") {
		t.Fatalf("context percentage not shown:\n%s", got)
	}
}

// The gauge hue is a continuous ramp, not a set of steps: it runs green
// through amber to red so the color itself reads as pressure.
func TestContextToneRunsGreenToRed(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	th := m.th

	// The ramp's endpoints and midpoint land exactly on their stops.
	for _, tc := range []struct {
		fill float64
		want color.Color
		name string
	}{
		{0, th.C.Success, "empty is green"},
		{0.5, th.C.Warning, "half is amber"},
		{1, th.C.Danger, "full is red"},
	} {
		if got := m.contextTone(tc.fill); got != tc.want {
			t.Errorf("%s: tone at %.2f = %v, want %v", tc.name, tc.fill, got, tc.want)
		}
	}

	// And it moves monotonically between them: more red, less green, as
	// pressure climbs.
	var prevR, prevG uint32

	for i := range 11 {
		r, g, _, _ := m.contextTone(float64(i) / 10).RGBA()

		if i > 0 {
			if r < prevR {
				t.Errorf("red channel fell between %d0%% and %d0%%", i-1, i)
			}

			if g > prevG {
				t.Errorf("green channel rose between %d0%% and %d0%%", i-1, i)
			}
		}

		prevR, prevG = r, g
	}
}

// The warning names no command: compaction is automatic in this system, driven
// by a compactor context provider, so there is no operator action to point at.
func TestContextWarningAppearsOnlyUnderPressure(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	if got := m.contextWarning(); got != "" {
		t.Fatalf("warning with no usage: %q", got)
	}

	m.Update(UsageMsg{UsedTokens: 100_000, EffectiveCeiling: 200_000})

	if got := m.contextWarning(); got != "" {
		t.Fatalf("warning at 50%%: %q", got)
	}

	m.Update(UsageMsg{UsedTokens: 190_000, EffectiveCeiling: 200_000})

	warning := m.contextWarning()
	if warning == "" {
		t.Fatal("no warning at 95% of the ceiling")
	}

	if strings.Contains(warning, "/") {
		t.Fatalf("warning names a command that does not exist: %q", warning)
	}

	// It must reach the status line, replacing the less urgent focus label.
	if got := plain(m.View().Content); !strings.Contains(got, warning) {
		t.Fatalf("warning never reached the frame:\n%s", got)
	}
}

func TestContextMeterReachesTheStatusBar(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(UsageMsg{UsedTokens: 51_204, EffectiveCeiling: 200_000})

	got := plain(m.View().Content)
	if !strings.Contains(got, "26%") {
		t.Fatalf("context percentage not painted:\n%s", got)
	}

	if !strings.Contains(got, "━") {
		t.Fatalf("gauge fill not painted:\n%s", got)
	}
}

// A transcript grows upward from the composer. Content shorter than the
// viewport is pushed to the bottom so the newest message sits next to where
// the operator is typing, with the empty space above it rather than between.
func TestTranscriptIsBottomAnchored(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(placeMsg(renderv1.Region_REGION_MAIN_CHAT, "newest line", 1))

	rows := strings.Split(plain(m.View().Content), "\n")

	// Find the transcript's content rows: inside the conversation panel.
	var lastContent int

	for i, r := range rows {
		if strings.Contains(r, "newest line") {
			lastContent = i
		}
	}

	if lastContent == 0 {
		t.Fatal("content not found in the frame")
	}

	// The composer starts within a few rows of the content, not a screen away.
	composerRow := 0

	for i, r := range rows {
		if strings.Contains(r, "ask anything") {
			composerRow = i
		}
	}

	if gap := composerRow - lastContent; gap > 4 {
		t.Fatalf("content sits %d rows above the composer; it should hug it", gap)
	}
}

// Static session data belongs in the sidebar, where there is room, rather than
// beside the composer.
func TestSessionPanelsRenderInTheSidebar(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(UsageMsg{UsedTokens: 10, EffectiveCeiling: 100, OutputTokens: 9_120})

	got := plain(m.View().Content)

	for _, want := range []string{"usage", "9.1k"} {
		if !strings.Contains(got, want) {
			t.Errorf("sidebar missing %q:\n%s", want, got)
		}
	}
}

// Where the session is working belongs in the top bar: it is stable for the
// whole session and is the first thing checked on returning to a window.
func TestWorkspaceAppearsInTheTopBar(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(WorkspaceMsg{Directory: "~/code/aiagent", Repository: "pluggableharness/agent"})

	header := headerRows(t, m)

	for _, want := range []string{"~/code/aiagent", "pluggableharness/agent"} {
		if !strings.Contains(header, want) {
			t.Errorf("header missing %q:\n%s", want, header)
		}
	}

	// The agent is a setting, not identity, and lives beside the composer.
	if strings.Contains(header, "Code") {
		t.Errorf("agent leaked into the header:\n%s", header)
	}
}

// statusRow returns the plain text of the volatile-state line, which sits
// between the composer and the footer box.
func statusRow(t *testing.T, m *Model) string {
	t.Helper()

	rows := strings.Split(plain(m.View().Content), "\n")

	return rows[len(rows)-chromePanelHeight-1]
}

// headerRows returns the plain text of the header box.
func headerRows(t *testing.T, m *Model) string {
	t.Helper()

	rows := strings.Split(plain(m.View().Content), "\n")

	return strings.Join(rows[:chromePanelHeight], "\n")
}

// footerRows returns the plain text of the footer box.
func footerRows(t *testing.T, m *Model) string {
	t.Helper()

	rows := strings.Split(plain(m.View().Content), "\n")

	return strings.Join(rows[len(rows)-chromePanelHeight:], "\n")
}

// A long path keeps its tail, which is the part that identifies it.
func TestLongDirectoryClipsFromTheLeft(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(WorkspaceMsg{Directory: "/home/steven/code/aiagent/internal/tui/shell/deeply/nested"})

	if header := headerRows(t, m); !strings.Contains(header, "nested") {
		t.Errorf("header lost the path tail:\n%s", header)
	}
}

// A panel with nothing to show is not rendered: an empty titled box is worse
// than no box.
func TestSessionPanelsAbsentWithoutData(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)

	if got := plain(m.View().Content); strings.Contains(got, "workspace") {
		t.Fatal("workspace panel rendered with no workspace data")
	}
}

// The status line carries only what changes during a turn.
func TestStatusLineCarriesOnlyVolatileState(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(WorkspaceMsg{Directory: "~/code/aiagent", Repository: "pluggableharness/agent"})
	m.Update(UsageMsg{UsedTokens: 52_000, EffectiveCeiling: 200_000, CumulativeCostUSD: 0.42, InputTokens: 100, CacheReadTokens: 900})
	m.Update(StatusMsg{Model: "claude-opus-5", Effort: "high", Elapsed: 22 * time.Minute})

	status := statusRow(t, m)

	for _, want := range []string{"context", "26%", "cache", "cost", "$0.42", "elapsed"} {
		if !strings.Contains(status, want) {
			t.Errorf("status line missing %q: %q", want, status)
		}
	}

	// Static fields must not have followed it down here.
	for _, unwanted := range []string{"dir", "repo", "model"} {
		if strings.Contains(status, unwanted) {
			t.Errorf("static field %q leaked onto the status line: %q", unwanted, status)
		}
	}

	// Model and reasoning sit on the composer, not the status line — but in the
	// opposite corner from the agent, so the two are not read as one label.
	rows := strings.Split(plain(m.View().Content), "\n")
	prompt := composerRowIndex(t, rows)
	top, bottom := rows[prompt-1], rows[prompt+1]

	for _, want := range []string{"claude-opus-5", "high"} {
		if !strings.Contains(bottom, want) {
			t.Errorf("composer bottom border missing %q: %q", want, bottom)
		}

		if strings.Contains(top, want) {
			t.Errorf("%q leaked into the composer title: %q", want, top)
		}
	}

	if !strings.Contains(top, "Code") {
		t.Errorf("composer title missing the agent: %q", top)
	}
}

// The context meter must never blink out while a terminal is being resized.
//
// It did: the right-hand group was all-or-nothing, so one column of width could
// make a field affordable and take the meter from drawable to
// below-the-minimum in a single step.
func TestContextMeterSurvivesEveryWidth(t *testing.T) {
	t.Parallel()

	// One model, resized in place: rebuilding it per width made this the
	// slowest test in the package for no added coverage.
	m := New()
	m.Update(UsageMsg{
		UsedTokens: 51_204, EffectiveCeiling: 200_000,
		CumulativeCostUSD: 0.42, InputTokens: 100, CacheReadTokens: 900,
	})
	m.Update(StatusMsg{Elapsed: 22 * time.Minute})

	// Rendering just the status line rather than the whole frame: the sidebar
	// and transcript cost far more to paint and have nothing to do with this.
	// The range stops at 150 deliberately: every group-shedding transition
	// happens below it, and each width costs a full gradient render.
	for w := 50; w <= 150; w++ {
		status := plain(m.statusLine(Solve(w, 24, 1, false)))

		if !strings.Contains(status, "context") {
			t.Fatalf("width %d: context segment dropped entirely: %q", w, status)
		}

		if bar := strings.Count(status, "━") + strings.Count(status, "─"); bar < minMeterBar {
			t.Fatalf("width %d: meter collapsed to %d cells: %q", w, bar, status)
		}
	}
}

// Header and footer are bordered boxes, in the same visual language as the
// panels between them. A background tint was tried first and did not read as
// chrome at these contrast levels.
func TestHeaderAndFooterAreBoxed(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(WorkspaceMsg{Directory: "~/code/aiagent"})

	header, footer := headerRows(t, m), footerRows(t, m)

	for _, tc := range []struct{ name, rows, title string }{
		{"header", header, "pluggableharness"},
		{"footer", footer, "keys"},
	} {
		if !strings.Contains(tc.rows, "╭") || !strings.Contains(tc.rows, "╰") {
			t.Errorf("%s is not boxed:\n%s", tc.name, tc.rows)
		}

		if !strings.Contains(tc.rows, tc.title) {
			t.Errorf("%s missing its title %q:\n%s", tc.name, tc.title, tc.rows)
		}
	}

	// The status line between them stays unboxed: it is what the boxes are
	// separating, not another piece of chrome.
	rows := strings.Split(plain(m.View().Content), "\n")
	status := rows[len(rows)-chromePanelHeight-1]

	if strings.Contains(status, "╭") || strings.Contains(status, "╰") {
		t.Errorf("status line was boxed: %q", status)
	}
}

// The cursor must land on the row the composer actually paints, at the column
// just past the text.
//
// It drifted once already: the header grew from a single line into a bordered
// box and the cursor kept counting it as one row, so it sat two rows above the
// text. Asserting against the painted frame rather than against the arithmetic
// is what makes that class of mistake fail loudly.
func TestCursorLandsOnTheComposerRow(t *testing.T) {
	t.Parallel()

	sizes := [][2]int{
		{120, 30}, {120, 24}, {120, 16}, {120, 14}, {120, 10}, {80, 20}, {60, 12},
	}

	for _, size := range sizes {
		w, h := size[0], size[1]

		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()

			m := New()
			m.Update(tea.WindowSizeMsg{Width: w, Height: h})
			press(t, m, "h", "i")

			x, y, ok := m.cursorScreenPos()
			if !ok {
				t.Fatal("cursor reported no position while the composer had focus")
			}

			rows := strings.Split(plain(m.View().Content), "\n")
			if y >= len(rows) {
				t.Fatalf("cursor row %d is outside the frame (%d rows)", y, len(rows))
			}

			if !strings.Contains(rows[y], "› hi") {
				t.Fatalf("cursor row %d is not the composer row: %q", y, rows[y])
			}

			// The column sits immediately after what has been typed.
			if got := []rune(rows[y]); x >= len(got) || string(got[x-2:x]) != "hi" {
				t.Fatalf("cursor column %d does not follow the text: %q", x, rows[y])
			}
		})
	}
}

// A multi-line composer puts the cursor on the continuation row.
func TestCursorFollowsTheComposerAcrossLines(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "a")
	m.Update(key("shift+enter"))
	press(t, m, "b")

	_, y, ok := m.cursorScreenPos()
	if !ok {
		t.Fatal("no cursor position")
	}

	rows := strings.Split(plain(m.View().Content), "\n")
	if !strings.Contains(rows[y], "b") || strings.Contains(rows[y], "›") {
		t.Fatalf("cursor row %d is not the second composer line: %q", y, rows[y])
	}
}

// The cursor is hidden whenever the composer does not own the keyboard.
func TestCursorHiddenWhenComposerIsNotFocused(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "tab") // focus the transcript

	if v := m.View(); v.Cursor != nil {
		t.Fatal("cursor shown while the transcript had focus")
	}

	m.Update(PermissionMsg{ItemID: "i", Title: "?"})

	if v := m.View(); v.Cursor != nil {
		t.Fatal("cursor shown while a modal was up")
	}
}

// composerRow finds the row carrying the prompt, rather than assuming an
// offset that shifts whenever a band's height changes.
func composerRow(t *testing.T, rows []string) string {
	t.Helper()

	return rows[composerRowIndex(t, rows)]
}

// composerRowIndex locates the composer by its prompt rather than by counting
// rows from an edge. Offsets from the bottom of the frame have broken twice —
// once when the header became a bordered box, once when the status line grew —
// and each time the test kept passing against the wrong row for a while first.
func composerRowIndex(t *testing.T, rows []string) int {
	t.Helper()

	for i, r := range rows {
		if strings.Contains(r, "›") {
			return i
		}
	}

	t.Fatal("no composer row in the frame")

	return 0
}

// runeCol reports the display column of the first occurrence of sub. Byte
// offsets are useless here: the box-drawing characters are multi-byte, so
// strings.Index would report a position several columns off.
func runeCol(t *testing.T, row, sub string) int {
	t.Helper()

	b := strings.Index(row, sub)
	if b < 0 {
		t.Fatalf("%q not found in %q", sub, row)
	}

	return len([]rune(row[:b]))
}

// Every band's text starts on the same column.
//
// The status line has no border of its own, so it needs a wider margin than the
// panels to land where their content does; the lines inside the header and
// footer need a narrower one, because their panel has already padded them.
// Getting either wrong leaves a band a column adrift, which reads as sloppy
// long before anyone works out why.
func TestChromeTextSharesOneLeftMargin(t *testing.T) {
	t.Parallel()

	for _, w := range []int{124, 100, 90, 72} {
		m := New()
		m.Update(tea.WindowSizeMsg{Width: w, Height: 26})
		m.Update(WorkspaceMsg{Directory: "~/code/aiagent"})
		m.Update(UsageMsg{UsedTokens: 51_204, EffectiveCeiling: 200_000})

		rows := strings.Split(plain(m.View().Content), "\n")

		cols := map[string]int{
			"header":   runeCol(t, rows[1], "~/code"),
			"composer": runeCol(t, composerRow(t, rows), "›"),
			"status":   runeCol(t, rows[len(rows)-chromePanelHeight-1], "context"),
			"footer":   runeCol(t, rows[len(rows)-chromePanelHeight+1], "enter"),
		}

		for name, got := range cols {
			if got != cols["composer"] {
				t.Errorf("width %d: %s starts at column %d, composer at %d", w, name, got, cols["composer"])
			}
		}
	}
}

// The status line's right edge lands on the panels' content edge, not on their
// border — it is inset to match what is inside them.
func TestStatusLineRightEdgeMatchesPanelContent(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(UsageMsg{
		UsedTokens: 51_204, EffectiveCeiling: 200_000,
		CumulativeCostUSD: 0.42, InputTokens: 100, CacheReadTokens: 900,
	})
	m.Update(StatusMsg{Elapsed: 22 * time.Minute})

	rows := strings.Split(plain(m.View().Content), "\n")
	status := strings.TrimRight(rows[len(rows)-chromePanelHeight-1], " ")
	composer := strings.TrimRight(composerRow(t, rows), " ")

	// The composer row ends with gutter + border; its content ends two columns
	// earlier, which is where the status line should end.
	statusEnd := len([]rune(status)) - 1
	composerContentEnd := len([]rune(composer)) - 1 - 2

	if statusEnd != composerContentEnd {
		t.Errorf("status ends at column %d, panel content ends at %d", statusEnd, composerContentEnd)
	}
}
