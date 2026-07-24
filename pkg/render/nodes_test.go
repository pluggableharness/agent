package render_test

import (
	"testing"

	structpb "google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func TestText(t *testing.T) {
	t.Parallel()

	got := render.Text("hello")
	text, ok := got.GetNode().(*renderv1.RenderNode_Text)
	if !ok {
		t.Fatalf("Text(%q).GetNode() type = %T, want *renderv1.RenderNode_Text", "hello", got.GetNode())
	}
	if text.Text.GetContent() != "hello" {
		t.Errorf("Text(%q).Content = %q, want %q", "hello", text.Text.GetContent(), "hello")
	}
	if text.Text.Style != nil {
		t.Errorf("Text(%q).Style = %v, want nil (unset)", "hello", text.Text.Style)
	}
}

func TestTextStyled(t *testing.T) {
	t.Parallel()

	got := render.TextStyled("careful", renderv1.TextStyle_TEXT_STYLE_WARNING)
	text := got.GetText()
	if text == nil {
		t.Fatalf("TextStyled(...).GetText() = nil, want set")
	}
	if text.GetContent() != "careful" {
		t.Errorf("TextStyled content = %q, want %q", text.GetContent(), "careful")
	}
	if text.GetStyle() != renderv1.TextStyle_TEXT_STYLE_WARNING {
		t.Errorf("TextStyled style = %v, want %v", text.GetStyle(), renderv1.TextStyle_TEXT_STYLE_WARNING)
	}
}

func TestCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		language string
		content  string
		wantLang string
		wantSet  bool
	}{
		{name: "with language", language: "go", content: "package main", wantLang: "go", wantSet: true},
		{name: "no language", language: "", content: "plain text", wantSet: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := render.Code(tt.language, tt.content)
			block := got.GetCodeBlock()
			if block == nil {
				t.Fatalf("Code(%q, %q).GetCodeBlock() = nil, want set", tt.language, tt.content)
			}
			if block.GetContent() != tt.content {
				t.Errorf("Code content = %q, want %q", block.GetContent(), tt.content)
			}
			if tt.wantSet && (block.Language == nil || *block.Language != tt.wantLang) {
				t.Errorf("Code language = %v, want %q", block.Language, tt.wantLang)
			}
			if !tt.wantSet && block.Language != nil {
				t.Errorf("Code language = %q, want nil (unset)", *block.Language)
			}
		})
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	hunk := render.Hunk(1, 2, 1, 3,
		render.DiffContextLine("unchanged"),
		render.DiffAddLine("new line"),
		render.DiffRemoveLine("old line"),
	)
	got := render.Diff(hunk)
	diff := got.GetDiff()
	if diff == nil {
		t.Fatalf("Diff(...).GetDiff() = nil, want set")
	}
	if len(diff.GetHunks()) != 1 {
		t.Fatalf("Diff(...).Hunks has %d entries, want 1", len(diff.GetHunks()))
	}
	h := diff.GetHunks()[0]
	if h.GetOldStart() != 1 || h.GetOldLines() != 2 || h.GetNewStart() != 1 || h.GetNewLines() != 3 {
		t.Errorf("Hunk header = (%d,%d,%d,%d), want (1,2,1,3)", h.GetOldStart(), h.GetOldLines(), h.GetNewStart(), h.GetNewLines())
	}
	if len(h.GetLines()) != 3 {
		t.Fatalf("Hunk has %d lines, want 3", len(h.GetLines()))
	}

	wantOps := []renderv1.DiffLineOp{
		renderv1.DiffLineOp_DIFF_LINE_OP_CONTEXT,
		renderv1.DiffLineOp_DIFF_LINE_OP_ADD,
		renderv1.DiffLineOp_DIFF_LINE_OP_REMOVE,
	}
	wantText := []string{"unchanged", "new line", "old line"}
	for i, line := range h.GetLines() {
		if line.GetOp() != wantOps[i] {
			t.Errorf("line[%d].Op = %v, want %v", i, line.GetOp(), wantOps[i])
		}
		if line.GetText() != wantText[i] {
			t.Errorf("line[%d].Text = %q, want %q", i, line.GetText(), wantText[i])
		}
	}
}

func TestDiffEmpty(t *testing.T) {
	t.Parallel()

	got := render.Diff()
	diff := got.GetDiff()
	if diff == nil {
		t.Fatalf("Diff().GetDiff() = nil, want set")
	}
	if len(diff.GetHunks()) != 0 {
		t.Errorf("Diff().Hunks has %d entries, want 0", len(diff.GetHunks()))
	}
}

func TestTable(t *testing.T) {
	t.Parallel()

	headers := []string{"name", "value"}
	rows := [][]string{
		{"a", "1"},
		{"b", "2"},
	}
	got := render.Table(headers, rows)
	table := got.GetTable()
	if table == nil {
		t.Fatalf("Table(...).GetTable() = nil, want set")
	}
	if len(table.GetHeaders()) != 2 || table.GetHeaders()[0] != "name" || table.GetHeaders()[1] != "value" {
		t.Errorf("Table headers = %v, want %v", table.GetHeaders(), headers)
	}
	if len(table.GetRows()) != 2 {
		t.Fatalf("Table has %d rows, want 2", len(table.GetRows()))
	}
	if got, want := table.GetRows()[0].GetCells(), rows[0]; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Table row[0] = %v, want %v", got, want)
	}
	if got, want := table.GetRows()[1].GetCells(), rows[1]; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Table row[1] = %v, want %v", got, want)
	}
}

func TestLink(t *testing.T) {
	t.Parallel()

	got := render.Link("click me", "https://example.com")
	link := got.GetLink()
	if link == nil {
		t.Fatalf("Link(...).GetLink() = nil, want set")
	}
	if link.GetText() != "click me" {
		t.Errorf("Link text = %q, want %q", link.GetText(), "click me")
	}
	if link.GetUrl() != "https://example.com" {
		t.Errorf("Link url = %q, want %q", link.GetUrl(), "https://example.com")
	}
}

func TestList(t *testing.T) {
	t.Parallel()

	item1, item2 := render.Text("one"), render.Text("two")
	got := render.List(item1, item2)
	list := got.GetList()
	if list == nil {
		t.Fatalf("List(...).GetList() = nil, want set")
	}
	if list.GetOrdered() {
		t.Errorf("List(...).Ordered = true, want false")
	}
	if len(list.GetItems()) != 2 {
		t.Fatalf("List(...).Items has %d entries, want 2", len(list.GetItems()))
	}
}

func TestOrderedList(t *testing.T) {
	t.Parallel()

	got := render.OrderedList(render.Text("first"))
	list := got.GetList()
	if list == nil {
		t.Fatalf("OrderedList(...).GetList() = nil, want set")
	}
	if !list.GetOrdered() {
		t.Errorf("OrderedList(...).Ordered = false, want true")
	}
	if len(list.GetItems()) != 1 {
		t.Fatalf("OrderedList(...).Items has %d entries, want 1", len(list.GetItems()))
	}
}

func TestGroup(t *testing.T) {
	t.Parallel()

	got := render.Group(render.Text("a"), render.Text("b"))
	group := got.GetGroup()
	if group == nil {
		t.Fatalf("Group(...).GetGroup() = nil, want set")
	}
	if len(group.GetChildren()) != 2 {
		t.Fatalf("Group(...).Children has %d entries, want 2", len(group.GetChildren()))
	}
}

func TestCollapsible(t *testing.T) {
	t.Parallel()

	got := render.Collapsible("details", render.Text("body"))
	c := got.GetCollapsible()
	if c == nil {
		t.Fatalf("Collapsible(...).GetCollapsible() = nil, want set")
	}
	if c.GetSummary() != "details" {
		t.Errorf("Collapsible summary = %q, want %q", c.GetSummary(), "details")
	}
	if c.GetCollapsedByDefault() {
		t.Errorf("Collapsible(...).CollapsedByDefault = true, want false")
	}
	if len(c.GetChildren()) != 1 {
		t.Fatalf("Collapsible(...).Children has %d entries, want 1", len(c.GetChildren()))
	}
}

func TestCollapsedByDefault(t *testing.T) {
	t.Parallel()

	got := render.CollapsedByDefault("details", render.Text("body"))
	c := got.GetCollapsible()
	if c == nil {
		t.Fatalf("CollapsedByDefault(...).GetCollapsible() = nil, want set")
	}
	if !c.GetCollapsedByDefault() {
		t.Errorf("CollapsedByDefault(...).CollapsedByDefault = false, want true")
	}
}

func TestSubSession(t *testing.T) {
	t.Parallel()

	got := render.SubSession("session-01ARZ3", "did a thing")
	sub := got.GetSubSession()
	if sub == nil {
		t.Fatalf("SubSession(...).GetSubSession() = nil, want set")
	}
	if sub.GetSessionId() != "session-01ARZ3" {
		t.Errorf("SubSession sessionID = %q, want %q", sub.GetSessionId(), "session-01ARZ3")
	}
	if sub.GetSummary() != "did a thing" {
		t.Errorf("SubSession summary = %q, want %q", sub.GetSummary(), "did a thing")
	}
}

func TestAction(t *testing.T) {
	t.Parallel()

	args, err := structpb.NewStruct(map[string]any{"path": "/tmp/x"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	got := render.Action("action-1", "Undo", "undo_edit", args, "ripgrep")
	action := got.GetAction()
	if action == nil {
		t.Fatalf("Action(...).GetAction() = nil, want set")
	}
	if action.GetId() != "action-1" {
		t.Errorf("Action id = %q, want %q", action.GetId(), "action-1")
	}
	if action.GetLabel() != "Undo" {
		t.Errorf("Action label = %q, want %q", action.GetLabel(), "Undo")
	}
	if action.GetToolName() != "undo_edit" {
		t.Errorf("Action toolName = %q, want %q", action.GetToolName(), "undo_edit")
	}
	if action.GetProvider() != "ripgrep" {
		t.Errorf("Action provider = %q, want %q", action.GetProvider(), "ripgrep")
	}
	if action.GetArgs().GetFields()["path"].GetStringValue() != "/tmp/x" {
		t.Errorf("Action args[path] = %q, want %q", action.GetArgs().GetFields()["path"].GetStringValue(), "/tmp/x")
	}
}
