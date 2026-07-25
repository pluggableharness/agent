package pluginhost

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// ErrUnknownClient reports a dispensed client of no recognized category
// type — reachable only if internal/pluginruntime grows a category this
// package has not been taught about.
var ErrUnknownClient = errors.New("pluginhost: unrecognized category client")

// The seven categories share a common shape — declare what you do,
// accept config, then category-specific RPCs
// (docs/specifications/architecture.md's "Seven categories share a
// common shape") — but not a common Go interface: each category's
// generated client is its own type with its own request/response
// messages, and the declare-what-you-do RPC is GetSchema for tool and
// GetCapabilities for the other six. The three functions below are the
// whole of that per-category knowledge, kept in one file so adding an
// eighth category means editing exactly one place.

// describeProducer calls the category's Describe RPC and returns the
// identity the plugin reports for itself. Every one of the seven
// categories declares Describe with an empty request and a producer-only
// response, so this is the one genuinely uniform step.
func describeProducer(ctx context.Context, client any) (*commonv1.ProducerRef, error) {
	switch c := client.(type) {
	case modelv1.ModelServiceClient:
		resp, err := c.Describe(ctx, &modelv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case toolv1.ToolServiceClient:
		resp, err := c.Describe(ctx, &toolv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case contextv1.ContextServiceClient:
		resp, err := c.Describe(ctx, &contextv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case memoryv1.MemoryServiceClient:
		resp, err := c.Describe(ctx, &memoryv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case frontendv1.FrontendServiceClient:
		resp, err := c.Describe(ctx, &frontendv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case widgetv1.WidgetServiceClient:
		resp, err := c.Describe(ctx, &widgetv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	case slashcommandv1.SlashCommandServiceClient:
		resp, err := c.Describe(ctx, &slashcommandv1.DescribeRequest{})
		return resp.GetProducer(), wrapRPC("describe", err)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnknownClient, client)
	}
}

// fetchCapabilities calls the category's capability-advertisement RPC —
// GetSchema for tool, GetCapabilities for the other six — and returns
// both the whole response (retained on Live for later consumers that
// need per-model specs, per-tool schemas, or subscribed hook points) and
// the ConfigSchema this package needs to decode the provider's own
// agent.hcl block.
//
// The ConfigSchema's position differs per category and is not
// guessable: model/context/memory/frontend/widget nest it inside a
// per-category Capabilities message, while tool and slashcommand carry
// it flat on the response.
func fetchCapabilities(ctx context.Context, client any) (any, *configv1.ConfigSchema, error) {
	switch c := client.(type) {
	case modelv1.ModelServiceClient:
		resp, err := c.GetCapabilities(ctx, &modelv1.GetCapabilitiesRequest{})
		return resp, resp.GetCapabilities().GetConfigSchema(), wrapRPC("get_capabilities", err)
	case toolv1.ToolServiceClient:
		resp, err := c.GetSchema(ctx, &toolv1.GetSchemaRequest{})
		return resp, resp.GetConfigSchema(), wrapRPC("get_schema", err)
	case contextv1.ContextServiceClient:
		resp, err := c.GetCapabilities(ctx, &contextv1.GetCapabilitiesRequest{})
		return resp, resp.GetCapabilities().GetConfigSchema(), wrapRPC("get_capabilities", err)
	case memoryv1.MemoryServiceClient:
		resp, err := c.GetCapabilities(ctx, &memoryv1.GetCapabilitiesRequest{})
		return resp, resp.GetCapabilities().GetConfigSchema(), wrapRPC("get_capabilities", err)
	case frontendv1.FrontendServiceClient:
		resp, err := c.GetCapabilities(ctx, &frontendv1.GetCapabilitiesRequest{})
		return resp, resp.GetCapabilities().GetConfigSchema(), wrapRPC("get_capabilities", err)
	case widgetv1.WidgetServiceClient:
		resp, err := c.GetCapabilities(ctx, &widgetv1.GetCapabilitiesRequest{})
		return resp, resp.GetCapabilities().GetConfigSchema(), wrapRPC("get_capabilities", err)
	case slashcommandv1.SlashCommandServiceClient:
		resp, err := c.GetCapabilities(ctx, &slashcommandv1.GetCapabilitiesRequest{})
		return resp, resp.GetConfigSchema(), wrapRPC("get_capabilities", err)
	default:
		return nil, nil, fmt.Errorf("%w: %T", ErrUnknownClient, client)
	}
}

// configurePlugin calls the category's Configure RPC with the already
// HCL-decoded config Struct. Every category's ConfigureRequest carries
// exactly one google.protobuf.Struct config field and an empty success
// response, so only the message types differ.
func configurePlugin(ctx context.Context, client any, cfg *structpb.Struct) error {
	var err error
	switch c := client.(type) {
	case modelv1.ModelServiceClient:
		_, err = c.Configure(ctx, &modelv1.ConfigureRequest{Config: cfg})
	case toolv1.ToolServiceClient:
		_, err = c.Configure(ctx, &toolv1.ConfigureRequest{Config: cfg})
	case contextv1.ContextServiceClient:
		_, err = c.Configure(ctx, &contextv1.ConfigureRequest{Config: cfg})
	case memoryv1.MemoryServiceClient:
		_, err = c.Configure(ctx, &memoryv1.ConfigureRequest{Config: cfg})
	case frontendv1.FrontendServiceClient:
		_, err = c.Configure(ctx, &frontendv1.ConfigureRequest{Config: cfg})
	case widgetv1.WidgetServiceClient:
		_, err = c.Configure(ctx, &widgetv1.ConfigureRequest{Config: cfg})
	case slashcommandv1.SlashCommandServiceClient:
		_, err = c.Configure(ctx, &slashcommandv1.ConfigureRequest{Config: cfg})
	default:
		return fmt.Errorf("%w: %T", ErrUnknownClient, client)
	}
	return wrapRPC("configure", err)
}

// wrapRPC prefixes an RPC error with this package and the operation,
// returning nil unchanged so callers can pass an error through
// unconditionally. The gRPC status is preserved for errors.As /
// status.FromError.
func wrapRPC(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("pluginhost: %s: %w", op, err)
}

// probeCategories is the fixed order a dev-override plugin of unknown
// category is probed in. It is deterministic rather than arbitrary so a
// binary that (incorrectly) answers Describe on more than one category
// resolves the same way on every run (.claude/rules/determinism.md).
var probeCategories = []commonv1.Category{
	commonv1.Category_CATEGORY_MODEL,
	commonv1.Category_CATEGORY_TOOL,
	commonv1.Category_CATEGORY_CONTEXT,
	commonv1.Category_CATEGORY_MEMORY,
	commonv1.Category_CATEGORY_FRONTEND,
	commonv1.Category_CATEGORY_WIDGET,
	commonv1.Category_CATEGORY_SLASHCOMMAND,
}
