package shell

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/tui/region"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// EventSource feeds the shell. It is declared here, in the package that
// consumes it, rather than beside an implementation — the shell needs exactly
// one method and should not depend on a transport to say so.
//
// Run blocks until ctx is canceled or the source is exhausted, delivering
// messages through send. Implementations translate their own inputs into the
// message vocabulary below; no wire type reaches the model unconverted.
type EventSource interface {
	Run(ctx context.Context, send func(tea.Msg)) error
}

// PlaceMsg delivers content for a region. It is the translated form of a
// ServerEvent.render carrying PlacedContent.
type PlaceMsg struct {
	Content  *renderv1.PlacedContent
	Producer region.Producer
	Sequence uint64
}

// DeltaMsg is streamed model text. Consecutive deltas sharing a TargetID
// accumulate into one growing block rather than separate lines.
type DeltaMsg struct {
	TargetID string
	Text     string
}

// SettledMsg reports that a streamed block finished, so its live buffer can be
// dropped in favor of the finished render that replaces it.
type SettledMsg struct{ TargetID string }

// PermissionMsg asks the operator to decide one plan item. The protocol
// requires this be presented in the overlay region with a visual treatment
// distinct from ambient content, and the decision unit is always one item —
// never one answer for a whole plan.
type PermissionMsg struct {
	ItemID string
	Title  string
	// Preview is the plan item's preview tree when the provider supplied one.
	// When it is nil the shell falls back to rendering the raw input, which the
	// plan/apply gate spec requires rather than showing nothing.
	Preview  *renderv1.RenderTree
	RawInput string
}

// NoticeMsg is an out-of-band message for the operator: an error, a rejected
// client event, or a session status change. Notices are surfaced, never
// silently dropped, because several of the protocol's error categories are
// explicitly required to be visible and distinct.
type NoticeMsg struct {
	Text  string
	Level NoticeLevel
}

// NoticeLevel classifies a notice for styling.
type NoticeLevel int

const (
	// NoticeInfo is ordinary progress information.
	NoticeInfo NoticeLevel = iota
	// NoticeWarn is worth attention short of an error.
	NoticeWarn
	// NoticeError is a failure.
	NoticeError
)

// StatusMsg updates the top bar and the model line of the status bar.
type StatusMsg struct {
	Session string
	Model   string
	Status  string
	// Thinking and Effort describe the model's reasoning configuration as the
	// operator would say it ("extended", "high"). ModelSpec carries a
	// ThinkingSpec, but no frontend-facing event exposes it yet — the bridge
	// resolves it and passes it through here.
	//
	// They sit beside the composer rather than in the top bar because they
	// change with the selected agent: they are settings, not identity.
	Thinking string
	Effort   string
	// Elapsed is how long the session has been running.
	//
	// It arrives pre-computed rather than being derived from a start time,
	// because the model is pure: it never reads the clock. Whatever drives the
	// shell decides how often this ticks.
	Elapsed time.Duration
}

// DismissOverlayMsg clears overlay content the shell did not resolve itself —
// the case where another frontend won a decision race and the kernel rejected
// this shell's late response.
type DismissOverlayMsg struct{ Reason string }

// Action is an operator-originated event destined for the kernel's Attach
// stream. The shell emits these; the bridge translates them to ClientEvents.
type Action interface{ isAction() }

// SubmitPrompt is a user message. The protocol carries content blocks rather
// than a bare string; the bridge wraps this text in a text block.
type SubmitPrompt struct{ Text string }

// DecisionScope mirrors the protocol's PlanDecisionScope.
type DecisionScope int

const (
	// ScopeOnce applies to this item only. It is the default the shell sends
	// absent explicit operator intent, which the spec states as a SHOULD.
	ScopeOnce DecisionScope = iota
	// ScopeSession remembers the verdict for the rest of the session.
	ScopeSession
	// ScopeAlways persists the verdict as policy.
	ScopeAlways
)

// Decision resolves one plan item.
type Decision struct {
	ItemID string
	Allow  bool
	Scope  DecisionScope
}

// Trigger activates an ActionNode. The protocol requires the node's tool name,
// args, and provider be dispatched unchanged, so this carries them verbatim
// rather than reinterpreting them.
type Trigger struct {
	NodeID   string
	ToolName string
	Provider string
	Args     *structpb.Struct
}

// Interrupt cancels the running turn. Cancellation cascades to the whole
// sub-agent tree, so this is never scoped to a single child.
type Interrupt struct{}

func (SubmitPrompt) isAction() {}
func (Decision) isAction()     {}
func (Trigger) isAction()      {}
func (Interrupt) isAction()    {}

// AgentSelected reports that the operator switched agent profile.
//
// No ClientEvent carries this today: the frontend protocol's client-event set
// has no agent-profile variant, and a session's profile is fixed when the
// session is created. The shell therefore keeps the selection as local state
// and emits this so the bridge can decide what it means — most plausibly the
// profile for the next session, or a direct-invoke slash command. Closing that
// gap is a protocol question, not a shell one; see the design doc.
type AgentSelected struct {
	Name string
}

func (AgentSelected) isAction() {}

// UsageMsg carries the session's context pressure and running cost, translated
// from ServerEvent.usage_update.
//
// The denominator is the *effective ceiling*, not the model's raw context
// window: the ceiling is what remains after the kernel reserves room for
// expected output and tool schemas, and it is the figure the protocol names as
// the one a context-budget indicator should divide against. Showing pressure
// against the raw window would understate it — an operator would read 70% while
// the next turn is already at risk of not fitting.
type UsageMsg struct {
	UsedTokens        int64
	EffectiveCeiling  int64
	CumulativeCostUSD float64

	// Cumulative token split, from model.v1.Usage. Cache reads are never also
	// counted in InputTokens, which is what makes the cache rate below a real
	// ratio rather than a double count.
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// CacheRate is the share of input that came from cache, and whether there was
// enough input to say. It is the number that explains why a turn was cheap.
func (u UsageMsg) CacheRate() (float64, bool) {
	total := u.InputTokens + u.CacheReadTokens
	if total <= 0 {
		return 0, false
	}

	return float64(u.CacheReadTokens) / float64(total), true
}

// WorkspaceMsg describes where the session is working.
//
// None of this is in the protocol: there is no workspace or VCS concept
// anywhere in the wire contracts, and there should not be one invented just to
// feed a status bar. It arrives as a message so the shell stays pure — cmd/tui
// can supply the directory, and a git widget is the natural source for the rest.
// Only what the shell actually renders is carried here. Branch, subtree, and
// pull request are deliberately absent: they are version-control detail, a git
// widget already contributes them as ordinary sidebar content, and duplicating
// them in shell chrome would give the operator two sources for one truth.
type WorkspaceMsg struct {
	// Directory is where the session is rooted.
	Directory string
	// Repository is the project it belongs to, e.g. "org/name".
	Repository string
}

// EditStatsMsg counts what the session has read and changed.
//
// Also not in the protocol: no event aggregates per-tool line counts today. A
// tool provider knows them, so the natural path is a widget or a kernel-side
// rollup — either way it reaches the shell as this message.
type EditStatsMsg struct {
	LinesRead    int64
	LinesAdded   int64
	LinesRemoved int64
}
