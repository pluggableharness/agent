package shell

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pluggableharness/agent/internal/tui/region"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// DemoSource is a scripted EventSource that exercises every region and node
// type without a kernel.
//
// It exists because no kernel-side code launches a frontend plugin and drives
// its Attach stream yet: cmd/agent is non-interactive and the frontend-backed
// interactive driver is still pending. Until that lands this is what makes the
// shell runnable and reviewable. It is a fixture, not a shipping path, and it
// should be deleted the moment the real bridge exists.
type DemoSource struct {
	// Step paces the script. Zero means emit everything immediately, which is
	// what tests want.
	Step time.Duration
}

// Run implements EventSource.
func (d DemoSource) Run(ctx context.Context, send func(tea.Msg)) error {
	for _, msg := range demoScript() {
		select {
		case <-ctx.Done():
			// Cancellation is ordinary control flow for a stream, never an
			// error to report.
			return nil
		default:
		}

		send(msg)

		if d.Step > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(d.Step):
			}
		}
	}

	<-ctx.Done()

	return nil
}

// demoScript is the fixed sequence of messages the demo emits. It is a pure
// function so tests can assert against it without running the source.
func demoScript() []tea.Msg {
	kernel := region.Producer{Category: "kernel", Name: "demo"}
	// Two distinct widget producers, so the sidebar exercises coexistence and
	// priority ordering rather than one widget replacing the other.
	gitWidget := region.Producer{Category: "widget", Name: "git"}
	jobsWidget := region.Producer{Category: "widget", Name: "jobs"}

	return []tea.Msg{
		StatusMsg{
			Session:  "session-01DEMO",
			Model:    "claude-opus-5",
			Status:   "ready",
			Thinking: "extended",
			Effort:   "high",
			Elapsed:  22 * time.Minute,
		},

		WorkspaceMsg{
			Directory:  "~/code/aiagent",
			Repository: "pluggableharness/agent",
		},

		EditStatsMsg{LinesRead: 4820, LinesAdded: 612, LinesRemoved: 148},

		place(kernel, 1, renderv1.Region_REGION_MAIN_CHAT, false, nil, render.Tree(render.Group(
			render.TextStyled("Reference TUI shell", renderv1.TextStyle_TEXT_STYLE_BOLD),
			render.Text("Every region below is plugin-contributable. This content is a fixture."),
		))),

		place(kernel, 2, renderv1.Region_REGION_MAIN_CHAT, false, nil, render.Tree(render.Collapsible(
			"read_file(internal/tui/shell/model.go)",
			render.Code("go", "func (m *Model) View() tea.View {\n\t// ...\n}"),
		))),

		place(kernel, 3, renderv1.Region_REGION_MAIN_CHAT, false, nil, render.Tree(render.Diff(
			render.Hunk(12, 3, 12, 4,
				render.DiffContextLine("func Solve(width, height int) Layout {"),
				render.DiffRemoveLine("\treturn Layout{}"),
				render.DiffAddLine("\tl := Layout{Width: width}"),
				render.DiffAddLine("\treturn l"),
			),
		))),

		place(kernel, 4, renderv1.Region_REGION_MAIN_CHAT, false, nil, render.Tree(render.Group(
			render.TextStyled("Interactive content", renderv1.TextStyle_TEXT_STYLE_DIM),
			render.Action("act_compact", "Compact context", "compact_context", nil, "builtin"),
		))),

		place(kernel, 5, renderv1.Region_REGION_MAIN_CHAT, false, nil,
			render.Tree(render.SubSession("session-01CHILD", "search the codebase"))),

		// No repeated heading: the panel title already names the producer.
		// With workspace detail out of shell chrome, the git widget is the only
		// source of VCS state — which is the point: one truth, contributed by
		// the plugin that owns it.
		place(gitWidget, 6, renderv1.Region_REGION_SIDEBAR, true, new(int32(10)), render.Tree(render.Group(
			render.Text("feat/tui-shell"),
			render.TextStyled("3 modified", renderv1.TextStyle_TEXT_STYLE_WARNING),
			render.Text("pr #11"),
			render.Action("act_diff", "Review diff", "git_diff", nil, "git"),
		))),

		// A second widget of a different kind: the shell already reports context
		// and cost itself, so a fixture that repeated them would demonstrate
		// duplication rather than what widgets are for.
		place(jobsWidget, 7, renderv1.Region_REGION_SIDEBAR, true, new(int32(20)), render.Tree(render.Group(
			render.TextStyled("build ✓ 2.1s", renderv1.TextStyle_TEXT_STYLE_SUCCESS),
			render.TextStyled("tests running", renderv1.TextStyle_TEXT_STYLE_WARNING),
		))),

		UsageMsg{
			UsedTokens:        51_204,
			EffectiveCeiling:  200_000,
			CumulativeCostUSD: 0.42,
			InputTokens:       18_400,
			OutputTokens:      9_120,
			CacheReadTokens:   146_800,
			CacheWriteTokens:  22_050,
		},

		DeltaMsg{TargetID: "msg_1", Text: "Streaming text arrives token "},
		DeltaMsg{TargetID: "msg_1", Text: "by token on the fast path."},

		PermissionMsg{
			ItemID: "item_1",
			Title:  "Allow write_file(internal/tui/shell/model.go)?",
			Preview: render.Tree(render.Diff(
				render.Hunk(1, 1, 1, 2,
					render.DiffContextLine("package shell"),
					render.DiffAddLine("// added by the plan"),
				),
			)),
		},
	}
}

func place(p region.Producer, seq uint64, r renderv1.Region, replace bool, priority *int32, tree *renderv1.RenderTree) PlaceMsg {
	return PlaceMsg{
		Producer: p,
		Sequence: seq,
		Content: &renderv1.PlacedContent{
			Region:   r,
			Content:  tree,
			Replace:  replace,
			Priority: priority,
		},
	}
}
