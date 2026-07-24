package widget_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	structpb "google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	"github.com/pluggableharness/agent/pkg/widget"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

var testIdentity = plugin.Identity{
	Name:    "git-status",
	Version: "1.0.0",
	Source:  "github.com/agentco/git-status-widget",
}

func TestService_Describe(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	resp, err := client.Describe(t.Context(), &widgetv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != "git-status" || producer.GetVersion() != "1.0.0" || producer.GetSource() != "github.com/agentco/git-status-widget" {
		t.Errorf("Describe() producer = %+v, want name/version/source from testIdentity", producer)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_WIDGET {
		t.Errorf("Describe() producer.Category = %v, want CATEGORY_WIDGET", producer.GetCategory())
	}
}

func TestService_Callback(t *testing.T) {
	t.Parallel()

	cb := plugin.NewCallback()
	svc := widget.NewService(&fakeProvider{}, testIdentity, cb)
	if got := svc.Callback(); got != cb {
		t.Errorf("Callback() = %p, want %p", got, cb)
	}
}

func TestService_Register(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{}, testIdentity, plugin.NewCallback())
	gs := grpc.NewServer()
	t.Cleanup(gs.Stop)

	svc.Register(gs)

	if _, ok := gs.GetServiceInfo()["pluggableharness.widget.v1.WidgetService"]; !ok {
		t.Error("Register: WidgetService not registered on server")
	}
}

func TestService_GetCapabilities(t *testing.T) {
	t.Parallel()

	want := widget.NewCapabilities(nil, []renderv1.Region{renderv1.Region_REGION_SIDEBAR}, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL)
	svc := widget.NewService(&fakeProvider{
		getCapabilitiesFunc: func(context.Context) (widget.Capabilities, error) { return want, nil },
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	resp, err := client.GetCapabilities(t.Context(), &widgetv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	caps := resp.GetCapabilities()
	if len(caps.GetRegions()) != 1 || caps.GetRegions()[0] != renderv1.Region_REGION_SIDEBAR {
		t.Errorf("GetCapabilities().Regions = %v, want [REGION_SIDEBAR]", caps.GetRegions())
	}
	if len(caps.GetSupportedHookPoints()) != 1 || caps.GetSupportedHookPoints()[0] != commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL {
		t.Errorf("GetCapabilities().SupportedHookPoints = %v, want [HOOK_POINT_POST_TOOL_CALL]", caps.GetSupportedHookPoints())
	}
}

func TestService_GetCapabilities_error(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		getCapabilitiesFunc: func(context.Context) (widget.Capabilities, error) {
			return widget.Capabilities{}, widget.RenderFailed("cannot build schema")
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	_, err := client.GetCapabilities(t.Context(), &widgetv1.GetCapabilitiesRequest{})
	assertWidgetStatus(t, err, codes.Internal, widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED, "cannot build schema")
}

func TestService_Configure_success(t *testing.T) {
	t.Parallel()

	var gotConfig bool
	svc := widget.NewService(&fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error { gotConfig = true; return nil },
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	if _, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if !gotConfig {
		t.Error("Configure: Provider.Configure was not called")
	}
}

func TestService_Configure_regionUnsupportedError(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return widget.RegionUnsupported("this widget has no sidebar support")
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	_, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{})
	assertWidgetStatus(t, err, codes.InvalidArgument, widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_REGION_UNSUPPORTED, "this widget has no sidebar support")
}

func TestService_Configure_plainErrorMapsToUnknown(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error { return errors.New("malformed config") },
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	_, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{})
	assertWidgetStatus(t, err, codes.Internal, widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_UNKNOWN, "malformed config")
}

func TestService_Attach_pushesUpdatesWithSessionID(t *testing.T) {
	t.Parallel()

	var gotSessionID string
	svc := widget.NewService(&fakeProvider{
		attachFunc: func(_ context.Context, req widget.AttachRequest, sender *widget.UpdateSender) error {
			gotSessionID = req.SessionID
			if err := sender.Send(widget.Update{
				Region:  renderv1.Region_REGION_SIDEBAR,
				Content: render.Tree(render.Text("first")),
				Mode:    widget.UpdateAppend,
			}); err != nil {
				return err
			}
			return sender.Send(widget.Update{
				Region:  renderv1.Region_REGION_TOP_BAR,
				Content: render.Tree(render.Text("second")),
				Mode:    widget.UpdateReplace,
			})
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	stream, err := client.Attach(t.Context(), &widgetv1.AttachRequest{SessionId: "session-01"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv (first): %v", err)
	}
	if first.GetRegion() != renderv1.Region_REGION_SIDEBAR || first.GetReplace() {
		t.Errorf("first update = region=%v replace=%v, want region=REGION_SIDEBAR replace=false (append)", first.GetRegion(), first.GetReplace())
	}

	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv (second): %v", err)
	}
	if second.GetRegion() != renderv1.Region_REGION_TOP_BAR || !second.GetReplace() {
		t.Errorf("second update = region=%v replace=%v, want region=REGION_TOP_BAR replace=true", second.GetRegion(), second.GetReplace())
	}

	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("Recv (third): err = %v, want io.EOF", err)
	}
	if gotSessionID != "session-01" {
		t.Errorf("Provider.Attach saw SessionID = %q, want session-01", gotSessionID)
	}
}

func TestService_Attach_error(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		attachFunc: func(context.Context, widget.AttachRequest, *widget.UpdateSender) error {
			return widget.RegionUnsupported("no overlay support")
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	stream, err := client.Attach(t.Context(), &widgetv1.AttachRequest{SessionId: "session-01"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	_, err = stream.Recv()
	assertWidgetStatus(t, err, codes.InvalidArgument, widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_REGION_UNSUPPORTED, "no overlay support")
}

// TestService_Attach_clientCancelIsCleanShutdown exercises
// widget-protocol.md#transport's cancellation discipline: the kernel
// (here, the test client) closing the Attach stream mid-flight MUST be
// treated as normal control flow by both UpdateSender.Send and the
// Provider, never as an application error to report.
func TestService_Attach_clientCancelIsCleanShutdown(t *testing.T) {
	t.Parallel()

	providerDone := make(chan struct{})
	var sendAfterCancelErr error

	svc := widget.NewService(&fakeProvider{
		attachFunc: func(ctx context.Context, _ widget.AttachRequest, sender *widget.UpdateSender) error {
			defer close(providerDone)
			if err := sender.Send(widget.Update{
				Region:  renderv1.Region_REGION_SIDEBAR,
				Content: render.Tree(render.Text("hello")),
			}); err != nil {
				return err
			}
			<-ctx.Done()
			sendAfterCancelErr = sender.Send(widget.Update{
				Region:  renderv1.Region_REGION_SIDEBAR,
				Content: render.Tree(render.Text("after cancel")),
			})
			return ctx.Err()
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.Attach(ctx, &widgetv1.AttachRequest{SessionId: "session-01"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}

	cancel()

	select {
	case <-providerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Provider.Attach did not observe cancellation within 5s")
	}

	if !errors.Is(sendAfterCancelErr, context.Canceled) {
		t.Errorf("Send after cancel = %v, want context.Canceled", sendAfterCancelErr)
	}
}

// assertWidgetStatus asserts err is a gRPC status with code wantCode
// carrying a *widget.Error with category wantCategory and message
// wantMessage, recovered via widget.FromStatus.
func assertWidgetStatus(t *testing.T, err error, wantCode codes.Code, wantCategory widgetv1.WidgetErrorCategory, wantMessage string) {
	t.Helper()

	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := status.Code(err); got != wantCode {
		t.Errorf("status.Code(err) = %v, want %v", got, wantCode)
	}
	werr, ok := widget.FromStatus(err)
	if !ok {
		t.Fatalf("FromStatus(%v) ok = false, want true", err)
	}
	if werr.Category != wantCategory {
		t.Errorf("FromStatus(err).Category = %v, want %v", werr.Category, wantCategory)
	}
	if werr.Message != wantMessage {
		t.Errorf("FromStatus(err).Message = %q, want %q", werr.Message, wantMessage)
	}
}
