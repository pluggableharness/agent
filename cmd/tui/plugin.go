package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/tui/region"
	"github.com/pluggableharness/agent/internal/tui/shell"
	"github.com/pluggableharness/agent/internal/tui/theme"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/frontend"
	"github.com/pluggableharness/agent/pkg/kernel"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// tuiProvider is the FrontendService Provider for the reference TUI.
// Configure opens the TTY, dials the kernel callback channel, creates a
// session, and starts the Bubble Tea loop plus Subscribe/StreamDeltas.
type tuiProvider struct {
	identity plugin.Identity
	callback *plugin.Callback
	theme    string

	mu     sync.Mutex
	cancel context.CancelFunc
}

func (p *tuiProvider) Capabilities(context.Context) (*frontend.Capabilities, error) {
	return frontend.NewCapabilities(nil), nil
}

func (p *tuiProvider) Configure(ctx context.Context, _ *structpb.Struct) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return nil // already configured
	}

	client, err := p.callback.Client(ctx)
	if err != nil {
		return fmt.Errorf("tui: callback client: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		if err := p.runShell(runCtx, client); err != nil {
			slog.Error("tui shell stopped", "error", err)
		}
	}()
	return nil
}

func (p *tuiProvider) runShell(ctx context.Context, client *kernel.Client) error {
	th, _ := theme.ByName(p.theme)

	tty, err := openTTY()
	if err != nil {
		return fmt.Errorf("tui: open terminal: %w", err)
	}
	defer func() { _ = tty.Close() }()

	info, err := client.CreateSession(ctx, &kernelv1.CreateSessionRequest{})
	if err != nil {
		return fmt.Errorf("tui: create session: %w", err)
	}
	sessionID := info.GetSessionId()
	slog.Info("tui: session created", "session_id", sessionID)

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

	go p.drainActions(ctx, client, sessionID, outbox)
	go p.subscribeBus(ctx, client, sessionID, prog.Send)
	go p.streamDeltas(ctx, client, sessionID, prog.Send)
	go p.backfill(ctx, client, sessionID, prog.Send)

	// Initial state snapshot.
	if state, err := client.GetSessionState(ctx, sessionID); err == nil {
		prog.Send(stateToStatus(state))
	}
	if blocks, err := client.ListMetadata(ctx, sessionID); err == nil {
		for _, b := range blocks {
			if msg := metadataToPlace(b); msg != nil {
				prog.Send(*msg)
			}
		}
	}

	_, err = prog.Run()
	return err
}

func (p *tuiProvider) drainActions(ctx context.Context, client *kernel.Client, sessionID string, outbox <-chan shell.Action) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-outbox:
			if err := dispatchAction(ctx, client, sessionID, a); err != nil {
				slog.Warn("dispatch action", "type", fmt.Sprintf("%T", a), "error", err)
			}
		}
	}
}

func dispatchAction(ctx context.Context, client *kernel.Client, sessionID string, a shell.Action) error {
	switch v := a.(type) {
	case shell.SubmitPrompt:
		_, err := client.SubmitInput(ctx, sessionID, []*contentv1.ContentBlock{
			{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: v.Text}}},
		})
		return err
	case shell.Decision:
		dec := planv1.ClientDecision_CLIENT_DECISION_DENY
		if v.Allow {
			dec = planv1.ClientDecision_CLIENT_DECISION_ALLOW
		}
		scope := planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE
		switch v.Scope {
		case shell.ScopeSession:
			scope = planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION
		case shell.ScopeAlways:
			scope = planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS
		}
		return client.ResolvePlanDecisionArgs(ctx, sessionID, v.ItemID, dec, scope, nil)
	case shell.Trigger:
		return client.TriggerAction(ctx, &kernelv1.TriggerActionRequest{
			SessionId: sessionID,
			NodeId:    v.NodeID,
			ToolName:  v.ToolName,
			Provider:  v.Provider,
			Args:      v.Args,
		})
	case shell.Interrupt:
		return client.Interrupt(ctx, sessionID)
	default:
		return nil
	}
}

func (p *tuiProvider) subscribeBus(ctx context.Context, client *kernel.Client, sessionID string, send func(tea.Msg)) {
	sub, err := client.Subscribe(ctx, []string{"kernel.event.*", "kernel.state", "kernel.metadata"}, func(ev *kernelv1.BusEvent) {
		switch ev.GetTopic() {
		case "kernel.state":
			var state sessionv1.SessionState
			if err := proto.Unmarshal(ev.GetPayload(), &state); err != nil {
				return
			}
			if state.GetInfo().GetSessionId() != sessionID {
				return
			}
			send(stateToStatus(&state))
		case "kernel.metadata":
			var block metadatav1.MetadataBlock
			if err := proto.Unmarshal(ev.GetPayload(), &block); err != nil {
				return
			}
			if block.GetSessionId() != sessionID {
				return
			}
			if msg := metadataToPlace(&block); msg != nil {
				send(*msg)
			}
		}
	})
	if err != nil {
		slog.Warn("subscribe failed", "error", err)
		return
	}
	defer sub.Close()
	<-ctx.Done()
}

func (p *tuiProvider) streamDeltas(ctx context.Context, client *kernel.Client, sessionID string, send func(tea.Msg)) {
	err := client.StreamDeltas(ctx, sessionID, func(d *kernelv1.TokenDelta) error {
		send(shell.DeltaMsg{TargetID: d.GetTargetId(), Text: d.GetText()})
		return nil
	})
	if err != nil && ctx.Err() == nil && err != io.EOF {
		slog.Warn("stream deltas ended", "error", err)
	}
}

func (p *tuiProvider) backfill(ctx context.Context, client *kernel.Client, sessionID string, send func(tea.Msg)) {
	sub, err := client.ReadEvents(ctx, &kernelv1.ReadEventsRequest{SessionId: sessionID}, func(ev *kernelv1.StoredEvent) {
		send(shell.NoticeMsg{
			Text:  fmt.Sprintf("backfill %s seq=%d", ev.GetKind().String(), ev.GetSequence()),
			Level: shell.NoticeInfo,
		})
	})
	if err != nil {
		slog.Debug("backfill failed", "error", err)
		return
	}
	// ReadEvents is finite; Close waits for the receive goroutine to exit.
	_ = sub.Close()
}
func stateToStatus(state *sessionv1.SessionState) shell.StatusMsg {
	msg := shell.StatusMsg{
		Session: state.GetInfo().GetSessionId(),
		Status:  state.GetInfo().GetStatus().String(),
	}
	if m := state.GetModel(); m != nil {
		msg.Model = m.GetProvider() + "/" + m.GetId()
	}
	if e := state.GetElapsed(); e != nil {
		msg.Elapsed = e.AsDuration()
	}
	return msg
}

func metadataToPlace(b *metadatav1.MetadataBlock) *shell.PlaceMsg {
	if b == nil || b.GetLiveness() == metadatav1.Liveness_LIVENESS_DISCONNECTED {
		return nil
	}
	text := metadataText(b)
	if text == "" {
		return nil
	}
	tree := &renderv1.RenderTree{
		Root: &renderv1.RenderNode{
			Node: &renderv1.RenderNode_Text{
				Text: &renderv1.TextNode{Content: text},
			},
		},
	}
	producer := region.Producer{Category: "metadata", Name: b.GetId()}
	if p := b.GetProducer(); p != nil {
		producer.Name = p.GetName()
		producer.Category = p.GetCategory().String()
	}
	pri := b.GetPriority()
	return &shell.PlaceMsg{
		Region:   region.Sidebar,
		Tree:     tree,
		Producer: producer,
		Replace:  true,
		Priority: &pri,
	}
}

func metadataText(b *metadatav1.MetadataBlock) string {
	switch body := b.GetBody().(type) {
	case *metadatav1.MetadataBlock_KeyValue:
		return body.KeyValue.GetKey() + ": " + body.KeyValue.GetValue()
	case *metadatav1.MetadataBlock_Status:
		s := body.Status.GetText()
		if d := body.Status.GetDetail(); d != "" {
			s += " — " + d
		}
		return s
	case *metadatav1.MetadataBlock_Progress:
		return body.Progress.GetLabel()
	case *metadatav1.MetadataBlock_ItemList:
		return body.ItemList.GetTitle()
	case *metadatav1.MetadataBlock_Timer:
		if body.Timer.GetLabel() != "" {
			return body.Timer.GetLabel()
		}
		return "timer"
	default:
		return b.GetId()
	}
}
