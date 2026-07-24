package render_test

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/render"
)

func TestTree(t *testing.T) {
	t.Parallel()

	root := render.Text("hello")
	got := render.Tree(root)
	if got.GetRoot() != root {
		t.Errorf("Tree(root).GetRoot() = %v, want the same node pointer %v", got.GetRoot(), root)
	}
}

func TestTreeNilRoot(t *testing.T) {
	t.Parallel()

	got := render.Tree(nil)
	if got.GetRoot() != nil {
		t.Errorf("Tree(nil).GetRoot() = %v, want nil", got.GetRoot())
	}
}
