package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// Emit persists one event into the session's state backend
// (kernel-callbacks.md#emit) — the only way a plugin writes into the state
// backend; the kernel is the state backend's sole writer and performs the
// actual write, assigning the returned id and the ordering-authoritative
// sequence number itself. A successful Emit also republishes the same
// event onto the event bus, on the reserved topic
// "kernel.event.{kind}" — a Subscribe caller sees it there without polling
// ReadEvents.
//
// sessionID is an explicit, required parameter rather than folded into an
// options struct where it could be left at its zero value: every
// mandatory-session_id RPC on this service "follows Emit's rule: the
// kernel MUST reject a call naming any session other than the one the
// calling plugin was actually invoked for" (kernel-callbacks.md's
// session-scoping rule) — an empty or wrong sessionID here is a request
// the kernel will reject, not a value ever safe to default or omit.
//
// kind MUST NOT be EventKind_EVENT_KIND_UNSPECIFIED — the kernel rejects
// it with a detectable, named "unspecified" error rather than silently
// treating it as some real event kind (kernel-callbacks.md#emit).
// schemaVersion MUST name the pluggableharness.event.vN package payload's
// shape belongs to (e.g. "1" for event.v1). payload is opaque to the
// kernel by design — it never inspects the bytes — and is interpreted only
// by whichever spec owns kind; producer identity is never a parameter here
// because the kernel derives it server-side from the calling connection.
func (c *Client) Emit(ctx context.Context, sessionID string, kind kernelv1.EventKind, schemaVersion string, payload []byte) (*kernelv1.EmitResult, error) {
	result, err := c.raw.Emit(ctx, &kernelv1.EmitRequest{
		SessionId:     sessionID,
		Kind:          kind,
		SchemaVersion: schemaVersion,
		Payload:       payload,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: emit: %w", err)
	}
	return result, nil
}
