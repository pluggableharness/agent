package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/protobuf/types/known/structpb"
)

// GetConfig returns the calling plugin's own already-decoded agent.hcl
// configuration — the same shape Configure received
// (kernel-callbacks.md#getconfig). A plugin MUST NOT echo any value from
// the returned Struct into Emit, Publish, Render, a log line, or an error
// message if that value came from a sensitive config attribute — the
// same rule every category's own Configure already carries, restated here
// because GetConfig is a second channel a secret crosses.
func (c *Client) GetConfig(ctx context.Context) (*structpb.Struct, error) {
	result, err := c.raw.GetConfig(ctx, &kernelv1.GetConfigRequest{})
	if err != nil {
		return nil, fmt.Errorf("kernel: get config: %w", err)
	}
	return result.GetConfig(), nil
}
