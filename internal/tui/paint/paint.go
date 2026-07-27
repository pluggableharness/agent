package paint

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
	"github.com/pluggableharness/agent/internal/tui/ui"
	"github.com/pluggableharness/agent/pkg/frontend"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// minWidth is the narrowest column count the painter will lay out against.
// Below this, wrapping produces more line breaks than content, so the painter
// clamps rather than degenerating.
const minWidth = 8

// Opts carries the per-paint state the shell owns: how wide to lay out, which
// action is under the cursor, and which collapsible paths the operator has
// toggled open.
type Opts struct {
	// Width is the available column count.
	Width int
	// FocusedAction is the ActionNode ID currently under the action cursor.
	// Empty means no action is focused in this region.
	FocusedAction string
	// Expanded maps a node path to a forced expansion state, overriding a
	// CollapsibleNode's collapsed_by_default. Paths are assigned by Walk and
	// are stable for a given tree shape.
	Expanded map[string]bool
}

func (o Opts) width() int {
	if o.Width < minWidth {
		return minWidth
	}

	return o.Width
}

// Painter renders nodes using one theme.
type Painter struct {
	th theme.Theme
}

// New returns a Painter that renders with th.
func New(th theme.Theme) *Painter { return &Painter{th: th} }

// Theme returns the theme this painter renders with.
func (p *Painter) Theme() theme.Theme { return p.th }

// Tree renders a whole tree. A nil tree or a tree with no root paints as empty
// rather than as an error — an absent tree is a legitimate state, not a fault.
func (p *Painter) Tree(t *renderv1.RenderTree, o Opts) string {
	return p.TreeAt(t, o, "0")
}

// TreeAt renders a tree rooted at an explicit path. A region holding several
// producers' trees gives each one a distinct root path, so the paths that
// Opts.Expanded and Targets key against stay unique across the whole region.
func (p *Painter) TreeAt(t *renderv1.RenderTree, o Opts, rootPath string) string {
	if t == nil || t.GetRoot() == nil {
		return ""
	}

	return p.Node(t.GetRoot(), o, rootPath)
}

// Node renders a single node and its descendants. The path identifies this
// node's position in the tree and is what Opts.Expanded keys against.
func (p *Painter) Node(n *renderv1.RenderNode, o Opts, path string) string {
	if n == nil {
		return ""
	}

	switch node := n.GetNode().(type) {
	case *renderv1.RenderNode_Text:
		return p.text(node.Text, o)
	case *renderv1.RenderNode_CodeBlock:
		return p.codeBlock(node.CodeBlock, o)
	case *renderv1.RenderNode_Diff:
		return p.diff(node.Diff, o)
	case *renderv1.RenderNode_Table:
		return p.table(node.Table, o)
	case *renderv1.RenderNode_Link:
		return p.link(node.Link)
	case *renderv1.RenderNode_List:
		return p.list(node.List, o, path)
	case *renderv1.RenderNode_Group:
		return p.group(node.Group, o, path)
	case *renderv1.RenderNode_Collapsible:
		return p.collapsible(node.Collapsible, o, path)
	case *renderv1.RenderNode_SubSession:
		return p.subSession(node.SubSession, o)
	case *renderv1.RenderNode_Action:
		return p.action(node.Action, o)
	default:
		// A variant added to the enum after this build shipped. The protocol
		// requires graceful rendering rather than an error or a silent drop,
		// and pkg/frontend already implements exactly that traversal.
		return p.th.Dim.Width(o.width()).Render(frontend.FallbackText(n))
	}
}

func (p *Painter) text(n *renderv1.TextNode, o Opts) string {
	return p.th.TextStyle(n.Style).Width(o.width()).Render(ui.ExpandTabs(n.GetContent()))
}

func (p *Painter) codeBlock(n *renderv1.CodeBlockNode, o Opts) string {
	var b strings.Builder

	if lang := n.GetLanguage(); lang != "" {
		b.WriteString(p.th.Dim.Render(lang))
		b.WriteString("\n")
	}

	// The block is indented rather than boxed so that a code block nested in a
	// list or collapsible does not fight the parent's own indentation.
	body := p.th.CodeBlock.Width(o.width() - 2).Render(ui.ExpandTabs(n.GetContent()))
	b.WriteString(indent(body, "  "))

	return b.String()
}

func (p *Painter) diff(n *renderv1.DiffNode, o Opts) string {
	lines := make([]string, 0, len(n.GetHunks()))

	for _, h := range n.GetHunks() {
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			h.GetOldStart(), h.GetOldLines(), h.GetNewStart(), h.GetNewLines())
		lines = append(lines, p.th.DiffHeader.Render(header))

		for _, l := range h.GetLines() {
			lines = append(lines, p.diffLine(l))
		}
	}

	// Diff lines are truncated rather than wrapped: a wrapped diff line loses
	// the column alignment that makes the +/- gutter readable.
	return lipgloss.NewStyle().MaxWidth(o.width()).Render(strings.Join(lines, "\n"))
}

func (p *Painter) diffLine(l *renderv1.DiffLine) string {
	switch l.GetOp() {
	case renderv1.DiffLineOp_DIFF_LINE_OP_ADD:
		return p.th.DiffAdd.Render("+" + ui.ExpandTabs(l.GetText()))
	case renderv1.DiffLineOp_DIFF_LINE_OP_REMOVE:
		return p.th.DiffRemove.Render("-" + ui.ExpandTabs(l.GetText()))
	case renderv1.DiffLineOp_DIFF_LINE_OP_CONTEXT, renderv1.DiffLineOp_DIFF_LINE_OP_UNSPECIFIED:
		return p.th.Default.Render(" " + ui.ExpandTabs(l.GetText()))
	default:
		return p.th.Default.Render(" " + ui.ExpandTabs(l.GetText()))
	}
}

func (p *Painter) table(n *renderv1.TableNode, o Opts) string {
	headers := n.GetHeaders()
	rows := n.GetRows()

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(ui.ExpandTabs(h))
	}

	for _, r := range rows {
		for i, c := range r.GetCells() {
			if w := lipgloss.Width(ui.ExpandTabs(c)); i < len(widths) && w > widths[i] {
				widths[i] = w
			}
		}
	}

	out := make([]string, 0, len(rows)+1)
	if len(headers) > 0 {
		out = append(out, p.th.TableHeader.Render(joinCells(headers, widths)))
	}

	for _, r := range rows {
		out = append(out, p.th.Default.Render(joinCells(r.GetCells(), widths)))
	}

	// Columns are truncated, not wrapped, for the same alignment reason diffs
	// are: a wrapped cell breaks the grid the table exists to convey.
	return lipgloss.NewStyle().MaxWidth(o.width()).Render(strings.Join(out, "\n"))
}

func joinCells(cells []string, widths []int) string {
	parts := make([]string, 0, len(cells))

	for i, c := range cells {
		c = ui.ExpandTabs(c)
		w := lipgloss.Width(c)
		if i < len(widths) && widths[i] > w {
			c += strings.Repeat(" ", widths[i]-w)
		}

		parts = append(parts, c)
	}

	return strings.Join(parts, "  ")
}

func (p *Painter) link(n *renderv1.LinkNode) string {
	// OSC 8 hyperlinks are emitted unconditionally: terminals that do not
	// understand the sequence ignore it and show the label, so there is no
	// capability check to get wrong. The URL is appended dimmed so the target
	// stays visible in a terminal that swallowed the escape.
	label := p.th.Link.Render(ui.ExpandTabs(n.GetText()))
	if n.GetUrl() == "" {
		return label
	}

	return label + p.th.Dim.Render(" ("+n.GetUrl()+")")
}

func (p *Painter) list(n *renderv1.ListNode, o Opts, path string) string {
	items := n.GetItems()
	out := make([]string, 0, len(items))

	inner := o
	inner.Width = o.width() - 3

	for i, item := range items {
		marker := "• "
		if n.GetOrdered() {
			marker = strconv.Itoa(i+1) + ". "
		}

		body := p.Node(item, inner, path+"."+strconv.Itoa(i))
		out = append(out, hangingIndent(body, marker))
	}

	return strings.Join(out, "\n")
}

func (p *Painter) group(n *renderv1.GroupNode, o Opts, path string) string {
	children := n.GetChildren()
	out := make([]string, 0, len(children))

	// A group is a transparent container: no border, no indent, no label.
	// Adding chrome here would contradict what the node type means.
	for i, c := range children {
		out = append(out, p.Node(c, o, path+"."+strconv.Itoa(i)))
	}

	return strings.Join(out, "\n")
}

func (p *Painter) collapsible(n *renderv1.CollapsibleNode, o Opts, path string) string {
	expanded := !n.GetCollapsedByDefault()
	if forced, ok := o.Expanded[path]; ok {
		expanded = forced
	}

	marker := "▸ "
	if expanded {
		marker = "▾ "
	}

	head := p.th.RegionTitle.Render(marker + ui.ExpandTabs(n.GetSummary()))
	if !expanded {
		return head
	}

	children := n.GetChildren()
	out := make([]string, 0, len(children)+1)
	out = append(out, head)

	inner := o
	inner.Width = o.width() - 2

	for i, c := range children {
		out = append(out, indent(p.Node(c, inner, path+"."+strconv.Itoa(i)), "  "))
	}

	return strings.Join(out, "\n")
}

func (p *Painter) subSession(n *renderv1.SubSessionNode, o Opts) string {
	// Deliberately a pointer, never inlined: the protocol defines this node as
	// a reference to a nested transcript, not a place to expand one.
	label := n.GetSummary()
	if label == "" {
		label = "sub-session"
	}

	return p.th.SubSession.Width(o.width()).Render("⤷ " + label + " (" + n.GetSessionId() + ")")
}

func (p *Painter) action(n *renderv1.ActionNode, o Opts) string {
	style := p.th.Action
	if n.GetId() != "" && n.GetId() == o.FocusedAction {
		style = p.th.ActionFocused
	}

	return style.Render("[ " + ui.ExpandTabs(n.GetLabel()) + " ]")
}

// indent prefixes every line of s with pad.
func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}

	return strings.Join(lines, "\n")
}

// hangingIndent prefixes the first line with marker and subsequent lines with
// an equivalent run of spaces, so wrapped list items stay aligned under their
// own text rather than under the bullet.
func hangingIndent(s, marker string) string {
	lines := strings.Split(s, "\n")
	pad := strings.Repeat(" ", lipgloss.Width(marker))

	for i := range lines {
		if i == 0 {
			lines[i] = marker + lines[i]

			continue
		}

		lines[i] = pad + lines[i]
	}

	return strings.Join(lines, "\n")
}
