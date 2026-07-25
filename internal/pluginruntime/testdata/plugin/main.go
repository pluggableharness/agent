//go:build integration

// Command plugin is the minimal fixture internal/pluginruntime's
// integration tier (launch_integration_test.go) builds and launches as a
// real subprocess, to exercise a genuine hashicorp/go-plugin round-trip:
// one canned ToolService.GetSchema RPC, plus one callback into
// KernelCallbackService.Log over the fixed callback broker ID
// (pkg/common.CallbackBrokerID), proving the reverse channel.
//
// Built on pkg/plugin and pkg/tool — the plugin-author SDK a real
// third-party plugin also imports — rather than hand-rolling the
// hashicorp/go-plugin adapter directly. This is the SDK's own
// end-to-end acceptance test: if this fixture, built with nothing but the
// public pkg/plugin/pkg/tool/pkg/hook surface, still round-trips through
// this package's real launch sequence, the SDK genuinely works. It also
// registers hook.Service alongside tool.Service on the same
// plugin.Config.Services slice, proving pkg/plugin's multi-service muxing
// (agent-loop/hook-dispatch.md's "one shared connection, more than one
// gRPC service") doesn't break a real subprocess launch. Its Observe
// facet logs back through the kernel callback so
// launch_integration_test.go can prove a DispatchHook issued through
// internal/pluginruntime.Plugin.HookClient reached *this* subprocess over
// the same muxed connection its ToolServiceClient was dispensed on.
//
// Build-tagged integration so it never enters the default `go build ./...`
// (which already skips testdata/ regardless).
package main

import (
	"context"
	"log/slog"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/hook"
	"github.com/pluggableharness/agent/pkg/plugin"
	"github.com/pluggableharness/agent/pkg/schema"
	"github.com/pluggableharness/agent/pkg/tool"
)

// fixtureToolName is the single tool GetSchema reports — checked by
// launch_integration_test.go to confirm the RPC actually round-tripped
// through the real subprocess rather than returning a zero value some
// other way.
const fixtureToolName = "fixture_echo"

// fixtureHookLogMessage is what Observe logs back through the kernel
// callback — launch_integration_test.go waits for it to confirm a
// DispatchHook call landed in this subprocess, rather than being answered
// by anything else on the kernel side.
const fixtureHookLogMessage = "fixture hook observed"

// fixtureIdentity is this fixture's own self-reported plugin.Identity, per
// pkg/plugin.Identity's doc comment — used both for Describe (not
// exercised by this fixture's test) and for building tool.Service.
var fixtureIdentity = plugin.Identity{
	Name:    "fixture",
	Version: "0.0.0",
	Source:  "internal/pluginruntime/testdata/plugin",
}

// fixtureProvider implements tool.Provider — the ToolService this fixture
// serves — and hook.Observer, muxed onto the same connection via
// plugin.Config.Services to prove multi-service registration works.
type fixtureProvider struct {
	callback *plugin.Callback
}

var (
	_ tool.Provider = (*fixtureProvider)(nil)
	_ hook.Observer = (*fixtureProvider)(nil)
)

// Schema returns a single, fixed tool.Schema — the "one canned RPC" this
// fixture exists to round-trip — and, on its way out, calls back into
// KernelCallbackService.Log via the SDK's own NewSlogHandler. This is the
// sanctioned call site for callback.Client per pkg/plugin's "callback-
// timing trap" doc comment: an RPC handler, invoked only once go-plugin
// has already begun dispensing this process's client to the kernel, never
// eagerly from a background goroutine at process start.
func (p *fixtureProvider) Schema(ctx context.Context) ([]*tool.Schema, error) {
	if client, err := p.callback.Client(ctx); err == nil {
		slog.New(client.NewSlogHandler()).Info("fixture plugin started")
	}

	empty, err := schema.Object(nil)
	if err != nil {
		return nil, err
	}
	return []*tool.Schema{
		{
			Name:         fixtureToolName,
			Kind:         tool.KindResource,
			Risk:         tool.RiskClassLow,
			Description:  "internal/pluginruntime integration fixture",
			InputSchema:  empty,
			OutputSchema: empty,
			Concurrency:  &tool.ConcurrencySpec{Safe: true},
			Idempotent:   true,
		},
	}, nil
}

// Configure accepts any config; this fixture takes none.
func (p *fixtureProvider) Configure(context.Context, map[string]any) error {
	return nil
}

// Invoke is never called by this fixture's test but must exist to satisfy
// tool.Provider.
func (p *fixtureProvider) Invoke(_ context.Context, call *tool.Call, stream *tool.Stream) error {
	return stream.Send(tool.NewResultEvent(map[string]any{"echo": call.Arguments}))
}

// Observe implements hook.Observer by logging back through the kernel
// callback channel, so a DispatchHook call the kernel issues over the
// muxed connection is observable on the kernel side as having genuinely
// reached this subprocess. Like Schema, this is an RPC handler — the
// sanctioned call site for callback.Client per pkg/plugin's
// "callback-timing trap" doc comment.
func (p *fixtureProvider) Observe(ctx context.Context, payload *hook.Payload) error {
	if client, err := p.callback.Client(ctx); err == nil {
		slog.New(client.NewSlogHandler()).Info(fixtureHookLogMessage, "point", payload.Point.String())
	}
	return nil
}

func main() {
	callback := plugin.NewCallback()
	provider := &fixtureProvider{callback: callback}

	plugin.Serve(plugin.Config{
		Identity: fixtureIdentity,
		Category: commonv1.Category_CATEGORY_TOOL,
		Callback: callback,
		Services: []plugin.Service{
			tool.NewService(provider, fixtureIdentity, callback),
			hook.NewService(provider),
		},
	})
}
