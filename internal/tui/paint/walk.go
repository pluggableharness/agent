package paint

import (
	"strconv"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// TargetKind distinguishes what activating a focus target does.
type TargetKind int

const (
	// TargetAction is an ActionNode. Activating it dispatches an
	// action_trigger ClientEvent carrying the node's tool_name, args, and
	// provider unchanged, which the protocol states as a MUST.
	TargetAction TargetKind = iota
	// TargetCollapsible is a CollapsibleNode. Activating it toggles expansion
	// locally and sends nothing to the kernel.
	TargetCollapsible
)

// Target is one keyboard-reachable element inside a rendered tree.
type Target struct {
	// Path is the node's position in the tree, matching the path scheme the
	// painter uses for Opts.Expanded.
	Path string
	Kind TargetKind
	// Action is set only when Kind is TargetAction.
	Action *renderv1.ActionNode
	// Summary is set only when Kind is TargetCollapsible.
	Summary string
}

// Targets returns every keyboard-reachable element in a tree, in the same
// order the painter emits them, so a cursor index means the same thing to both.
//
// Elements inside a collapsed CollapsibleNode are omitted: content the operator
// cannot see must not be reachable by a cursor that appears to move over
// nothing. The expanded map has the same meaning as Opts.Expanded.
func Targets(t *renderv1.RenderTree, expanded map[string]bool) []Target {
	return TargetsAt(t, expanded, "0")
}

// TargetsAt is Targets rooted at an explicit path, matching Painter.TreeAt so
// a cursor index and a rendered node agree on which element they name.
func TargetsAt(t *renderv1.RenderTree, expanded map[string]bool, rootPath string) []Target {
	if t == nil || t.GetRoot() == nil {
		return nil
	}

	var out []Target
	collect(t.GetRoot(), rootPath, expanded, &out)

	return out
}

func collect(n *renderv1.RenderNode, path string, expanded map[string]bool, out *[]Target) {
	if n == nil {
		return
	}

	switch node := n.GetNode().(type) {
	case *renderv1.RenderNode_Action:
		*out = append(*out, Target{Path: path, Kind: TargetAction, Action: node.Action})
	case *renderv1.RenderNode_List:
		collectChildren(node.List.GetItems(), path, expanded, out)
	case *renderv1.RenderNode_Group:
		collectChildren(node.Group.GetChildren(), path, expanded, out)
	case *renderv1.RenderNode_Collapsible:
		*out = append(*out, Target{
			Path:    path,
			Kind:    TargetCollapsible,
			Summary: node.Collapsible.GetSummary(),
		})

		if isExpanded(node.Collapsible, path, expanded) {
			collectChildren(node.Collapsible.GetChildren(), path, expanded, out)
		}
	default:
		// Every other variant is a leaf with nothing to focus.
	}
}

func collectChildren(children []*renderv1.RenderNode, path string, expanded map[string]bool, out *[]Target) {
	for i, c := range children {
		collect(c, path+"."+strconv.Itoa(i), expanded, out)
	}
}

func isExpanded(n *renderv1.CollapsibleNode, path string, expanded map[string]bool) bool {
	if forced, ok := expanded[path]; ok {
		return forced
	}

	return !n.GetCollapsedByDefault()
}
