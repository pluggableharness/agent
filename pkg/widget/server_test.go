package widget_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	structpb "google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
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
	if producer.GetName() != "git-status" || producer.GetVersion() != "1.0.0" {
		t.Errorf("Describe() producer = %+v, want name/version from testIdentity", producer)
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

	want := widget.NewCapabilities(nil, widget.WithSupportedHookPoints(commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL))
	svc := widget.NewService(&fakeProvider{
		getCapabilitiesFunc: func(context.Context) (widget.Capabilities, error) { return want, nil },
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	resp, err := client.GetCapabilities(t.Context(), &widgetv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	caps := resp.GetCapabilities()
	if len(caps.GetSupportedHookPoints()) != 1 || caps.GetSupportedHookPoints()[0] != commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL {
		t.Errorf("GetCapabilities().SupportedHookPoints = %v, want [POST_TOOL_CALL]", caps.GetSupportedHookPoints())
	}
}

func TestService_Configure(t *testing.T) {
	t.Parallel()

	var got *structpb.Struct
	svc := widget.NewService(&fakeProvider{
		configureFunc: func(_ context.Context, config *structpb.Struct) error {
			got = config
			return nil
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	cfg, err := structpb.NewStruct(map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if !got.GetFields()["enabled"].GetBoolValue() {
		t.Errorf("Configure received %v, want enabled=true", got)
	}
}

func TestService_Configure_error(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return widget.RenderFailed("cannot configure")
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	_, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("Configure code = %v, want Internal", status.Code(err))
	}
}

func TestService_Configure_plainError(t *testing.T) {
	t.Parallel()

	svc := widget.NewService(&fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return errors.New("plain boom")
		},
	}, testIdentity, plugin.NewCallback())
	client := newTestClient(t, svc)

	_, err := client.Configure(t.Context(), &widgetv1.ConfigureRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("Configure code = %v, want Internal for plain error", status.Code(err))
	}
}
