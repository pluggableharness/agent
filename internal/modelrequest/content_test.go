package modelrequest

import (
	"errors"
	"strings"
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func textBlock(s string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: s}}}
}

func imageBlock() *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{MediaType: "image/png", Data: []byte("x")}}}
}

func documentBlock() *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Document{Document: &contentv1.DocumentBlock{MediaType: "application/pdf", Data: []byte("x")}}}
}

func userMessage(blocks ...*contentv1.ContentBlock) *contentv1.Message {
	return &contentv1.Message{Role: contentv1.Role_ROLE_USER, Content: blocks}
}

func TestValidateContentVisionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		supportsVision bool
		hasImage       bool
		wantErr        bool
	}{
		{name: "supported, image present", supportsVision: true, hasImage: true, wantErr: false},
		{name: "supported, image absent", supportsVision: true, hasImage: false, wantErr: false},
		{name: "unsupported, image present", supportsVision: false, hasImage: true, wantErr: true},
		{name: "unsupported, image absent", supportsVision: false, hasImage: false, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := &modelv1.ModelSpec{SupportsVision: tt.supportsVision}
			blocks := []*contentv1.ContentBlock{textBlock("hello")}
			if tt.hasImage {
				blocks = append(blocks, imageBlock())
			}
			messages := []*contentv1.Message{userMessage(blocks...)}

			err := ValidateContent(messages, spec)
			if tt.wantErr {
				assertUnsupported(t, err, "image", 0, 1)
			} else if err != nil {
				t.Fatalf("ValidateContent() = %v, want nil", err)
			}
		})
	}
}

func TestValidateContentDocumentMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		supportsDocuments bool
		hasDocument       bool
		wantErr           bool
	}{
		{name: "supported, document present", supportsDocuments: true, hasDocument: true, wantErr: false},
		{name: "supported, document absent", supportsDocuments: true, hasDocument: false, wantErr: false},
		{name: "unsupported, document present", supportsDocuments: false, hasDocument: true, wantErr: true},
		{name: "unsupported, document absent", supportsDocuments: false, hasDocument: false, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := &modelv1.ModelSpec{SupportsDocuments: tt.supportsDocuments}
			blocks := []*contentv1.ContentBlock{textBlock("hello")}
			if tt.hasDocument {
				blocks = append(blocks, documentBlock())
			}
			messages := []*contentv1.Message{userMessage(blocks...)}

			err := ValidateContent(messages, spec)
			if tt.wantErr {
				assertUnsupported(t, err, "document", 0, 1)
			} else if err != nil {
				t.Fatalf("ValidateContent() = %v, want nil", err)
			}
		})
	}
}

func TestValidateContentNamesTheRightBlockAmongSupportedOnes(t *testing.T) {
	t.Parallel()

	spec := &modelv1.ModelSpec{SupportsVision: true, SupportsDocuments: false}
	messages := []*contentv1.Message{
		userMessage(textBlock("ok"), imageBlock()), // fully valid message
		userMessage(textBlock("ok"), documentBlock()),
	}

	err := ValidateContent(messages, spec)
	assertUnsupported(t, err, "document", 1, 1)
}

func TestValidateContentEmptyMessages(t *testing.T) {
	t.Parallel()

	spec := &modelv1.ModelSpec{SupportsVision: false, SupportsDocuments: false}
	if err := ValidateContent(nil, spec); err != nil {
		t.Fatalf("ValidateContent(nil, ...) = %v, want nil", err)
	}
	if err := ValidateContent([]*contentv1.Message{userMessage()}, spec); err != nil {
		t.Fatalf("ValidateContent with no blocks = %v, want nil", err)
	}
}

func TestValidateContentNilSpecRejectsBoth(t *testing.T) {
	t.Parallel()

	if err := ValidateContent([]*contentv1.Message{userMessage(imageBlock())}, nil); err == nil {
		t.Fatalf("ValidateContent with nil spec and image = nil, want ErrUnsupportedContent")
	}
	if err := ValidateContent([]*contentv1.Message{userMessage(documentBlock())}, nil); err == nil {
		t.Fatalf("ValidateContent with nil spec and document = nil, want ErrUnsupportedContent")
	}
}

func TestUnsupportedContentErrorMessage(t *testing.T) {
	t.Parallel()

	err := &UnsupportedContentError{MessageIndex: 2, BlockIndex: 1, Kind: "image"}
	got := err.Error()
	for _, want := range []string{"message 2", "block 1", "image"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Error() = %q, want it to mention %q", got, want)
		}
	}
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("errors.Is(err, ErrUnsupportedContent) = false")
	}
}

// assertUnsupported fails t unless err wraps ErrUnsupportedContent and its
// *UnsupportedContentError names wantKind at wantMsgIdx/wantBlockIdx.
func assertUnsupported(t *testing.T, err error, wantKind string, wantMsgIdx, wantBlockIdx int) {
	t.Helper()

	if err == nil {
		t.Fatalf("ValidateContent() = nil, want an ErrUnsupportedContent-wrapping error")
	}
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("errors.Is(err, ErrUnsupportedContent) = false for err %v", err)
	}
	var uce *UnsupportedContentError
	if !errors.As(err, &uce) {
		t.Fatalf("errors.As(err, &UnsupportedContentError) = false for err %v", err)
	}
	if uce.Kind != wantKind {
		t.Fatalf("Kind = %q, want %q", uce.Kind, wantKind)
	}
	if uce.MessageIndex != wantMsgIdx {
		t.Fatalf("MessageIndex = %d, want %d", uce.MessageIndex, wantMsgIdx)
	}
	if uce.BlockIndex != wantBlockIdx {
		t.Fatalf("BlockIndex = %d, want %d", uce.BlockIndex, wantBlockIdx)
	}
}
