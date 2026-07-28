// Command tui runs the reference terminal shell for PluggableHarness Agent.
//
// Default mode is a frontend plugin subprocess (hashicorp/go-plugin): the
// kernel launches it, Configure dials the callback channel, CreateSession
// opens a session, and operator input rides SubmitInput / ResolvePlanDecision /
// Interrupt. There is no Attach stream.
//
// Offline layout review: pass -demo to drive the shell from a scripted source
// without a kernel.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pluggableharness/agent/internal/tui/shell"
	"github.com/pluggableharness/agent/internal/tui/theme"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/frontend"
	"github.com/pluggableharness/agent/pkg/plugin"
)

var (
	pluginName    = "tui"
	pluginVersion = "0.1.0"
	pluginSource  = "github.com/pluggableharness/agent/cmd/tui"
)

func main() {
	demo := flag.Bool("demo", false, "run the offline scripted demo (no kernel)")
	themeName := flag.String("theme", "dark", "color theme: dark or light")
	step := flag.Duration("step", 120*time.Millisecond, "delay between scripted demo events")
	logLevel := flag.String("log-level", "warn", "log level: debug, info, warn, error")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(*logLevel),
	})))

	if *demo {
		if err := runDemo(*themeName, *step); err != nil {
			fmt.Fprintf(os.Stderr, "tui: %v\n", err)
			os.Exit(1)
		}
		return
	}

	identity := plugin.Identity{
		Name:    pluginName,
		Version: pluginVersion,
		Source:  pluginSource,
	}
	callback := plugin.NewCallback()
	provider := &tuiProvider{
		identity: identity,
		callback: callback,
		theme:    *themeName,
	}

	// Serve blocks until go-plugin tears the subprocess down; Close is what
	// gives the shell goroutine Configure started an actual shutdown path.
	defer provider.Close()

	plugin.Serve(plugin.Config{
		Identity: identity,
		Category: commonv1.Category_CATEGORY_FRONTEND,
		Callback: callback,
		Services: []plugin.Service{frontend.NewService(provider, identity, callback)},
	})
}

func runDemo(themeName string, step time.Duration) error {
	th, ok := theme.ByName(themeName)
	if !ok {
		slog.Warn("unknown theme, falling back", "requested", themeName, "using", th.Name)
	}

	tty, err := openTTY()
	if err != nil {
		return fmt.Errorf("tui: open terminal: %w", err)
	}
	defer func() {
		if cerr := tty.Close(); cerr != nil {
			slog.Warn("closing terminal", "error", cerr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	outbox := make(chan shell.Action, 64)
	model := shell.New(
		shell.WithTheme(th),
		shell.WithEmitter(func(a shell.Action) {
			select {
			case outbox <- a:
			default:
				slog.Warn("outbox full, dropping action")
			}
		}),
	)

	prog := tea.NewProgram(model,
		tea.WithContext(ctx),
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)

	go drainOutbox(ctx, outbox)
	go func() {
		src := shell.DemoSource{Step: step}
		if rerr := src.Run(ctx, prog.Send); rerr != nil {
			slog.Error("event source stopped", "error", rerr)
		}
	}()

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: run: %w", err)
	}
	return nil
}

func drainOutbox(ctx context.Context, outbox <-chan shell.Action) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-outbox:
			slog.Debug("client action", "action", fmt.Sprintf("%T", a))
		}
	}
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
