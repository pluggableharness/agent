package frontend

import (
	"fmt"
	"strings"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// FallbackText renders any RenderNode as plain text, gracefully — the
// generic-treatment helper render-tree.md's node-type table and
// conformance.md's "RenderTree node types render gracefully, including
// unknown/unspecialized ones" MUST rule ask every frontend to have on
// hand, so an author's own Paint switch has a safe default case to call
// instead of erroring or dropping content. It handles every node type this
// package's own build knows about, and — critically — a variant added to
// the RenderNode oneof after this package shipped: node.GetNode() returns
// nil for any oneof value this generated code doesn't recognize, and the
// switch below's default case treats that identically to an explicitly
// empty node, returning "" rather than panicking on a type assertion. A
// nil node also returns "".
func FallbackText(node *renderv1.RenderNode) string {
	if node == nil {
		return ""
	}

	switch n := node.GetNode().(type) {
	case *renderv1.RenderNode_Text:
		return n.Text.GetContent()
	case *renderv1.RenderNode_CodeBlock:
		return n.CodeBlock.GetContent()
	case *renderv1.RenderNode_Diff:
		return fallbackDiffText(n.Diff)
	case *renderv1.RenderNode_Table:
		return fallbackTableText(n.Table)
	case *renderv1.RenderNode_Link:
		return fmt.Sprintf("%s (%s)", n.Link.GetText(), n.Link.GetUrl())
	case *renderv1.RenderNode_List:
		return fallbackChildrenText(n.List.GetItems())
	case *renderv1.RenderNode_Group:
		return fallbackChildrenText(n.Group.GetChildren())
	case *renderv1.RenderNode_Collapsible:
		summary := n.Collapsible.GetSummary()
		body := fallbackChildrenText(n.Collapsible.GetChildren())
		if body == "" {
			return summary
		}
		return summary + "\n" + body
	case *renderv1.RenderNode_SubSession:
		return n.SubSession.GetSummary()
	case *renderv1.RenderNode_Action:
		return n.Action.GetLabel()
	default:
		// A future RenderNode variant this build predates, or an entirely
		// empty node — render-tree.md's graceful-fallback MUST: never
		// error, never drop the caller's Paint loop, never panic on the
		// unrecognized shape.
		return ""
	}
}

// fallbackDiffText renders a DiffNode with no dedicated diff view as plain
// before/after text — render-tree.md's node-type table names this
// treatment explicitly for DiffNode.
func fallbackDiffText(diff *renderv1.DiffNode) string {
	var b strings.Builder
	for _, hunk := range diff.GetHunks() {
		for i, line := range hunk.GetLines() {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(diffLinePrefix(line.GetOp()))
			b.WriteString(line.GetText())
		}
	}
	return b.String()
}

func diffLinePrefix(op renderv1.DiffLineOp) string {
	switch op {
	case renderv1.DiffLineOp_DIFF_LINE_OP_ADD:
		return "+ "
	case renderv1.DiffLineOp_DIFF_LINE_OP_REMOVE:
		return "- "
	default:
		return "  "
	}
}

// fallbackTableText renders a TableNode as a plain, delimited grid.
func fallbackTableText(table *renderv1.TableNode) string {
	var b strings.Builder
	b.WriteString(strings.Join(table.GetHeaders(), " | "))
	for _, row := range table.GetRows() {
		b.WriteByte('\n')
		b.WriteString(strings.Join(row.GetCells(), " | "))
	}
	return b.String()
}

// fallbackChildrenText joins the fallback text of each child node with
// newlines, skipping any that render to nothing.
func fallbackChildrenText(children []*renderv1.RenderNode) string {
	var lines []string
	for _, child := range children {
		if text := FallbackText(child); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}
