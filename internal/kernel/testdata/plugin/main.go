//go:build integration

// Command plugin is the model-provider fixture internal/kernel's
// integration tier (kernel_integration_test.go) builds and launches as a
// real subprocess.
//
// It exists because a whole kernel session cannot run without a model
// provider: internal/session resolves a profile's model chain against the
// live provider catalog and fails outright with no model to route to. The
// two fixtures that already existed when this was written
// (internal/pluginhost's and internal/pluginruntime's) both serve the tool
// category only, so this is a third — deliberately, not by oversight.
//
// The completion it produces is fixed: one text block, one Usage event,
// one Stop. That is enough for the composition root's job to be observable
// end to end (config -> plugin launch -> catalog -> turn -> session ->
// printed final message) without the fixture needing any of a real
// vendor's behavior.
//
// Built entirely on pkg/plugin and pkg/model — the third-party
// plugin-author SDK — matching the other two fixtures. Build-tagged
// integration so it never enters the default `go build ./...`.
package main

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/config"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// The strings the integration test asserts against, kept as consts on
// both sides of the process boundary so a rename is a compile error here
// and a visible edit there, never a silent mismatch.
const (
	// fixtureModelID is the ModelSpec.id an agent_profile's model{} block
	// names.
	fixtureModelID = "fixture-model-1"

	// fixtureAnswer is the entire completion this fixture ever produces.
	// The test asserts it reaches the kernel's stdout verbatim.
	fixtureAnswer = "the composition root works"
)

// fixtureIdentity is what this plugin reports through Describe.
var fixtureIdentity = plugin.Identity{
	Name:    "fixture-model",
	Version: "0.0.0",
	Source:  "internal/kernel/testdata/plugin",
}

// fixtureProvider implements model.Provider: the three MUST RPCs and
// nothing more. It deliberately does not implement model.TokenCounter, so
// the kernel's own fallback heuristic
// (kernel-callbacks.md#the-fallback-heuristic) is what counts context
// tokens — the path a provider without a real tokenizer actually takes.
type fixtureProvider struct{}

var _ model.Provider = (*fixtureProvider)(nil)

// Capabilities advertises one free model that supports tool use and
// streaming. Free pricing keeps the session's cost ledger at zero, which
// keeps the profile's max_cost_usd bound out of the way of what this test
// is actually about.
func (*fixtureProvider) Capabilities(context.Context) (*model.Capabilities, error) {
	// An empty-but-present ConfigSchema: model.NewCapabilities requires
	// one, and this fixture genuinely takes no configuration.
	configSchema, err := config.Schema()
	if err != nil {
		return nil, err
	}
	return model.NewCapabilities([]model.Spec{{
		ID:                fixtureModelID,
		ContextWindow:     200000,
		MaxOutputTokens:   8192,
		SupportsToolUse:   true,
		SupportsStreaming: true,
		// A model with neither capability leaves both specs at their zero
		// value: no thinking (no controls, nothing to disable) and no
		// caching (neither mechanism declared).
		Thinking: model.ThinkingSpec{},
		Caching:  model.CachingSpec{},
		Pricing:  model.Pricing{Currency: "USD", Free: true},
		SupportedToolChoiceModes: []modelv1.ToolChoiceMode{
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
		},
	}}, configSchema)
}

// Configure accepts anything; this fixture declares no config schema and
// therefore receives no config.
func (*fixtureProvider) Configure(context.Context, *structpb.Struct) error { return nil }

// StreamCompletion emits the one canned completion: text, usage, stop.
//
// It never emits a tool_use block, so every turn's DoneCheck succeeds on
// the first turn and the session completes without touching the plan/apply
// gate. That is the narrowest path that still proves the whole
// composition, which is what this fixture is for — exercising the gate
// belongs in internal/plangate's own tests, against its own fakes.
func (*fixtureProvider) StreamCompletion(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	if err := sink.TextDelta(fixtureAnswer); err != nil {
		return err
	}
	if err := sink.Usage(model.Usage{InputTokens: 12, OutputTokens: 6}); err != nil {
		return err
	}
	return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
}

func main() {
	callback := plugin.NewCallback()
	provider := &fixtureProvider{}

	plugin.Serve(plugin.Config{
		Identity: fixtureIdentity,
		Category: commonv1.Category_CATEGORY_MODEL,
		Callback: callback,
		Services: []plugin.Service{model.NewService(provider, fixtureIdentity, callback)},
	})
}
