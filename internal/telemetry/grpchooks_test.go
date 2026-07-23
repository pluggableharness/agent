package telemetry_test

import (
	"testing"

	"github.com/pluggableharness/agent/internal/telemetry"
)

func TestProvider_grpcHandlers(t *testing.T) {
	t.Parallel()
	p, _ := newTestProvider(t)

	if h := p.ClientHandler(); h == nil {
		t.Error("ClientHandler() = nil")
	}
	if h := p.ServerHandler(); h == nil {
		t.Error("ServerHandler() = nil")
	}
}

func TestResourceEnv(t *testing.T) {
	t.Parallel()

	got := telemetry.ResourceEnv("tool", "ripgrep", "1.2.3")
	want := "pluggableharness.agent.producer.category=tool,pluggableharness.agent.producer.name=ripgrep,pluggableharness.agent.producer.version=1.2.3"
	if got != want {
		t.Errorf("ResourceEnv = %q, want %q", got, want)
	}
}

func TestResourceEnv_escapesSpecialCharacters(t *testing.T) {
	t.Parallel()

	got := telemetry.ResourceEnv("tool", "my tool", "1.0.0,beta")
	want := "pluggableharness.agent.producer.category=tool,pluggableharness.agent.producer.name=my+tool,pluggableharness.agent.producer.version=1.0.0%2Cbeta"
	if got != want {
		t.Errorf("ResourceEnv = %q, want %q", got, want)
	}
}
