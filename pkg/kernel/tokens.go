package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// CountTokens resolves an exact-if-possible token count for req's content,
// optionally preferring req.ModelRef's tokenizer when that model provider
// implements its own optional CountTokens RPC (kernel-callbacks.md#counttokens).
// The result's Exact field distinguishes a real vendor tokenizer's count
// from the kernel's single documented fallback heuristic
// (kernel-callbacks.md#the-fallback-heuristic:
// ceil(total_utf8_byte_length/4)) — a caller MUST NOT re-derive that
// formula itself, this RPC is the one place it's implemented.
//
// CountTokens is plugin-scoped: req carries no session_id field, and this
// call is valid regardless of whether the calling plugin is currently
// invoked for any session (kernel-callbacks.md's plugin-scoped vs.
// session-scoped split). req is passed through directly rather than
// exploded into discrete parameters — Content (repeated, MUST be set) plus
// the optional ModelRef already form the request's full, minimal shape, so
// an options-struct-of-parameters would just be reproducing the generated
// type's own two fields.
//
// context.md and memory.md providers MUST route their own `tokens` field
// computation through this call rather than an arbitrary provider-local
// heuristic — see kernel-callbacks.md#why-a-kernel-primitive-not-a-provider-local-heuristic.
func (c *Client) CountTokens(ctx context.Context, req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	result, err := c.raw.CountTokens(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("kernel: count tokens: %w", err)
	}
	return result, nil
}
