package plugin

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Service is one gRPC service registration a plugin process contributes.
// A category SDK's server.go returns one of these; a plugin author may
// pass more than one Service to Config.Services to mux e.g. ToolService +
// HookSubscriberService on the same connection.
type Service interface {
	// Register registers this service's handler on s.
	Register(s *grpc.Server)
}

// Config is what Serve needs to launch one plugin subprocess.
type Config struct {
	// Identity is this plugin build's own self-reported identity.
	Identity Identity
	// Category is this plugin's primary category — the one PluginSet
	// entry key.
	Category commonv1.Category
	// Callback is the lazily-dialed handle to the kernel callback
	// channel; the plugin author constructs it via NewCallback and holds
	// onto it.
	Callback *Callback
	// Services is one or more gRPC service registrations, muxed on the
	// same *grpc.Server.
	Services []Service
}

// Serve blocks and runs the plugin subprocess main loop — a thin,
// pre-wired call to hashicorp/go-plugin's own plugin.Serve, which does not
// return under normal operation. This is the function a plugin author's
// main() calls.
func Serve(cfg Config) {
	plugin.Serve(serveConfig(cfg))
}

// serveConfig builds the *plugin.ServeConfig Serve hands to
// hashicorp/go-plugin, factored out of Serve so the handshake and
// PluginSet wiring are unit-testable without invoking the real,
// blocks-forever plugin.Serve.
func serveConfig(cfg Config) *plugin.ServeConfig {
	return &plugin.ServeConfig{
		HandshakeConfig: common.Handshake,
		Plugins:         pluginSet(cfg),
		GRPCServer:      plugin.DefaultGRPCServer,
	}
}

// pluginSet builds the one-entry go-plugin PluginSet for cfg, keyed by
// common.PluginKey(cfg.Category) — the only entry a single plugin process
// ever serves, since one subprocess implements exactly one primary
// category (though, per Config.Services, it may mux additional service
// surfaces onto that same connection).
func pluginSet(cfg Config) plugin.PluginSet {
	return plugin.PluginSet{
		common.PluginKey(cfg.Category): &grpcPlugin{cfg: cfg},
	}
}

// grpcPlugin is the plugin.GRPCPlugin adapter Serve registers for
// cfg.Category — the plugin-side mirror of
// internal/pluginruntime/adapter.go's categoryPlugin (which runs
// kernel-side). GRPCServer registers every cfg.Services entry on the
// shared *grpc.Server and records broker on cfg.Callback for later lazy
// dialing (doc.go's "callback-timing trap"); GRPCClient always errors,
// since this package only ever runs plugin-side.
type grpcPlugin struct {
	plugin.Plugin

	cfg Config
}

var _ plugin.GRPCPlugin = (*grpcPlugin)(nil)

// GRPCServer registers every p.cfg.Services entry on s, then — if
// p.cfg.Callback is set — records broker on it so a later Callback.Client
// call can lazily dial the fixed callback broker ID once go-plugin
// actually begins serving it, which happens only after this method
// returns (doc.go).
func (p *grpcPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	for _, svc := range p.cfg.Services {
		svc.Register(s)
	}
	if p.cfg.Callback != nil {
		p.cfg.Callback.setBroker(broker)
	}
	return nil
}

// GRPCClient always fails: see errGRPCClientUnsupported.
func (p *grpcPlugin) GRPCClient(context.Context, *plugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, errGRPCClientUnsupported
}
