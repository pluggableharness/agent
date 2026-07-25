package modelrequest

import (
	"errors"
	"fmt"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// ErrUnsupportedContent is the sentinel every *UnsupportedContentError
// wraps, so a caller can test for this rejection with errors.Is without
// caring about the exact message index/block index/kind — per
// data-types.md's "Canonical message & content-block schema" section,
// this is the kernel-level reject case for an ImageBlock or DocumentBlock
// sent to a model whose ModelSpec doesn't declare support for it. It is
// never a silent drop: frontend/frontend-protocol.md#usermessage-carries-contentblocks
// requires the kernel reject an image against a non-vision model with a
// clear error, not swallow the block.
var ErrUnsupportedContent = errors.New("modelrequest: content block unsupported by model")

// UnsupportedContentError is ValidateContent's structured error, naming
// exactly which block failed and where. Callers use errors.Is(err,
// ErrUnsupportedContent) to detect the rejection generically, or
// errors.As to recover the message/block position.
type UnsupportedContentError struct {
	// MessageIndex is the zero-based index into the messages slice
	// ValidateContent was given.
	MessageIndex int

	// BlockIndex is the zero-based index into that message's Content
	// slice.
	BlockIndex int

	// Kind names the unsupported content-block kind: "image" or
	// "document".
	Kind string
}

// Error implements the error interface.
func (e *UnsupportedContentError) Error() string {
	return fmt.Sprintf("modelrequest: message %d block %d: %s block unsupported by model: %v", e.MessageIndex, e.BlockIndex, e.Kind, ErrUnsupportedContent)
}

// Unwrap makes errors.Is(err, ErrUnsupportedContent) succeed for any
// *UnsupportedContentError.
func (e *UnsupportedContentError) Unwrap() error {
	return ErrUnsupportedContent
}

// ValidateContent checks every ContentBlock in messages against spec's
// declared support, per data-types.md's "Canonical message & content-block
// schema" section:
//
//   - an ImageBlock MUST be rejected if spec.GetSupportsVision() is
//     false.
//   - a DocumentBlock MUST be rejected if spec.GetSupportsDocuments() is
//     false.
//
// It returns the first violation found, scanning messages and each
// message's content blocks in order, wrapped as an
// *UnsupportedContentError naming the offending block's kind and
// position. It returns nil if every block is supported (or if messages
// contains neither block kind at all — text/tool_use/tool_result/
// thinking/redacted_thinking blocks are never checked here, since v1's
// two independently-gated kinds are exactly image and document). A nil
// spec is treated as a model declaring no vision or document support, so
// any image or document block against a nil spec is rejected.
func ValidateContent(messages []*contentv1.Message, spec *modelv1.ModelSpec) error {
	supportsVision := spec.GetSupportsVision()
	supportsDocuments := spec.GetSupportsDocuments()

	for mi, msg := range messages {
		for bi, block := range msg.GetContent() {
			switch {
			case block.GetImage() != nil && !supportsVision:
				return &UnsupportedContentError{MessageIndex: mi, BlockIndex: bi, Kind: "image"}
			case block.GetDocument() != nil && !supportsDocuments:
				return &UnsupportedContentError{MessageIndex: mi, BlockIndex: bi, Kind: "document"}
			}
		}
	}
	return nil
}
