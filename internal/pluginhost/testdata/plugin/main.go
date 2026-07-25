//go:build integration

// Command plugin is the fixture internal/pluginhost's integration tier
// (supervisor_integration_test.go) builds and launches as a real
// subprocess.
//
// It exists to exercise the whole per-provider bring-up sequence against
// something real: Describe, a GetSchema that advertises a ConfigSchema
// worth decoding, and a Configure that — crucially — calls back into
// KernelCallbackService.GetConfig from inside its own handler. That last
// part is what proves internal/pluginhost installs a plugin's decoded
// config into its callback slot BEFORE issuing Configure, which
// kernel-callbacks.md permits a plugin to rely on.
//
// It is built entirely on pkg/plugin, pkg/tool, and pkg/config — the
// third-party plugin-author SDK — rather than a hand-rolled
// hashicorp/go-plugin adapter, matching internal/pluginruntime's own
// fixture. It serves the tool category only, which is deliberate: the
// dev-override category probe tries model first, so a tool-only binary
// is what proves the probe skips a category the plugin does not serve
// rather than latching onto the first one it tries.
//
// Build-tagged integration so it never enters the default
// `go build ./...` (which already skips testdata/ regardless).
package main

import (
	"context"
	"fmt"
	"log/slog"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	"github.com/pluggableharness/agent/pkg/schema"
	"github.com/pluggableharness/agent/pkg/tool"
)

// The strings the integration test asserts against. Kept as consts on
// both sides of the process boundary so a rename is a compile error in
// the fixture and a visible edit in the test, never a silent mismatch.
const (
	// fixtureConfigAttr is the single declared config attribute, so the
	// test can put a value in a provider{} block and see it arrive.
	fixtureConfigAttr = "greeting"

	// configureLogMessage is logged by Configure once it has confirmed
	// the value it received matches what GetConfig answers.
	configureLogMessage = "fixture configure saw its own config"

	// configMismatchLogMessage is logged instead when they disagree — a
	// distinct message so the test fails on the right thing rather than
	// timing out on the absence of the success message.
	configMismatchLogMessage = "fixture configure config mismatch"
)

// The identity this fixture reports through Describe, overridable at
// build time with -ldflags "-X main.fixtureName=...". It is deliberately
// NOT read from the environment: internal/pluginruntime launches every
// subprocess under a minimal PATH/HOME/TMPDIR allowlist and never
// inherits the kernel's own environment (its CLAUDE.md's env-allowlist
// decision), so an env var set by a test would never reach this process.
// Build-time linker flags are how the integration tier produces two
// binaries with distinct published identities from one source file.
var (
	fixtureName    = "fixture"
	fixtureVersion = "1.0.0"
	fixtureSource  = "github.com/agentco/pluginhost-fixture"
)

// fixtureProvider implements tool.Provider plus the optional
// tool.ConfigSchemaProvider.
type fixtureProvider struct {
	callback *plugin.Callback
}

var (
	_ tool.Provider             = (*fixtureProvider)(nil)
	_ tool.ConfigSchemaProvider = (*fixtureProvider)(nil)
)

// ConfigSchema advertises one optional string attribute — enough for
// internal/config.DecodeProviderConfig to have real work to do.
func (p *fixtureProvider) ConfigSchema() (*configv1.ConfigSchema, error) {
	attr, err := config.Attribute(fixtureConfigAttr, configv1.AttrType_ATTR_TYPE_STRING)
	if err != nil {
		return nil, err
	}
	return config.Schema(attr)
}

// Configure is where this fixture earns its keep: it reads the config it
// was handed, then immediately calls back into GetConfig and compares.
// Both can only agree if the kernel installed the decoded config into
// this plugin's callback slot before issuing Configure.
func (p *fixtureProvider) Configure(ctx context.Context, cfg map[string]any) error {
	client, err := p.callback.Client(ctx)
	if err != nil {
		return fmt.Errorf("fixture: callback client: %w", err)
	}
	logger := slog.New(client.NewSlogHandler())

	fromCallback, err := client.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("fixture: get config: %w", err)
	}

	want, _ := cfg[fixtureConfigAttr].(string)
	got := fromCallback.GetFields()[fixtureConfigAttr].GetStringValue()
	if got != want {
		logger.Info(configMismatchLogMessage, "configure", want, "get_config", got)
		return nil
	}
	logger.Info(configureLogMessage, "value", got)
	return nil
}

// Schema satisfies tool.Provider with one trivial operation.
func (p *fixtureProvider) Schema(context.Context) ([]*tool.Schema, error) {
	empty, err := schema.Object(nil)
	if err != nil {
		return nil, err
	}
	return []*tool.Schema{{
		Name:         "fixture_echo",
		Kind:         tool.KindResource,
		Risk:         tool.RiskClassLow,
		Description:  "internal/pluginhost integration fixture",
		InputSchema:  empty,
		OutputSchema: empty,
		Concurrency:  &tool.ConcurrencySpec{Safe: true},
		Idempotent:   true,
	}}, nil
}

// Invoke is never called by this fixture's tests but must exist to
// satisfy tool.Provider.
func (p *fixtureProvider) Invoke(_ context.Context, call *tool.Call, stream *tool.Stream) error {
	return stream.Send(tool.NewResultEvent(map[string]any{"echo": call.Arguments}))
}

func main() {
	callback := plugin.NewCallback()
	provider := &fixtureProvider{callback: callback}
	id := plugin.Identity{
		Name:    fixtureName,
		Version: fixtureVersion,
		Source:  fixtureSource,
	}

	plugin.Serve(plugin.Config{
		Identity: id,
		Category: commonv1.Category_CATEGORY_TOOL,
		Callback: callback,
		Services: []plugin.Service{tool.NewService(provider, id, callback)},
	})
}
