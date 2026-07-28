package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// GetSessionState returns the fixed-schema "where am I" snapshot for
// sessionID. Pair with Subscribe on topic kernel.state for live updates.
func (c *Client) GetSessionState(ctx context.Context, sessionID string) (*sessionv1.SessionState, error) {
	result, err := c.raw.GetSessionState(ctx, &kernelv1.GetSessionStateRequest{SessionId: sessionID})
	if err != nil {
		return nil, fmt.Errorf("kernel: get session state: %w", err)
	}
	return result.GetState(), nil
}

// SubmitInput submits operator content as the next turn and returns the
// assigned turn_id for correlation.
func (c *Client) SubmitInput(ctx context.Context, sessionID string, content []*contentv1.ContentBlock) (turnID string, err error) {
	result, err := c.raw.SubmitInput(ctx, &kernelv1.SubmitInputRequest{
		SessionId: sessionID,
		Content:   content,
	})
	if err != nil {
		return "", fmt.Errorf("kernel: submit input: %w", err)
	}
	return result.GetTurnId(), nil
}

// ResolvePlanDecision answers a pending plan item that policy evaluated
// as ASK.
func (c *Client) ResolvePlanDecision(ctx context.Context, req *kernelv1.ResolvePlanDecisionRequest) error {
	if _, err := c.raw.ResolvePlanDecision(ctx, req); err != nil {
		return fmt.Errorf("kernel: resolve plan decision: %w", err)
	}
	return nil
}

// ResolvePlanDecisionArgs is a convenience wrapper around
// ResolvePlanDecision for the common allow/deny case.
func (c *Client) ResolvePlanDecisionArgs(ctx context.Context, sessionID, planItemID string, decision planv1.ClientDecision, scope planv1.PlanDecisionScope, corrected *structpb.Struct) error {
	return c.ResolvePlanDecision(ctx, &kernelv1.ResolvePlanDecisionRequest{
		SessionId:      sessionID,
		PlanItemId:     planItemID,
		Decision:       decision,
		CorrectedInput: corrected,
		Scope:          scope,
	})
}

// ResolveInteractive answers a pending interactive-kind tool call.
func (c *Client) ResolveInteractive(ctx context.Context, sessionID, callID string, response *structpb.Struct) error {
	if _, err := c.raw.ResolveInteractive(ctx, &kernelv1.ResolveInteractiveRequest{
		SessionId: sessionID,
		CallId:    callID,
		Response:  response,
	}); err != nil {
		return fmt.Errorf("kernel: resolve interactive: %w", err)
	}
	return nil
}

// Interrupt cancels the running turn for sessionID.
func (c *Client) Interrupt(ctx context.Context, sessionID string) error {
	if _, err := c.raw.Interrupt(ctx, &kernelv1.InterruptRequest{SessionId: sessionID}); err != nil {
		return fmt.Errorf("kernel: interrupt: %w", err)
	}
	return nil
}

// CreateSession creates a new session and auto-attaches the caller.
func (c *Client) CreateSession(ctx context.Context, req *kernelv1.CreateSessionRequest) (*sessionv1.SessionInfo, error) {
	result, err := c.raw.CreateSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("kernel: create session: %w", err)
	}
	return result.GetInfo(), nil
}

// AttachSession subscribes the caller to an existing session.
func (c *Client) AttachSession(ctx context.Context, sessionID string) (*sessionv1.SessionInfo, error) {
	result, err := c.raw.AttachSession(ctx, &kernelv1.AttachSessionRequest{SessionId: sessionID})
	if err != nil {
		return nil, fmt.Errorf("kernel: attach session: %w", err)
	}
	return result.GetInfo(), nil
}

// ResumeSession attaches a historical session for continuation or
// replay-only.
func (c *Client) ResumeSession(ctx context.Context, sessionID string) (*sessionv1.SessionInfo, error) {
	result, err := c.raw.ResumeSession(ctx, &kernelv1.ResumeSessionRequest{SessionId: sessionID})
	if err != nil {
		return nil, fmt.Errorf("kernel: resume session: %w", err)
	}
	return result.GetInfo(), nil
}

// DetachSession unsubscribes the caller from sessionID.
func (c *Client) DetachSession(ctx context.Context, sessionID string) error {
	if _, err := c.raw.DetachSession(ctx, &kernelv1.DetachSessionRequest{SessionId: sessionID}); err != nil {
		return fmt.Errorf("kernel: detach session: %w", err)
	}
	return nil
}

// ListSessions returns a filtered session summary list.
func (c *Client) ListSessions(ctx context.Context, req *kernelv1.ListSessionsRequest) ([]*sessionv1.SessionInfo, error) {
	result, err := c.raw.ListSessions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("kernel: list sessions: %w", err)
	}
	return result.GetSessions(), nil
}

// PublishMetadata upserts a MetadataBlock. The kernel stamps producer and
// liveness=LIVE.
func (c *Client) PublishMetadata(ctx context.Context, sessionID string, block *metadatav1.MetadataBlock) (*metadatav1.MetadataBlock, error) {
	result, err := c.raw.PublishMetadata(ctx, &kernelv1.PublishMetadataRequest{
		SessionId: sessionID,
		Block:     block,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: publish metadata: %w", err)
	}
	return result.GetBlock(), nil
}

// RetractMetadata flips a block to DISCONNECTED and republishes it.
func (c *Client) RetractMetadata(ctx context.Context, sessionID, blockID string) (*metadatav1.MetadataBlock, error) {
	result, err := c.raw.RetractMetadata(ctx, &kernelv1.RetractMetadataRequest{
		SessionId: sessionID,
		BlockId:   blockID,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: retract metadata: %w", err)
	}
	return result.GetBlock(), nil
}

// ListMetadata returns every known MetadataBlock for sessionID.
func (c *Client) ListMetadata(ctx context.Context, sessionID string) ([]*metadatav1.MetadataBlock, error) {
	result, err := c.raw.ListMetadata(ctx, &kernelv1.ListMetadataRequest{SessionId: sessionID})
	if err != nil {
		return nil, fmt.Errorf("kernel: list metadata: %w", err)
	}
	return result.GetBlocks(), nil
}

// InvokeSlashCommand dispatches a slash command against sessionID.
func (c *Client) InvokeSlashCommand(ctx context.Context, sessionID, name, args string) error {
	if _, err := c.raw.InvokeSlashCommand(ctx, &kernelv1.InvokeSlashCommandRequest{
		SessionId: sessionID,
		Name:      name,
		Args:      args,
	}); err != nil {
		return fmt.Errorf("kernel: invoke slash command: %w", err)
	}
	return nil
}

// TriggerAction dispatches an ActionNode activation.
func (c *Client) TriggerAction(ctx context.Context, req *kernelv1.TriggerActionRequest) error {
	if _, err := c.raw.TriggerAction(ctx, req); err != nil {
		return fmt.Errorf("kernel: trigger action: %w", err)
	}
	return nil
}

// DeltaHandler is called once per TokenDelta on a StreamDeltas stream.
type DeltaHandler func(delta *kernelv1.TokenDelta) error

// StreamDeltas opens the live-only token fast path for sessionID and
// delivers each delta to handler until the stream ends or ctx is canceled.
// The kernel does not batch; callers coalesce to their own refresh rate.
func (c *Client) StreamDeltas(ctx context.Context, sessionID string, handler DeltaHandler) error {
	stream, err := c.raw.StreamDeltas(ctx, &kernelv1.StreamDeltasRequest{SessionId: sessionID})
	if err != nil {
		return fmt.Errorf("kernel: stream deltas: %w", err)
	}
	for {
		delta, err := stream.Recv()
		if err != nil {
			// End-of-stream and cancellation are normal control flow
			// (grpc.md); anything else is a real failure and MUST NOT be
			// swallowed just because ctx happens to be done by now.
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
				return nil
			}
			return fmt.Errorf("kernel: stream deltas: recv: %w", err)
		}
		if err := handler(delta); err != nil {
			return err
		}
	}
}
