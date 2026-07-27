package paint_test

import (
	"testing"

	"github.com/pluggableharness/agent/internal/tui/paint"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func paths(in []paint.Target) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, t.Path)
	}

	return out
}

func samePaths(a, b []string) bool {
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

func TestTargetsFindsActionsInPaintOrder(t *testing.T) {
	t.Parallel()

	tree := render.Tree(render.Group(
		render.Text("not focusable"),
		render.Action("a1", "First", "tool_a", nil, "p"),
		render.List(
			render.Action("a2", "Second", "tool_b", nil, "p"),
			render.Text("also not focusable"),
			render.Action("a3", "Third", "tool_c", nil, "p"),
		),
	))

	got := paint.Targets(tree, nil)
	if len(got) != 3 {
		t.Fatalf("got %d targets, want 3: %+v", len(got), got)
	}

	for i, want := range []string{"a1", "a2", "a3"} {
		if got[i].Kind != paint.TargetAction {
			t.Fatalf("target %d kind = %v, want action", i, got[i].Kind)
		}

		if got[i].Action.GetId() != want {
			t.Errorf("target %d id = %q, want %q", i, got[i].Action.GetId(), want)
		}
	}

	// Paths must be unique and match the painter's scheme so a cursor index
	// and a rendered node agree on which element they name.
	if want := []string{"0.1", "0.2.0", "0.2.2"}; !samePaths(paths(got), want) {
		t.Fatalf("paths = %v, want %v", paths(got), want)
	}
}

// Content the operator cannot see must not be reachable by a cursor that
// appears to move over nothing.
func TestTargetsSkipsCollapsedChildren(t *testing.T) {
	t.Parallel()

	tree := render.Tree(render.CollapsedByDefault("hidden",
		render.Action("buried", "Buried", "tool", nil, "p"),
	))

	got := paint.Targets(tree, nil)
	if len(got) != 1 {
		t.Fatalf("got %d targets, want only the collapsible itself: %+v", len(got), got)
	}

	if got[0].Kind != paint.TargetCollapsible || got[0].Summary != "hidden" {
		t.Fatalf("expected the collapsible as the sole target, got %+v", got[0])
	}

	// Expanding it exposes the child.
	expanded := paint.Targets(tree, map[string]bool{"0": true})
	if len(expanded) != 2 {
		t.Fatalf("expanded: got %d targets, want 2: %+v", len(expanded), expanded)
	}

	if expanded[1].Action.GetId() != "buried" {
		t.Fatalf("expanded child = %+v, want the buried action", expanded[1])
	}
}

// An expanded-by-default collapsible can be forced closed, which must also
// withdraw its children from the target list.
func TestTargetsHonorsForcedCollapse(t *testing.T) {
	t.Parallel()

	tree := render.Tree(render.Collapsible("shown",
		render.Action("child", "Child", "tool", nil, "p"),
	))

	if got := paint.Targets(tree, nil); len(got) != 2 {
		t.Fatalf("expanded by default: got %d targets, want 2", len(got))
	}

	if got := paint.Targets(tree, map[string]bool{"0": false}); len(got) != 1 {
		t.Fatalf("forced collapsed: got %d targets, want 1", len(got))
	}
}

func TestTargetsAtUsesTheGivenRootPath(t *testing.T) {
	t.Parallel()

	tree := render.Tree(render.Group(render.Action("a", "A", "tool", nil, "p")))

	got := paint.TargetsAt(tree, nil, "7")
	if len(got) != 1 || got[0].Path != "7.0" {
		t.Fatalf("TargetsAt rooted wrong: %v", paths(got))
	}
}

func TestTargetsOnEmptyTrees(t *testing.T) {
	t.Parallel()

	if got := paint.Targets(nil, nil); got != nil {
		t.Fatalf("nil tree = %v, want nil", got)
	}

	if got := paint.Targets(&renderv1.RenderTree{}, nil); got != nil {
		t.Fatalf("rootless tree = %v, want nil", got)
	}

	if got := paint.Targets(render.Tree(render.Text("leaf")), nil); len(got) != 0 {
		t.Fatalf("leaf-only tree = %v, want no targets", got)
	}
}

func TestTargetsNestedCollapsibles(t *testing.T) {
	t.Parallel()

	tree := render.Tree(render.Collapsible("outer",
		render.Collapsible("inner",
			render.Action("deep", "Deep", "tool", nil, "p"),
		),
	))

	got := paint.Targets(tree, nil)
	if want := []string{"0", "0.0", "0.0.0"}; !samePaths(paths(got), want) {
		t.Fatalf("nested paths = %v, want %v", paths(got), want)
	}
}
