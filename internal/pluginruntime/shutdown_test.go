package pluginruntime

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/hashicorp/go-plugin"

	"github.com/pluggableharness/agent/pkg/common"
)

func TestCloseWithKill_returnsAsSoonAsKillFinishes(t *testing.T) {
	t.Parallel()

	var killed bool
	killFn := func() { killed = true }
	var canceled bool
	cancelLaunch := func() { canceled = true }

	start := time.Now()
	err := closeWithKill(context.Background(), killFn, cancelLaunch, time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("closeWithKill: %v", err)
	}
	if !killed {
		t.Error("killFn was not called")
	}
	if canceled {
		t.Error("cancelLaunch was called despite killFn finishing well inside the drain window")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("closeWithKill took %v, want near-instant (killFn returns immediately)", elapsed)
	}
}

func TestCloseWithKill_escalatesWhenKillOutlivesDrainWindow(t *testing.T) {
	t.Parallel()

	killDone := make(chan struct{})
	killFn := func() {
		<-killDone // simulate a Kill() that outlives the drain window
	}
	canceled := make(chan struct{})
	cancelLaunch := func() {
		close(canceled)
		close(killDone) // let the blocked killFn goroutine finish, as canceling the launch ctx would in practice
	}

	start := time.Now()
	err := closeWithKill(context.Background(), killFn, cancelLaunch, 20*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("closeWithKill: %v", err)
	}
	select {
	case <-canceled:
	default:
		t.Error("cancelLaunch was never called despite killFn outliving the drain window")
	}
	if elapsed < 20*time.Millisecond {
		t.Errorf("closeWithKill took %v, want at least the drain window (20ms)", elapsed)
	}
}

func TestCloseWithKill_respectsCtxDeadlineOverDrainTimeout(t *testing.T) {
	t.Parallel()

	killDone := make(chan struct{})
	killFn := func() { <-killDone }
	cancelLaunch := func() { close(killDone) }

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := closeWithKill(ctx, killFn, cancelLaunch, time.Hour) // drain timeout far longer than ctx's own deadline
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("closeWithKill: %v", err)
	}
	if elapsed >= time.Hour {
		t.Errorf("closeWithKill waited the full drain timeout instead of ctx's tighter deadline")
	}
}

func TestCloseWithKill_nilCancelLaunch(t *testing.T) {
	t.Parallel()

	killDone := make(chan struct{})
	close(killDone) // killFn returns immediately
	killFn := func() { <-killDone }

	if err := closeWithKill(context.Background(), killFn, nil, time.Second); err != nil {
		t.Fatalf("closeWithKill: %v", err)
	}
}

// TestPlugin_Close_neverStartedClient exercises Close against a real
// *plugin.Client that was never started (plugin.NewClient only builds a
// struct — it execs nothing until Client()/Start() is called, confirmed
// by reading go-plugin's own source), so Kill() is a safe, fast no-op.
// This is what lets shutdown.go's public Close method be unit-tested
// against the real hashicorp/go-plugin types without spawning a
// subprocess.
func TestPlugin_Close_neverStartedClient(t *testing.T) {
	t.Parallel()

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  common.Handshake,
		Plugins:          plugin.PluginSet{},
		Cmd:              exec.CommandContext(t.Context(), "true"),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	})

	var canceled bool
	p := &Plugin{client: client, cancelLaunch: func() { canceled = true }}

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if canceled {
		t.Error("cancelLaunch was called for a never-started client's fast no-op Kill()")
	}
}
