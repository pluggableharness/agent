package shell

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
)

func TestDefaultRosterIsDistinguishable(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	seenName := map[string]bool{}
	seenColor := map[string]bool{}

	for _, a := range DefaultAgents {
		if seenName[a.Name] {
			t.Errorf("duplicate agent name %q", a.Name)
		}

		seenName[a.Name] = true

		// Two agents sharing a color would make the badge meaningless.
		key := th.Tone(a.Tone)
		r, g, b, _ := key.RGBA()
		id := string(rune(r)) + string(rune(g)) + string(rune(b))

		if seenColor[id] {
			t.Errorf("agent %q shares a color with another agent", a.Name)
		}

		seenColor[id] = true
	}

	if len(DefaultAgents) != 3 {
		t.Fatalf("expected the Code/Plan/Chat demo roster, got %d entries", len(DefaultAgents))
	}
}

func TestAgentRingCyclesAndWraps(t *testing.T) {
	t.Parallel()

	r := newAgentRing(DefaultAgents)

	if got := r.Current().Name; got != "Code" {
		t.Fatalf("initial agent = %q, want Code", got)
	}

	want := []string{"Plan", "Chat", "Code"}
	for i, w := range want {
		if got := r.cycle(false).Name; got != w {
			t.Fatalf("forward step %d = %q, want %q", i, got, w)
		}
	}

	// Backward wraps too, even though nothing binds it today.
	if got := r.cycle(true).Name; got != "Chat" {
		t.Fatalf("backward step = %q, want Chat", got)
	}
}

// An empty roster keeps the defaults: the shell always needs something to
// display as active, and Current must never index an empty slice.
func TestEmptyRosterFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	if got := newAgentRing(nil).Current().Name; got != DefaultAgents[0].Name {
		t.Fatalf("nil roster gave %q", got)
	}

	if got := newAgentRing([]Agent{}).Current().Name; got != DefaultAgents[0].Name {
		t.Fatalf("empty roster gave %q", got)
	}
}

func TestShiftTabCyclesAgentAndAnnounces(t *testing.T) {
	t.Parallel()

	m, rec := newTestModel(t)

	if got := m.Agent().Name; got != "Code" {
		t.Fatalf("startup agent = %q, want Code", got)
	}

	press(t, m, "shift+tab")

	if got := m.Agent().Name; got != "Plan" {
		t.Fatalf("after shift+tab = %q, want Plan", got)
	}

	got, ok := rec.last(t).(AgentSelected)
	if !ok {
		t.Fatalf("emitted %T, want AgentSelected", rec.last(t))
	}

	if got.Name != "Plan" {
		t.Fatalf("AgentSelected.Name = %q, want Plan", got.Name)
	}
}

// shift+tab must no longer move focus: the agent switcher owns it.
func TestShiftTabDoesNotMoveFocus(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "shift+tab")

	if m.Focus() != FocusInput {
		t.Fatalf("shift+tab changed focus to %v", m.Focus())
	}
}

// Cycling is a global binding, so it works from any focused region rather than
// only from the composer.
func TestAgentCyclesFromAnyRegion(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "tab") // focus main_chat

	press(t, m, "shift+tab")

	if got := m.Agent().Name; got != "Plan" {
		t.Fatalf("agent = %q, want Plan", got)
	}

	if m.Focus() != FocusMain {
		t.Fatalf("cycling the agent disturbed focus: %v", m.Focus())
	}
}

func TestActiveAgentIsVisibleInTheFrame(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "shift+tab") // -> Plan
	press(t, m, "x")         // clear the transient notice

	got := plain(m.View().Content)
	if !strings.Contains(got, "Plan") {
		t.Fatalf("active agent not shown in the frame:\n%s", got)
	}

	// The composer titles itself with the agent once no notice is pending.
	if strings.Contains(got, "Code") {
		t.Errorf("previous agent still visible:\n%s", got)
	}
}

// Switching agents changes the composer accent, which is the ambient signal
// that the mode changed.
func TestAgentColorDrivesTheComposerAccent(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	press(t, m, "x") // clear any notice so the accent is the agent's

	code := m.agentColor()
	press(t, m, "shift+tab")
	press(t, m, "x")

	if m.agentColor() == code {
		t.Fatal("agent color did not change with the selection")
	}
}

func TestWithAgentsOverridesTheRoster(t *testing.T) {
	t.Parallel()

	custom := []Agent{
		{Name: "Review", Tone: theme.ToneDanger},
		{Name: "Ship", Tone: theme.ToneSuccess},
	}

	m := New(WithAgents(custom))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	if got := m.Agent().Name; got != "Review" {
		t.Fatalf("custom roster active agent = %q, want Review", got)
	}

	press(t, m, "shift+tab")

	if got := m.Agent().Name; got != "Ship" {
		t.Fatalf("after cycle = %q, want Ship", got)
	}
}

// A notice occupies the composer title, so it must not stick around and hide
// the active agent forever.
func TestNoticeClearsOnNextKeystroke(t *testing.T) {
	t.Parallel()

	m, _ := newTestModel(t)
	m.Update(NoticeMsg{Text: "something happened", Level: NoticeWarn})

	if !strings.Contains(plain(m.View().Content), "something happened") {
		t.Fatal("notice was not shown")
	}

	press(t, m, "x")

	if strings.Contains(plain(m.View().Content), "something happened") {
		t.Fatal("notice survived a keystroke and would hide the agent title")
	}
}
