package paint_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/pluggableharness/agent/internal/tui/paint"
	"github.com/pluggableharness/agent/internal/tui/theme"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func newPainter() *paint.Painter { return paint.New(theme.Dark()) }

func wide() paint.Opts { return paint.Opts{Width: 60} }

// ansiPattern matches SGR escape sequences. Lip Gloss may emit them per
// character (underlined text does), so assertions strip them before matching.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func plain(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// contains reports whether the rendered output contains want, ignoring the
// styling escapes Lip Gloss may have wrapped it in.
func contains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(plain(got), want) {
		t.Fatalf("rendered output missing %q\ngot: %q", want, plain(got))
	}
}

// Every node type must produce visible output. A frontend MUST render every
// node type gracefully rather than dropping content it has no special
// treatment for.
func TestEveryNodeTypeRendersItsContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *renderv1.RenderNode
		want string
	}{
		{"text", render.Text("plain words"), "plain words"},
		{"styled text", render.TextStyled("loud", renderv1.TextStyle_TEXT_STYLE_BOLD), "loud"},
		{"code block", render.Code("go", "package main"), "package main"},
		{"code block language", render.Code("go", "x"), "go"},
		{"link text", render.Link("Anthropic", "https://example.test"), "Anthropic"},
		{"link url", render.Link("Anthropic", "https://example.test"), "example.test"},
		{"table header", render.Table([]string{"name"}, [][]string{{"row"}}), "name"},
		{"table cell", render.Table([]string{"name"}, [][]string{{"row"}}), "row"},
		{"list item", render.List(render.Text("only")), "only"},
		{"group child", render.Group(render.Text("inside")), "inside"},
		{"collapsible summary", render.Collapsible("summary", render.Text("child")), "summary"},
		{"sub-session summary", render.SubSession("session-1", "child work"), "child work"},
		{"sub-session id", render.SubSession("session-1", "child work"), "session-1"},
		{"action label", render.Action("a1", "Compact", "compact", nil, "builtin"), "Compact"},
	}

	p := newPainter()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			contains(t, p.Tree(render.Tree(tc.node), wide()), tc.want)
		})
	}
}

func TestDiffRendersEveryLineOpAndHunkHeader(t *testing.T) {
	t.Parallel()

	node := render.Diff(render.Hunk(1, 2, 1, 3,
		render.DiffContextLine("context line"),
		render.DiffAddLine("added line"),
		render.DiffRemoveLine("removed line"),
	))

	got := newPainter().Tree(render.Tree(node), wide())

	for _, want := range []string{"@@ -1,2 +1,3 @@", "context line", "added line", "removed line", "+", "-"} {
		contains(t, got, want)
	}
}

// A node whose variant this build does not recognize must still render rather
// than erroring or crashing the process.
func TestUnknownNodeVariantDoesNotPanicOrError(t *testing.T) {
	t.Parallel()

	got := newPainter().Tree(render.Tree(&renderv1.RenderNode{}), wide())

	if strings.Contains(got, "panic") {
		t.Fatalf("unexpected output for unknown variant: %q", got)
	}
}

func TestNilTreeAndNilNodeRenderEmpty(t *testing.T) {
	t.Parallel()

	p := newPainter()

	if got := p.Tree(nil, wide()); got != "" {
		t.Fatalf("nil tree rendered %q, want empty", got)
	}

	if got := p.Tree(&renderv1.RenderTree{}, wide()); got != "" {
		t.Fatalf("rootless tree rendered %q, want empty", got)
	}

	if got := p.Node(nil, wide(), "0"); got != "" {
		t.Fatalf("nil node rendered %q, want empty", got)
	}
}

func TestOrderedListNumbersItems(t *testing.T) {
	t.Parallel()

	node := render.OrderedList(render.Text("alpha"), render.Text("beta"))
	got := newPainter().Tree(render.Tree(node), wide())

	contains(t, got, "1. ")
	contains(t, got, "2. ")
	contains(t, got, "alpha")
	contains(t, got, "beta")
}

func TestUnorderedListUsesBullets(t *testing.T) {
	t.Parallel()

	got := newPainter().Tree(render.Tree(render.List(render.Text("alpha"))), wide())
	contains(t, got, "•")
}

// A group is a transparent container: no border, no indent, no label. Adding
// chrome would contradict what the node type means.
func TestGroupAddsNoChrome(t *testing.T) {
	t.Parallel()

	p := newPainter()

	grouped := p.Tree(render.Tree(render.Group(render.Text("a"), render.Text("b"))), wide())
	separate := p.Tree(render.Tree(render.Text("a")), wide()) + "\n" +
		p.Tree(render.Tree(render.Text("b")), wide())

	if grouped != separate {
		t.Fatalf("group added chrome\ngrouped:  %q\nseparate: %q", grouped, separate)
	}
}

func TestCollapsibleRespectsDefaultAndOverride(t *testing.T) {
	t.Parallel()

	collapsed := render.CollapsedByDefault("summary", render.Text("hidden child"))
	expanded := render.Collapsible("summary", render.Text("shown child"))
	p := newPainter()

	if got := p.Tree(render.Tree(collapsed), wide()); strings.Contains(plain(got), "hidden child") {
		t.Fatalf("collapsed_by_default node showed its children: %q", got)
	}

	if got := p.Tree(render.Tree(expanded), wide()); !strings.Contains(plain(got), "shown child") {
		t.Fatalf("expanded node hid its children: %q", got)
	}

	// An explicit override wins over the node's own default, in both directions.
	o := wide()
	o.Expanded = map[string]bool{"0": true}

	contains(t, p.Tree(render.Tree(collapsed), o), "hidden child")

	o.Expanded = map[string]bool{"0": false}

	if got := p.Tree(render.Tree(expanded), o); strings.Contains(plain(got), "shown child") {
		t.Fatalf("override failed to collapse an expanded-by-default node: %q", got)
	}
}

func TestActionIsStyledDifferentlyWhenFocused(t *testing.T) {
	t.Parallel()

	node := render.Action("act_1", "Compact", "compact", nil, "builtin")
	p := newPainter()

	unfocused := p.Tree(render.Tree(node), wide())

	focused := wide()
	focused.FocusedAction = "act_1"
	got := p.Tree(render.Tree(node), focused)

	if got == unfocused {
		t.Fatal("focused action rendered identically to unfocused; the cursor would be invisible")
	}
}

func TestWidthIsClampedRatherThanDegenerating(t *testing.T) {
	t.Parallel()

	// A pathological width must not produce one character per line or panic.
	got := newPainter().Tree(render.Tree(render.Text("some words here")), paint.Opts{Width: -5})
	if got == "" {
		t.Fatal("clamped width dropped content")
	}
}

func TestThemeAccessor(t *testing.T) {
	t.Parallel()

	if got := paint.New(theme.Light()).Theme().Name; got != "light" {
		t.Fatalf("Theme() = %q, want light", got)
	}
}
