package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// errGRPCServerUnsupported is returned by categoryPlugin.GRPCServer: this
// package only ever runs kernel-side (Dispense-ing a plugin's client),
// never plugin-side (serving a category implementation) — see this
// package's CLAUDE.md. Misuse fails loudly, at call time, rather than
// silently no-op-ing.
var errGRPCServerUnsupported = errors.New("pluginruntime: GRPCServer is not supported kernel-side — this package only dials plugins, it never serves a category implementation")

// errUnrecognizedCategory is wrapped into newCategoryClient's error for a
// commonv1.Category value with no known generated client type.
var errUnrecognizedCategory = errors.New("pluginruntime: unrecognized category")

// categoryPlugin is the plugin.GRPCPlugin dispensed for exactly one
// category, for exactly one Launch call. GRPCClient (run kernel-side)
// registers callback on the fixed callback broker exactly once via
// brokerOnce, then returns the raw generated <X>ServiceClient for
// category — never a hand-rolled wrapper (go-layout.md's "one Go
// representation of each wire message" rule).
type categoryPlugin struct {
	plugin.Plugin

	category  commonv1.Category
	callback  kernelv1.KernelCallbackServiceServer
	telemetry *telemetry.Provider

	brokerOnce sync.Once
}

var _ plugin.GRPCPlugin = (*categoryPlugin)(nil)

// GRPCServer always fails: see errGRPCServerUnsupported.
func (p *categoryPlugin) GRPCServer(*plugin.GRPCBroker, *grpc.Server) error {
	return errGRPCServerUnsupported
}

// GRPCClient runs kernel-side. It starts serving KernelCallbackService on
// the fixed callback broker (once per categoryPlugin instance, i.e. once
// per Launch call), then dispenses and returns the raw category service
// client dialed over conn.
func (p *categoryPlugin) GRPCClient(_ context.Context, broker *plugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	p.brokerOnce.Do(func() {
		go broker.AcceptAndServe(common.CallbackBrokerID, p.newCallbackServer)
	})
	return newCategoryClient(p.category, conn)
}

// newCallbackServer builds the grpc.Server that serves
// KernelCallbackService back to the plugin over the callback broker. This
// is the only place internal/telemetry.Provider.ServerHandler() is wired
// in this package — see this package's CLAUDE.md.
func (p *categoryPlugin) newCallbackServer(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts, grpc.StatsHandler(p.telemetry.ServerHandler()))
	s := grpc.NewServer(opts...)
	kernelv1.RegisterKernelCallbackServiceServer(s, p.callback)
	return s
}

// newCategoryClient returns the raw generated ServiceClient for category,
// dialed over conn — the value a Plugin's Dispensed() ultimately returns.
func newCategoryClient(category commonv1.Category, conn *grpc.ClientConn) (any, error) {
	switch category {
	case commonv1.Category_CATEGORY_MODEL:
		return modelv1.NewModelServiceClient(conn), nil
	case commonv1.Category_CATEGORY_TOOL:
		return toolv1.NewToolServiceClient(conn), nil
	case commonv1.Category_CATEGORY_CONTEXT:
		return contextv1.NewContextServiceClient(conn), nil
	case commonv1.Category_CATEGORY_MEMORY:
		return memoryv1.NewMemoryServiceClient(conn), nil
	case commonv1.Category_CATEGORY_FRONTEND:
		return frontendv1.NewFrontendServiceClient(conn), nil
	case commonv1.Category_CATEGORY_WIDGET:
		return widgetv1.NewWidgetServiceClient(conn), nil
	case commonv1.Category_CATEGORY_SLASHCOMMAND:
		return slashcommandv1.NewSlashCommandServiceClient(conn), nil
	default:
		return nil, fmt.Errorf("%w: %v", errUnrecognizedCategory, category)
	}
}

// pluginMap builds the one-entry go-plugin PluginSet for category (launch
// step 2), keyed by common.PluginKey(category) — the only entry a single
// launch ever has, since one subprocess implements exactly one category.
func pluginMap(category commonv1.Category, callback kernelv1.KernelCallbackServiceServer, prov *telemetry.Provider) plugin.PluginSet {
	return plugin.PluginSet{
		common.PluginKey(category): &categoryPlugin{
			category:  category,
			callback:  callback,
			telemetry: prov,
		},
	}
}
