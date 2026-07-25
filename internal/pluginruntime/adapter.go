package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

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

// launchScope holds everything scoped to one Launch call rather than to
// one category: the callback server served on the fixed callback broker,
// the sync.Once guarding that serve, and the muxed *grpc.ClientConn every
// service client for this subprocess is dialed over. Exactly one
// launchScope exists per launched subprocess, shared by reference across
// every categoryPlugin in that launch's plugin map.
//
// The Once lives here, not on categoryPlugin, because pkg/common's fixed
// CallbackBrokerID is only collision-free while broker.AcceptAndServe is
// called exactly once per subprocess (CLAUDE.md's "fixed callback broker
// ID" note). A launch whose plugin map carries more than one category —
// the dev_overrides category probe, which keys one subprocess by all seven
// commonv1.Category values because the real category isn't known ahead of
// time — dispenses more than one categoryPlugin against the same broker,
// so a per-categoryPlugin Once would let several goroutines race to serve
// the same fixed ID. Sharing one scope makes "exactly once" hold no matter
// how many categories a single launch dispenses.
type launchScope struct {
	callback  kernelv1.KernelCallbackServiceServer
	telemetry *telemetry.Provider

	serveOnce sync.Once
	conn      atomic.Pointer[grpc.ClientConn]
}

// newLaunchScope returns the launchScope shared by every categoryPlugin
// built for one Launch call.
func newLaunchScope(callback kernelv1.KernelCallbackServiceServer, prov *telemetry.Provider) *launchScope {
	return &launchScope{callback: callback, telemetry: prov}
}

// serveCallbackOnce starts serving KernelCallbackService on the fixed
// callback broker ID, at most once for this whole launch regardless of how
// many categoryPlugin values call it. A real *plugin.GRPCBroker has no
// exported constructor, so the once-guarded core is factored into
// doServeOnce (unit-tested directly, adapter_test.go) and only the
// one-line AcceptAndServe call itself is integration-tier.
func (s *launchScope) serveCallbackOnce(broker *plugin.GRPCBroker) {
	s.doServeOnce(func() {
		go broker.AcceptAndServe(common.CallbackBrokerID, s.newCallbackServer)
	})
}

// doServeOnce runs serve at most once per launchScope.
func (s *launchScope) doServeOnce(serve func()) {
	s.serveOnce.Do(serve)
}

// newCallbackServer builds the grpc.Server that serves
// KernelCallbackService back to the plugin over the callback broker. This
// is the only place internal/telemetry.Provider.ServerHandler() is wired
// in this package — see this package's CLAUDE.md.
func (s *launchScope) newCallbackServer(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts, grpc.StatsHandler(s.telemetry.ServerHandler()))
	gs := grpc.NewServer(opts...)
	kernelv1.RegisterKernelCallbackServiceServer(gs, s.callback)
	return gs
}

// clientConn returns the muxed connection this launch's category client
// was dialed over, or nil before any categoryPlugin has been dispensed.
func (s *launchScope) clientConn() *grpc.ClientConn {
	return s.conn.Load()
}

// categoryPlugin is the plugin.GRPCPlugin dispensed for exactly one
// category. GRPCClient (run kernel-side) registers the launch's callback
// server on the fixed callback broker — via the shared launchScope, so
// exactly once per subprocess however many categories this launch
// dispenses — then returns the raw generated <X>ServiceClient for
// category, never a hand-rolled wrapper (go-layout.md's "one Go
// representation of each wire message" rule).
type categoryPlugin struct {
	plugin.Plugin

	category commonv1.Category
	scope    *launchScope
}

var _ plugin.GRPCPlugin = (*categoryPlugin)(nil)

// GRPCServer always fails: see errGRPCServerUnsupported.
func (p *categoryPlugin) GRPCServer(*plugin.GRPCBroker, *grpc.Server) error {
	return errGRPCServerUnsupported
}

// GRPCClient runs kernel-side. It records the muxed connection on the
// shared launchScope (so Launch can dial a second service — the
// category-agnostic HookSubscriberService — over that same connection,
// per agent-loop/hook-dispatch.md's wire contract), starts serving
// KernelCallbackService on the fixed callback broker once per launch, then
// dispenses and returns the raw category service client dialed over conn.
func (p *categoryPlugin) GRPCClient(_ context.Context, broker *plugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	p.scope.conn.Store(conn)
	p.scope.serveCallbackOnce(broker)
	return newCategoryClient(p.category, conn)
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

// pluginMap builds the go-plugin PluginSet for categories (launch step 2),
// keyed by common.PluginKey(category), with every entry sharing scope.
//
// A normal launch passes exactly one category: one subprocess implements
// one category, and that stays the overwhelmingly common case. The
// variadic form exists for the one deliberate exception — probing a
// dev_overrides binary whose real category isn't known ahead of time,
// which keys one subprocess by several categories and calls Describe on
// each dispensed client to find the one that answers. Every entry sharing
// one scope is what keeps AcceptAndServe on the fixed callback broker ID
// exactly-once in that case (see launchScope).
func pluginMap(scope *launchScope, categories ...commonv1.Category) plugin.PluginSet {
	set := make(plugin.PluginSet, len(categories))
	for _, category := range categories {
		set[common.PluginKey(category)] = &categoryPlugin{
			category: category,
			scope:    scope,
		}
	}
	return set
}
