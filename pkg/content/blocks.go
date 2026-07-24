package content

import (
	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
)

// Text builds a ContentBlock carrying a TextBlock — the one variant every
// plugin MUST support, in both directions, unconditionally
// (docs/specifications/model/data-types.md's canonical-schema note).
func Text(text string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_Text{
			Text: &contentv1.TextBlock{Text: text},
		},
	}
}

// Image builds a ContentBlock carrying inline image bytes. data is the
// raw image content and mediaType is its MIME type (e.g. "image/png").
// Requires the target model's ModelSpec.supports_vision — the kernel MUST
// reject an image block sent to a model where that flag is false
// (docs/specifications/model/data-types.md's canonical-schema note); this
// package does not itself validate that, since the check needs the
// resolved ModelSpec this package has no access to.
func Image(data []byte, mediaType string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_Image{
			Image: &contentv1.ImageBlock{
				Data:      data,
				MediaType: mediaType,
			},
		},
	}
}

// Document builds a ContentBlock carrying inline non-image document
// content (e.g. a PDF) — the document-attachment analog of Image. data is
// the raw document content and mediaType is its MIME type (e.g.
// "application/pdf"). WithFilename sets the optional original filename.
// Requires the target model's ModelSpec.supports_documents, mirroring
// Image's supports_vision rule
// (docs/specifications/model/data-types.md's canonical-schema note).
func Document(data []byte, mediaType string, opts ...Option) *contentv1.ContentBlock {
	var o documentOptions
	for _, opt := range opts {
		opt(&o)
	}
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_Document{
			Document: &contentv1.DocumentBlock{
				Data:      data,
				MediaType: mediaType,
				Filename:  o.filename,
			},
		},
	}
}

// ToolUse builds a ContentBlock carrying a model's request to invoke a
// tool. id correlates this block to the ToolResultBlock that answers it
// (matching ToolResult's/ToolErrorResult's toolUseID argument); name is
// the tool's declared name; arguments are the call's already-parsed
// arguments, conforming to the tool's input_schema. Requires
// ModelSpec.supports_tool_use.
func ToolUse(id, name string, arguments *structpb.Struct) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_ToolUse{
			ToolUse: &contentv1.ToolUseBlock{
				Id:        id,
				Name:      name,
				Arguments: arguments,
			},
		},
	}
}

// ToolResult builds a ContentBlock carrying a successful tool invocation
// outcome. toolUseID is the id of the ToolUseBlock this result answers;
// content is the result's own content, recursively modeled as
// ContentBlocks (not a single string) because some vendors' tool results
// may include non-text content, e.g. an image a tool produced — in
// practice this is almost always a single Text block. Requires
// ModelSpec.supports_tool_use.
func ToolResult(toolUseID string, content ...*contentv1.ContentBlock) *contentv1.ContentBlock {
	return toolResult(toolUseID, false, content)
}

// ToolErrorResult builds a ContentBlock carrying a failed or denied tool
// invocation outcome — e.g. the plan/apply gate's synthesized denial
// block, which MUST let the model observe a denial in its own history
// (docs/specifications/model/data-types.md's ToolResultBlock.is_error
// note). Otherwise identical to ToolResult.
func ToolErrorResult(toolUseID string, content ...*contentv1.ContentBlock) *contentv1.ContentBlock {
	return toolResult(toolUseID, true, content)
}

// toolResult is the shared constructor behind ToolResult and
// ToolErrorResult, differing only in ToolResultBlock.IsError.
func toolResult(toolUseID string, isError bool, content []*contentv1.ContentBlock) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_ToolResult{
			ToolResult: &contentv1.ToolResultBlock{
				ToolUseId: toolUseID,
				Content:   content,
				IsError:   isError,
			},
		},
	}
}

// Thinking builds a ContentBlock carrying a model's extended-reasoning
// output. text is the accumulated reasoning text; signature is an opaque
// vendor integrity token, when the vendor's thinking blocks carry one —
// it MUST be stored and echoed back verbatim, never inspected or
// reformatted (docs/specifications/model/data-types.md's canonical-schema
// note); pass nil when the vendor doesn't supply one. Only relevant where
// ThinkingSpec.supported.
func Thinking(text string, signature []byte) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_Thinking{
			Thinking: &contentv1.ThinkingBlock{
				Text:      text,
				Signature: signature,
			},
		},
	}
}

// RedactedThinking builds a ContentBlock carrying a vendor-encrypted
// reasoning block that MUST be stored and round-tripped verbatim, exactly
// like Thinking's signature — the kernel never inspects data at all, not
// even as text (docs/specifications/model/data-types.md's canonical-schema
// note). Only relevant where ThinkingSpec.supported.
func RedactedThinking(data []byte) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_RedactedThinking{
			RedactedThinking: &contentv1.RedactedThinkingBlock{
				Data: data,
			},
		},
	}
}
