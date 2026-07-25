package plugin

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// previewProbeTimeout bounds a single Preview RPC made solely to
// discover whether a tool operation implements the optional Preview
// RPC — see resolveSupportsPreview and doc.go's "Resolving
// SupportsPreview". One-time, catalog-build-time cost; a hung plugin
// must not be able to block the rest of New.
const previewProbeTimeout = 5 * time.Second

// previewProbeCallID marks a Preview call made only to discover
// SupportsPreview, never to obtain a real preview — visible to a
// plugin's own logging/telemetry if it chooses to echo CallContext, and
// to anyone reading a trace, as distinct from a plan/apply gate's real
// Preview call for an actual pending ToolCall.
const previewProbeCallID = "providercatalog/plugin: supports-preview probe"

// resolveSupportsPreview reports whether the tool operation named
// toolName, served by client, implements the optional Preview RPC
// (tool/protocol.md#preview). client may be nil (the provider's Live
// entry did not assert to a tool client), in which case the answer is
// conservatively false — a nil client can serve nothing.
//
// The single RPC attempt here is the only signal the protocol offers;
// see doc.go's "Resolving SupportsPreview" for why checking specifically
// for codes.Unimplemented — and treating every other outcome, success or
// otherwise, as "implemented" — is a reliable discriminator regardless
// of whether the synthetic empty-arguments probe call itself would pass
// toolName's declared input schema.
func resolveSupportsPreview(ctx context.Context, tel *telemetry.Provider, logger *slog.Logger, client toolv1.ToolServiceClient, producer *commonv1.ProducerRef, toolName string) bool {
	if client == nil {
		return false
	}

	ctx, span := tel.StartToolPreview(ctx, toolName, producer)
	defer func() { telemetry.EndSpan(span, nil) }()

	probeCtx, cancel := context.WithTimeout(ctx, previewProbeTimeout)
	defer cancel()

	logger.DebugContext(probeCtx, "providercatalog/plugin: probing tool Preview support",
		"provider", producer.GetName(), "tool", toolName)

	_, err := client.Preview(probeCtx, &toolv1.PreviewRequest{
		Call: &toolv1.ToolCall{
			Id:        previewProbeCallID,
			ToolName:  toolName,
			Arguments: &structpb.Struct{},
		},
	})

	code := status.Code(err)
	switch code {
	case codes.Unimplemented:
		logger.DebugContext(probeCtx, "providercatalog/plugin: tool does not implement Preview",
			"provider", producer.GetName(), "tool", toolName)
		return false
	case codes.Canceled, codes.DeadlineExceeded:
		logger.WarnContext(probeCtx, "providercatalog/plugin: Preview probe did not complete in time, assuming unsupported",
			"provider", producer.GetName(), "tool", toolName, "code", code.String())
		return false
	default:
		// codes.OK (err == nil, a real preview came back) and any other
		// error code both mean the plugin's Previewer implementation was
		// reached at all — see the doc comment above.
		return true
	}
}
