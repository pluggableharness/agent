// Command provider is a minimal, complete model-provider plugin, and the
// reference an author starts from when writing a real one.
//
// # Why this is a separate Go module
//
// It exists to prove a property nothing else in this repository can:
// that `pkg/` is sufficient to write a plugin against, from outside this
// module. `internal/anthropic` is held to a depguard rule that forbids it
// importing any other `internal/` package, which *simulates* that
// isolation — but a simulation cannot catch an unexported type leaking
// through an exported signature, or a `pkg/` package that only compiles
// because something in the main module already resolved a dependency for
// it. Building this module in CI does.
//
// Its own go.mod carries a replace directive back to the working tree, so
// it is built against `pkg/` as it exists in the commit under review
// rather than the last published release.
//
// # What it demonstrates
//
// The three MUST RPCs (docs/specifications/model/conformance.md's summary
// matrix), the optional TokenCounter, and the plugin.Serve wiring. It
// invents no vendor: StreamCompletion echoes a canned completion, so the
// example stays about the SDK surface rather than about HTTP.
//
// A real provider replaces echoProvider's bodies with vendor calls and
// its catalog with a real roster. Everything else here — the identity
// stamping, the Serve call, the capability declaration shape — is what
// that provider would also do.
package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// modelID is the single model this example serves.
const modelID = "example-echo-1"

// attrGreeting is the one config attribute, present so the example shows
// a real ConfigSchema round trip rather than an empty one.
const attrGreeting = "greeting"

// Identity this build reports through Describe. Variables rather than
// constants so a release build can stamp them with -ldflags, matching how
// cmd/anthropic does it.
var (
	pluginName    = "example-echo"
	pluginVersion = "0.0.0"
	pluginSource  = "github.com/pluggableharness/agent/examples/provider"
)

// echoProvider implements model.Provider without a vendor behind it.
type echoProvider struct {
	greeting string
}

// Compile-time proof this type serves the three MUST RPCs and the SHOULD
// one. Render (MAY) is deliberately absent — the kernel's generic
// fallback renders plain text fine, which is what a provider with nothing
// unusual to show should do.
var (
	_ model.Provider     = (*echoProvider)(nil)
	_ model.TokenCounter = (*echoProvider)(nil)
)

// Capabilities declares the roster and config schema.
//
// No network call and no caching needed: the roster is a package
// constant. A provider fronting a gateway, whose roster is genuinely
// dynamic, resolves it once in Configure and serves it from memory here
// instead — see docs/specifications/model/protocol.md#getcapabilities.
func (p *echoProvider) Capabilities(context.Context) (*model.Capabilities, error) {
	greeting, err := config.Attribute(attrGreeting, configv1.AttrType_ATTR_TYPE_STRING,
		config.WithDefault(`"hello"`),
		config.WithDescription("Prefix this provider prepends to every echoed completion."),
	)
	if err != nil {
		return nil, fmt.Errorf("example: config schema: %w", err)
	}
	schema, err := config.Schema(greeting)
	if err != nil {
		return nil, fmt.Errorf("example: config schema: %w", err)
	}

	return model.NewCapabilities([]model.Spec{{
		ID:              modelID,
		ContextWindow:   200_000,
		MaxOutputTokens: 4096,
		SupportsToolUse: true,
		// Thinking and Caching left at their zero values: this model does
		// neither, and the zero value is the valid declaration for that.
		Thinking: model.ThinkingSpec{},
		Caching:  model.CachingSpec{},
		// Free, so the kernel's cost ledger stays at zero. A real provider
		// declares at least one PricingTier here, and exactly one tier must
		// match any (timestamp, input_token_count) pair.
		Pricing: model.Pricing{Currency: "USD", Free: true},
		SupportedToolChoiceModes: []modelv1.ToolChoiceMode{
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
		},
	}}, schema)
}

// Configure decodes the provider's agent.hcl block.
//
// It is written to be safely re-callable — the kernel may Configure a
// running plugin again when its configuration changes — so it replaces
// state wholesale rather than mutating it in place. A real provider
// rebuilds its vendor client here for the same reason.
func (p *echoProvider) Configure(_ context.Context, cfg *structpb.Struct) error {
	greeting := "hello"
	if v, ok := cfg.GetFields()[attrGreeting]; ok && v.GetStringValue() != "" {
		greeting = v.GetStringValue()
	}
	p.greeting = greeting
	return nil
}

// StreamCompletion echoes the last user message back.
//
// Note what a Provider is responsible for even with no vendor involved:
// exactly one terminal event (Sink enforces this), a usage event so the
// kernel has something to account, and treating cancellation as normal
// control flow rather than an error.
func (p *echoProvider) StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	if req.GetModelId() != modelID {
		return &model.Error{
			Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
			Message:   fmt.Sprintf("example: unknown model %q", req.GetModelId()),
			Retryable: false,
		}
	}

	// Content this model does not declare support for MUST be rejected,
	// never silently dropped: a dropped image means the model answers a
	// question about a picture it was never shown, and nothing upstream
	// can tell that happened.
	for _, m := range req.GetMessages() {
		for _, b := range m.GetContent() {
			switch b.GetBlock().(type) {
			case *contentv1.ContentBlock_Image, *contentv1.ContentBlock_Document:
				return &model.Error{
					Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
					Message:   "example: this model accepts text only",
					Retryable: false,
				}
			}
		}
	}

	// provider_options is pass-through: the kernel never reads a key, so a
	// vendor knob lives here rather than needing a protocol change. This
	// example uses it to let an operator override the greeting per request.
	greeting := p.greeting
	if v, ok := model.ProviderOptions(req).LookupString(attrGreeting); ok {
		greeting = v
	}

	reply := greeting + ", " + lastUserText(req.GetMessages())

	// Cancellation is normal control flow: return ctx.Err() unwrapped so
	// errors.Is(err, context.Canceled) works, and let pkg/model map it to
	// a bare codes.Canceled rather than an application error.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sink.TextDelta(reply); err != nil {
		return err
	}
	if err := sink.Usage(model.Usage{
		InputTokens:  int64(len(reply) / 4),
		OutputTokens: int64(len(reply) / 4),
	}); err != nil {
		return err
	}
	return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
}

// CountTokens implements the optional TokenCounter.
//
// It counts a whole request — messages, assembled context, and tool
// declarations — because that is what the RPC measures. A real provider
// calls its vendor's counting endpoint with the same three; the point
// here is that tool schemas are counted at all, since they are frequently
// the largest contributor and the easiest thing to forget.
func (p *echoProvider) CountTokens(_ context.Context, req *modelv1.CountTokensRequest) (int64, error) {
	var bytes int
	for _, m := range req.GetMessages() {
		for _, b := range m.GetContent() {
			bytes += len(b.GetText().GetText())
		}
	}
	for _, s := range req.GetAssembledContext() {
		for _, b := range s.GetContent() {
			bytes += len(b.GetText().GetText())
		}
	}
	for _, t := range req.GetTools() {
		bytes += len(t.GetName()) + len(t.GetDescription())
	}
	return int64((bytes + 3) / 4), nil
}

// lastUserText returns the text of the most recent user message, or a
// placeholder when there is none.
func lastUserText(messages []*contentv1.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].GetRole() != contentv1.Role_ROLE_USER {
			continue
		}
		var sb strings.Builder
		for _, b := range messages[i].GetContent() {
			sb.WriteString(b.GetText().GetText())
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return "(nothing to echo)"
}

func main() {
	identity := plugin.Identity{
		Name:    pluginName,
		Version: pluginVersion,
		Source:  pluginSource,
	}

	// Constructed here and handed to both Serve and the service, but never
	// dialed from main: pkg/plugin's callback-timing trap means
	// Callback.Client may only be called from inside an RPC handler.
	callback := plugin.NewCallback()

	plugin.Serve(plugin.Config{
		Identity: identity,
		Category: commonv1.Category_CATEGORY_MODEL,
		Callback: callback,
		Services: []plugin.Service{model.NewService(&echoProvider{greeting: "hello"}, identity, callback)},
	})
}
