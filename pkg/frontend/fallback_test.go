package frontend_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/frontend"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func TestFallbackText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *renderv1.RenderNode
		want string
	}{
		{"nil node", nil, ""},
		{"empty node", &renderv1.RenderNode{}, ""},
		{
			"text",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Text{Text: &renderv1.TextNode{Content: "hello"}}},
			"hello",
		},
		{
			"code_block",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_CodeBlock{CodeBlock: &renderv1.CodeBlockNode{Content: "x := 1"}}},
			"x := 1",
		},
		{
			"link",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Link{Link: &renderv1.LinkNode{Text: "docs", Url: "https://example.com"}}},
			"docs (https://example.com)",
		},
		{
			"sub_session",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_SubSession{SubSession: &renderv1.SubSessionNode{SessionId: "s1", Summary: "child task"}}},
			"child task",
		},
		{
			"action",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Action{Action: &renderv1.ActionNode{Label: "Undo", ToolName: "undo"}}},
			"Undo",
		},
		{
			"table",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Table{Table: &renderv1.TableNode{
				Headers: []string{"a", "b"},
				Rows:    []*renderv1.TableRow{{Cells: []string{"1", "2"}}},
			}}},
			"a | b\n1 | 2",
		},
		{
			"list",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_List{List: &renderv1.ListNode{
				Items: []*renderv1.RenderNode{
					{Node: &renderv1.RenderNode_Text{Text: &renderv1.TextNode{Content: "one"}}},
					{Node: &renderv1.RenderNode_Text{Text: &renderv1.TextNode{Content: "two"}}},
				},
			}}},
			"one\ntwo",
		},
		{
			"group",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Group{Group: &renderv1.GroupNode{
				Children: []*renderv1.RenderNode{
					{Node: &renderv1.RenderNode_Text{Text: &renderv1.TextNode{Content: "child"}}},
				},
			}}},
			"child",
		},
		{
			"collapsible with children",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Collapsible{Collapsible: &renderv1.CollapsibleNode{
				Summary: "details",
				Children: []*renderv1.RenderNode{
					{Node: &renderv1.RenderNode_Text{Text: &renderv1.TextNode{Content: "body"}}},
				},
			}}},
			"details\nbody",
		},
		{
			"collapsible with no children",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Collapsible{Collapsible: &renderv1.CollapsibleNode{Summary: "empty"}}},
			"empty",
		},
		{
			"action with nil args",
			&renderv1.RenderNode{Node: &renderv1.RenderNode_Action{Action: &renderv1.ActionNode{
				Label: "Run", ToolName: "run", Args: &structpb.Struct{},
			}}},
			"Run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := frontend.FallbackText(tt.node); got != tt.want {
				t.Errorf("FallbackText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFallbackText_Diff(t *testing.T) {
	t.Parallel()

	node := &renderv1.RenderNode{Node: &renderv1.RenderNode_Diff{Diff: &renderv1.DiffNode{
		Hunks: []*renderv1.DiffHunk{
			{
				OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
				Lines: []*renderv1.DiffLine{
					{Op: renderv1.DiffLineOp_DIFF_LINE_OP_CONTEXT, Text: "unchanged"},
					{Op: renderv1.DiffLineOp_DIFF_LINE_OP_REMOVE, Text: "old"},
					{Op: renderv1.DiffLineOp_DIFF_LINE_OP_ADD, Text: "new"},
				},
			},
		},
	}}}

	got := frontend.FallbackText(node)
	want := "  unchanged\n- old\n+ new"
	if got != want {
		t.Errorf("FallbackText(diff) = %q, want %q", got, want)
	}
	if !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Errorf("FallbackText(diff) = %q, missing before/after text", got)
	}
}
