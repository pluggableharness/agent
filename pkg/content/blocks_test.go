package content_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/content"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
)

func TestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "non-empty", text: "hello world"},
		{name: "empty", text: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := content.Text(tt.text)
			tb := got.GetText()
			if tb == nil {
				t.Fatalf("Text(%q).GetText() = nil, want a TextBlock", tt.text)
			}
			if tb.GetText() != tt.text {
				t.Errorf("Text(%q).GetText().GetText() = %q, want %q", tt.text, tb.GetText(), tt.text)
			}
			if _, ok := got.GetBlock().(*contentv1.ContentBlock_Text); !ok {
				t.Errorf("Text(%q) block variant = %T, want *ContentBlock_Text", tt.text, got.GetBlock())
			}
		})
	}
}

func TestImage(t *testing.T) {
	t.Parallel()

	data := []byte{0x89, 0x50, 0x4e, 0x47}
	const mediaType = "image/png"

	got := content.Image(data, mediaType)
	ib := got.GetImage()
	if ib == nil {
		t.Fatalf("Image().GetImage() = nil, want an ImageBlock")
	}
	if string(ib.GetData()) != string(data) {
		t.Errorf("Image().GetImage().GetData() = %v, want %v", ib.GetData(), data)
	}
	if ib.GetMediaType() != mediaType {
		t.Errorf("Image().GetImage().GetMediaType() = %q, want %q", ib.GetMediaType(), mediaType)
	}
}

func TestDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []content.Option
		wantFilename string
		wantHasName  bool
	}{
		{name: "no filename", opts: nil, wantHasName: false},
		{name: "with filename", opts: []content.Option{content.WithFilename("report.pdf")}, wantFilename: "report.pdf", wantHasName: true},
	}

	data := []byte("%PDF-1.4")
	const mediaType = "application/pdf"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := content.Document(data, mediaType, tt.opts...)
			db := got.GetDocument()
			if db == nil {
				t.Fatalf("Document().GetDocument() = nil, want a DocumentBlock")
			}
			if string(db.GetData()) != string(data) {
				t.Errorf("Document().GetDocument().GetData() = %v, want %v", db.GetData(), data)
			}
			if db.GetMediaType() != mediaType {
				t.Errorf("Document().GetDocument().GetMediaType() = %q, want %q", db.GetMediaType(), mediaType)
			}
			if hasName := db.Filename != nil; hasName != tt.wantHasName {
				t.Errorf("Document() Filename set = %v, want %v", hasName, tt.wantHasName)
			}
			if tt.wantHasName && db.GetFilename() != tt.wantFilename {
				t.Errorf("Document().GetDocument().GetFilename() = %q, want %q", db.GetFilename(), tt.wantFilename)
			}
		})
	}
}

func TestToolUse(t *testing.T) {
	t.Parallel()

	args, err := structpb.NewStruct(map[string]any{"path": "/tmp/x"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	got := content.ToolUse("tu-1", "read_file", args)
	tu := got.GetToolUse()
	if tu == nil {
		t.Fatalf("ToolUse().GetToolUse() = nil, want a ToolUseBlock")
	}
	if tu.GetId() != "tu-1" {
		t.Errorf("ToolUse().GetToolUse().GetId() = %q, want %q", tu.GetId(), "tu-1")
	}
	if tu.GetName() != "read_file" {
		t.Errorf("ToolUse().GetToolUse().GetName() = %q, want %q", tu.GetName(), "read_file")
	}
	if tu.GetArguments().GetFields()["path"].GetStringValue() != "/tmp/x" {
		t.Errorf("ToolUse().GetToolUse().GetArguments()[path] = %v, want /tmp/x", tu.GetArguments().GetFields()["path"])
	}
}

func TestToolResult(t *testing.T) {
	t.Parallel()

	nested := content.ToolUse("nested-1", "inner_tool", nil)
	textBlock := content.Text("done")

	got := content.ToolResult("tu-1", textBlock, nested)
	tr := got.GetToolResult()
	if tr == nil {
		t.Fatalf("ToolResult().GetToolResult() = nil, want a ToolResultBlock")
	}
	if tr.GetToolUseId() != "tu-1" {
		t.Errorf("ToolResult().GetToolResult().GetToolUseId() = %q, want %q", tr.GetToolUseId(), "tu-1")
	}
	if tr.GetIsError() {
		t.Error("ToolResult().GetToolResult().GetIsError() = true, want false")
	}
	nestedContent := tr.GetContent()
	if len(nestedContent) != 2 {
		t.Fatalf("ToolResult() content length = %d, want 2", len(nestedContent))
	}
	if nestedContent[0].GetText().GetText() != "done" {
		t.Errorf("ToolResult() content[0] text = %q, want %q", nestedContent[0].GetText().GetText(), "done")
	}
	if nestedContent[1].GetToolUse().GetName() != "inner_tool" {
		t.Errorf("ToolResult() content[1] tool_use name = %q, want %q", nestedContent[1].GetToolUse().GetName(), "inner_tool")
	}
}

func TestToolErrorResult(t *testing.T) {
	t.Parallel()

	got := content.ToolErrorResult("tu-2", content.Text("denied"))
	tr := got.GetToolResult()
	if tr == nil {
		t.Fatalf("ToolErrorResult().GetToolResult() = nil, want a ToolResultBlock")
	}
	if !tr.GetIsError() {
		t.Error("ToolErrorResult().GetToolResult().GetIsError() = false, want true")
	}
	if tr.GetToolUseId() != "tu-2" {
		t.Errorf("ToolErrorResult().GetToolResult().GetToolUseId() = %q, want %q", tr.GetToolUseId(), "tu-2")
	}
	if len(tr.GetContent()) != 1 || tr.GetContent()[0].GetText().GetText() != "denied" {
		t.Errorf("ToolErrorResult() content = %v, want single text block %q", tr.GetContent(), "denied")
	}
}

func TestToolResult_noContent(t *testing.T) {
	t.Parallel()

	got := content.ToolResult("tu-3")
	if got.GetToolResult() == nil {
		t.Fatalf("ToolResult(no content).GetToolResult() = nil, want a ToolResultBlock")
	}
	if len(got.GetToolResult().GetContent()) != 0 {
		t.Errorf("ToolResult(no content) content = %v, want empty", got.GetToolResult().GetContent())
	}
}

func TestThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		text      string
		signature []byte
	}{
		{name: "with signature", text: "let me reason about this", signature: []byte{0x01, 0x02}},
		{name: "nil signature", text: "reasoning", signature: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := content.Thinking(tt.text, tt.signature)
			tb := got.GetThinking()
			if tb == nil {
				t.Fatalf("Thinking().GetThinking() = nil, want a ThinkingBlock")
			}
			if tb.GetText() != tt.text {
				t.Errorf("Thinking().GetThinking().GetText() = %q, want %q", tb.GetText(), tt.text)
			}
			if string(tb.GetSignature()) != string(tt.signature) {
				t.Errorf("Thinking().GetThinking().GetSignature() = %v, want %v", tb.GetSignature(), tt.signature)
			}
		})
	}
}

func TestRedactedThinking(t *testing.T) {
	t.Parallel()

	data := []byte{0xde, 0xad, 0xbe, 0xef}
	got := content.RedactedThinking(data)
	rb := got.GetRedactedThinking()
	if rb == nil {
		t.Fatalf("RedactedThinking().GetRedactedThinking() = nil, want a RedactedThinkingBlock")
	}
	if string(rb.GetData()) != string(data) {
		t.Errorf("RedactedThinking().GetRedactedThinking().GetData() = %v, want %v", rb.GetData(), data)
	}
}
