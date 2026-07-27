// Command tui runs the reference terminal shell for PluggableHarness Agent.
//
// The shell is a frontend provider: in its finished form the kernel launches it
// as a hashicorp/go-plugin subprocess and drives it over a bidirectional Attach
// stream. That kernel-side attach path does not exist yet, so this binary
// currently runs the shell against a scripted demo source, which is what makes
// the layout, focus model, and keymap reviewable ahead of the wiring.
//
// The terminal is opened directly rather than using stdin/stdout, because under
// go-plugin those streams belong to the handshake and the host's logger. That
// is the real code path, exercised here so it does not need revisiting when the
// bridge lands.
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
)

func main() {
	themeName := flag.String("theme", "dark", "color theme: dark or light")
	step := flag.Duration("step", 120*time.Millisecond, "delay between scripted demo events")
	logLevel := flag.String("log-level", "warn", "log level: debug, info, warn, error")
	flag.Parse()

	if err := run(*themeName, *step, *logLevel); err != nil {
		// Diagnostics go to stderr, never to the painted surface. Under
		// go-plugin the host collects this as structured plugin output.
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
}

func run(themeName string, step time.Duration, logLevel string) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(logLevel),
	})))

	th, ok := theme.ByName(themeName)
	if !ok {
		slog.Warn("unknown theme, falling back", "requested", themeName, "using", th.Name)
	}

	tty, err := openTTY()
	if err != nil {
		// No controlling terminal: the shell degrades to not attaching rather
		// than taking down whatever launched it.
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

	// Alt-screen is declared by the model's View in Bubble Tea v2, not as a
	// program option.
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

// drainOutbox stands in for the Attach stream's writer goroutine. The real
// bridge translates each Action into a ClientEvent and writes it to the stream
// in arrival order, which matters because the kernel processes client events in
// arrival order per session.
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
