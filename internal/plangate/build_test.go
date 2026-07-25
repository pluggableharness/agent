package plangate

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
)

func TestBuild_previewPopulation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		kind            toolv1.ToolKind
		supportsPreview bool
		client          *stubToolClient
		wantPreview     bool
		wantCalls       int
	}{
		{
			name:            "resource item with a preview-capable provider",
			kind:            toolv1.ToolKind_TOOL_KIND_RESOURCE,
			supportsPreview: true,
			client:          &stubToolClient{preview: previewTree()},
			wantPreview:     true,
			wantCalls:       1,
		},
		{
			name:            "provider without Preview is an unexceptional absence",
			kind:            toolv1.ToolKind_TOOL_KIND_RESOURCE,
			supportsPreview: false,
			client:          &stubToolClient{preview: previewTree()},
			wantPreview:     false,
			wantCalls:       0,
		},
		{
			name:            "a failing Preview degrades to an absent preview",
			kind:            toolv1.ToolKind_TOOL_KIND_RESOURCE,
			supportsPreview: true,
			client:          &stubToolClient{err: errFake},
			wantPreview:     false,
			wantCalls:       1,
		},
		{
			name:            "data_source items are never previewed",
			kind:            toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
			supportsPreview: true,
			client:          &stubToolClient{preview: previewTree()},
			wantPreview:     false,
			wantCalls:       0,
		},
		{
			name:            "interactive items are never previewed",
			kind:            toolv1.ToolKind_TOOL_KIND_INTERACTIVE,
			supportsPreview: true,
			client:          &stubToolClient{preview: previewTree()},
			wantPreview:     false,
			wantCalls:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newTestGate(t, Config{})
			item := resourceItem("i1", "fs", "write_file")
			item.Kind = tt.kind

			plan, err := g.Build(context.Background(), BuildRequest{
				TurnID: "turn-1",
				Items: []ProvisionalItem{{
					Item:     item,
					Provider: "fs",
					Handle: providercatalog.ToolHandle{
						Provider:        "fs",
						Producer:        &commonv1.ProducerRef{Name: "fs", Version: "1", Category: commonv1.Category_CATEGORY_TOOL},
						SupportsPreview: tt.supportsPreview,
						Client:          tt.client,
					},
				}},
			})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := len(plan.GetItems()); got != 1 {
				t.Fatalf("plan items = %d, want 1", got)
			}
			if gotPreview := plan.GetItems()[0].GetPreview() != nil; gotPreview != tt.wantPreview {
				t.Errorf("preview populated = %t, want %t", gotPreview, tt.wantPreview)
			}
			tt.client.mu.Lock()
			calls := tt.client.calls
			tt.client.mu.Unlock()
			if calls != tt.wantCalls {
				t.Errorf("Preview calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

// A Preview that outlives its deadline must degrade to an absent preview,
// never to an aborted plan — plan-apply-gate.md#preview-flow states that as
// an explicit MUST NOT.
func TestBuild_previewTimeoutDegradesToAbsentPreview(t *testing.T) {
	t.Parallel()

	client := &stubToolClient{preview: previewTree(), delay: time.Minute}
	g := newTestGate(t, Config{}, WithPreviewTimeout(10*time.Millisecond))

	plan, err := g.Build(context.Background(), BuildRequest{
		TurnID: "turn-1",
		Items: []ProvisionalItem{{
			Item:     resourceItem("i1", "fs", "write_file"),
			Provider: "fs",
			Handle:   providercatalog.ToolHandle{SupportsPreview: true, Client: client},
		}},
	})
	if err != nil {
		t.Fatalf("Build after a Preview timeout: %v, want nil (plan construction must still succeed)", err)
	}
	if plan.GetItems()[0].GetPreview() != nil {
		t.Error("preview populated after a timeout, want absent")
	}
}

func TestBuild_carriesItemsForwardByIdentity(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{})
	first := resourceItem("i1", "fs", "write_file")
	second := resourceItem("i2", "http", "post")

	plan, err := g.Build(context.Background(), BuildRequest{
		TurnID: "turn-1",
		Items: []ProvisionalItem{
			{Item: first, Provider: "fs"},
			{Item: second, Provider: "http"},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.GetTurnId() != "turn-1" {
		t.Errorf("turn id = %q, want %q", plan.GetTurnId(), "turn-1")
	}
	if plan.GetItems()[0] != first || plan.GetItems()[1] != second {
		t.Error("Build copied its items; they must be carried forward by identity")
	}
}

func TestBuild_errors(t *testing.T) {
	t.Parallel()

	previewedDataSource := resourceItem("i1", "fs", "read_file")
	previewedDataSource.Kind = toolv1.ToolKind_TOOL_KIND_DATA_SOURCE
	previewedDataSource.Preview = previewTree()

	previewedInteractive := resourceItem("i1", "ui", "ask_user")
	previewedInteractive.Kind = toolv1.ToolKind_TOOL_KIND_INTERACTIVE
	previewedInteractive.Preview = previewTree()

	tests := []struct {
		name string
		req  BuildRequest
		want error
	}{
		{
			name: "missing turn id",
			req:  BuildRequest{Items: []ProvisionalItem{{Item: resourceItem("i1", "fs", "write_file")}}},
			want: ErrNoTurnID,
		},
		{
			name: "nil plan item",
			req:  BuildRequest{TurnID: "turn-1", Items: []ProvisionalItem{{Provider: "fs"}}},
			want: ErrNilItem,
		},
		{
			name: "preview populated on a data_source item",
			req:  BuildRequest{TurnID: "turn-1", Items: []ProvisionalItem{{Item: previewedDataSource, Provider: "fs"}}},
			want: ErrPreviewNotAllowed,
		},
		{
			name: "preview populated on an interactive item",
			req:  BuildRequest{TurnID: "turn-1", Items: []ProvisionalItem{{Item: previewedInteractive, Provider: "ui"}}},
			want: ErrPreviewNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newTestGate(t, Config{})
			plan, err := g.Build(context.Background(), tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Build err = %v, want %v", err, tt.want)
			}
			if plan != nil {
				t.Errorf("Build returned a plan alongside an error: %v", plan)
			}
		})
	}
}

func TestBuild_emptyRequestIsAnEmptyPlan(t *testing.T) {
	t.Parallel()

	g := newTestGate(t, Config{})
	plan, err := g.Build(context.Background(), BuildRequest{TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.GetItems()) != 0 {
		t.Errorf("items = %d, want 0", len(plan.GetItems()))
	}
}
