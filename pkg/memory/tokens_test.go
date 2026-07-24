package memory_test

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/memory"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// TestCountTokens_UnconnectedCallback exercises CountTokens' error path: a
// *plugin.Callback that was never handed a broker (i.e. never went through
// plugin.Serve's real subprocess wiring) fails to dial, the same
// documented limitation pkg/kernel.Client.Dial carries — there is no way
// to construct a real *plugin.GRPCBroker from outside hashicorp/go-plugin
// in a unit test. This still exercises CountTokens' own error-wrapping
// path end to end.
func TestCountTokens_UnconnectedCallback(t *testing.T) {
	t.Parallel()

	_, err := memory.CountTokens(t.Context(), plugin.NewCallback(), "claude-x", "hello world")
	if err == nil {
		t.Fatal("CountTokens() error = nil, want an error from the unconnected callback")
	}
}
