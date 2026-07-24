package render

import (
	structpb "google.golang.org/protobuf/types/known/structpb"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Text builds a plain TextNode with no style hint — the frontend applies
// its own default styling
// (docs/specifications/frontend/render-tree.md#node-types: "style unset
// means 'frontend's own default'"). Use TextStyled to request a specific
// TextStyle, including TEXT_STYLE_NORMAL's "explicitly plain" state.
func Text(content string) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Text{
			Text: &renderv1.TextNode{Content: content},
		},
	}
}

// TextStyled builds a TextNode with an explicit presentation hint. Passing
// renderv1.TextStyle_TEXT_STYLE_NORMAL is a producer deliberately
// requesting plain styling — a distinct wire state from the unset style
// Text produces.
func TextStyled(content string, style renderv1.TextStyle) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Text{
			Text: &renderv1.TextNode{Content: content, Style: style.Enum()},
		},
	}
}

// Code builds a CodeBlockNode. An empty language means no syntax
// highlighting (docs/specifications/frontend/render-tree.md#node-types:
// "language unset means no syntax highlighting") — Code treats the empty
// string as that unset state rather than requiring callers to pass a nil
// pointer.
func Code(language, content string) *renderv1.RenderNode {
	block := &renderv1.CodeBlockNode{Content: content}
	if language != "" {
		block.Language = &language
	}
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_CodeBlock{CodeBlock: block},
	}
}

// Diff builds a DiffNode from a set of unified-diff-shaped hunks, in file
// order. Use Hunk to build each *renderv1.DiffHunk and DiffContextLine/
// DiffAddLine/DiffRemoveLine to build its lines.
func Diff(hunks ...*renderv1.DiffHunk) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Diff{
			Diff: &renderv1.DiffNode{Hunks: hunks},
		},
	}
}

// Hunk builds one DiffHunk, mirroring a standard unified-diff hunk header
// (`@@ -old_start,old_lines +new_start,new_lines @@`) plus its lines.
func Hunk(oldStart, oldLines, newStart, newLines int32, lines ...*renderv1.DiffLine) *renderv1.DiffHunk {
	return &renderv1.DiffHunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Lines:    lines,
	}
}

// DiffContextLine builds an unchanged-context DiffLine, present in both
// the old and new versions.
func DiffContextLine(text string) *renderv1.DiffLine {
	return &renderv1.DiffLine{Op: renderv1.DiffLineOp_DIFF_LINE_OP_CONTEXT, Text: text}
}

// DiffAddLine builds a DiffLine for a line added in the new version.
func DiffAddLine(text string) *renderv1.DiffLine {
	return &renderv1.DiffLine{Op: renderv1.DiffLineOp_DIFF_LINE_OP_ADD, Text: text}
}

// DiffRemoveLine builds a DiffLine for a line removed from the old
// version.
func DiffRemoveLine(text string) *renderv1.DiffLine {
	return &renderv1.DiffLine{Op: renderv1.DiffLineOp_DIFF_LINE_OP_REMOVE, Text: text}
}

// Table builds a TableNode from column headers and rows of string cells.
// Table is deliberately flat — string cells only, no nested RenderNode per
// cell (docs/specifications/frontend/render-tree.md#node-types) — each
// row's cells are expected to align to headers by index, but Table does
// not itself validate row width against len(headers); a mismatched row is
// a producer bug the frontend renders as-is rather than one this builder
// silently pads or truncates.
func Table(headers []string, rows [][]string) *renderv1.RenderNode {
	tableRows := make([]*renderv1.TableRow, len(rows))
	for i, row := range rows {
		tableRows[i] = &renderv1.TableRow{Cells: row}
	}
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Table{
			Table: &renderv1.TableNode{Headers: headers, Rows: tableRows},
		},
	}
}

// Link builds a LinkNode — a hyperlink with display text and a target
// URL, in that field order to match LinkNode.Text/LinkNode.Url.
func Link(text, url string) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Link{
			Link: &renderv1.LinkNode{Text: text, Url: url},
		},
	}
}

// List builds an unordered (bulleted) ListNode from its items, in display
// order. Use OrderedList for a numbered list.
func List(items ...*renderv1.RenderNode) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_List{
			List: &renderv1.ListNode{Items: items, Ordered: false},
		},
	}
}

// OrderedList builds a numbered ListNode from its items, in display
// order. Use List for a bulleted (unordered) list.
func OrderedList(items ...*renderv1.RenderNode) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_List{
			List: &renderv1.ListNode{Items: items, Ordered: true},
		},
	}
}

// Group builds a GroupNode — a plain, transparent container with no
// implied wrapper (border, indentation, label) beyond what the frontend
// chooses to apply.
func Group(children ...*renderv1.RenderNode) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Group{
			Group: &renderv1.GroupNode{Children: children},
		},
	}
}

// Collapsible builds a CollapsibleNode that starts expanded. Use
// CollapsedByDefault for one that starts collapsed.
func Collapsible(summary string, children ...*renderv1.RenderNode) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Collapsible{
			Collapsible: &renderv1.CollapsibleNode{Summary: summary, Children: children},
		},
	}
}

// CollapsedByDefault builds a CollapsibleNode that starts collapsed. Use
// Collapsible for one that starts expanded.
func CollapsedByDefault(summary string, children ...*renderv1.RenderNode) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Collapsible{
			Collapsible: &renderv1.CollapsibleNode{
				Summary:            summary,
				Children:           children,
				CollapsedByDefault: true,
			},
		},
	}
}

// SubSession builds a SubSessionNode — a pointer to a nested agent
// transcript (e.g. a RunSession-spawned child) rather than an inline copy
// of its content. See docs/specifications/kernel-callbacks.md and
// docs/specifications/agent-loop/subagents.md.
func SubSession(sessionID, summary string) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_SubSession{
			SubSession: &renderv1.SubSessionNode{SessionId: sessionID, Summary: summary},
		},
	}
}

// Action builds an ActionNode — interactive/clickable content. Per
// docs/specifications/frontend/render-tree.md#interactive-content-the-action-node,
// a frontend rendering this node MUST dispatch a ClientEvent.action_trigger
// carrying toolName/args/provider unchanged on activation, so Action
// carries exactly the fields that trigger needs alongside id (for
// correlating the resulting event back to this node) and label (the
// clickable text shown to the user).
func Action(id, label, toolName string, args *structpb.Struct, provider string) *renderv1.RenderNode {
	return &renderv1.RenderNode{
		Node: &renderv1.RenderNode_Action{
			Action: &renderv1.ActionNode{
				Id:       id,
				Label:    label,
				ToolName: toolName,
				Args:     args,
				Provider: provider,
			},
		},
	}
}
