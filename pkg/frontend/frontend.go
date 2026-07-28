package frontend

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// Capabilities is this frontend's static self-description, returned by
// GetCapabilities. It MUST be cheaply re-derivable and MUST NOT require a
// network call.
type Capabilities struct {
	// SlashCommands are the prompt-expansion commands this frontend
	// itself contributes. A direct-invoke command is declared exclusively
	// by a slashcommand.v1 provider instead, never here.
	SlashCommands []*commonv1.PromptExpansionSpec
	// ConfigSchema is this provider's agent.hcl configuration schema.
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints are the hook points this frontend can subscribe
	// to, so a mis-declared agent.hcl hook{} block naming an unsupported
	// point is rejected at config-load time.
	SupportedHookPoints []commonv1.HookPoint
}

// Provider is the interface a frontend plugin author implements. NewService
// adapts a Provider into the generated frontendv1.FrontendServiceServer.
//
// Kernel-to-frontend traffic (session state, metadata, transcript events,
// token deltas) and frontend-to-kernel control (SubmitInput, session
// lifecycle, plan/interactive resolution) ride the kernel callback
// channel — this interface is only the standard category triple.
type Provider interface {
	// Capabilities returns this frontend's static self-description. MUST
	// be cheaply re-queryable and MUST NOT require a network call.
	Capabilities(ctx context.Context) (*Capabilities, error)
	// Configure applies this provider's agent.hcl configuration, already
	// validated by the kernel against the ConfigSchema Capabilities
	// returned. A returned *Error becomes the structured detail of the
	// resulting gRPC status; any other error is wrapped as
	// FRONTEND_ERROR_CATEGORY_UNKNOWN.
	Configure(ctx context.Context, config *structpb.Struct) error
}
