// Command anthropic is the Anthropic model-provider plugin.
//
// It is a hashicorp/go-plugin subprocess: the kernel launches it, speaks
// pluggableharness.model.v1.ModelService to it over gRPC, and kills it at
// session end. It is never run directly by a human — started from a
// shell it simply prints go-plugin's handshake line and waits.
//
// Everything here is wiring, per .claude/rules/go-layout.md: build the
// provider, hand it to pkg/model's service adapter, serve. All real logic
// lives in internal/anthropic.
package main

import (
	"github.com/pluggableharness/agent/internal/anthropic"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Identity this build reports through the Describe RPC.
//
// These are variables rather than constants so a release build can stamp
// them with -ldflags (see .goreleaser.yaml), matching how
// internal/pluginhost's integration fixture is built. Describe has to
// answer from the running process because a dev_overrides binary has no
// agent.lock.hcl entry for the kernel to read identity from
// (configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry).
var (
	pluginName    = "anthropic"
	pluginVersion = "0.0.0"
	pluginSource  = "github.com/pluggableharness/agent-provider-anthropic"
)

func main() {
	identity := plugin.Identity{
		Name:    pluginName,
		Version: pluginVersion,
		Source:  pluginSource,
	}

	// The callback handle is constructed here and handed to both
	// plugin.Serve and the model service, but is deliberately never
	// dialed from main: pkg/plugin's "callback-timing trap" means
	// Callback.Client may only be called from inside an RPC handler,
	// after go-plugin has begun serving the broker.
	callback := plugin.NewCallback()
	provider := anthropic.New()

	plugin.Serve(plugin.Config{
		Identity: identity,
		Category: commonv1.Category_CATEGORY_MODEL,
		Callback: callback,
		Services: []plugin.Service{model.NewService(provider, identity, callback)},
	})
}
