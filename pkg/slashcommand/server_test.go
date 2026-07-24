package slashcommand_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	"github.com/pluggableharness/agent/pkg/slashcommand"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
)

func TestServiceGetCapabilities(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) {
			return []*slashcommand.Spec{validSpec("deploy")}, nil
		},
	}
	client := newTestClient(t, p)

	resp, err := client.GetCapabilities(t.Context(), &slashcommandv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if len(resp.GetCommands()) != 1 || resp.GetCommands()[0].GetName() != "deploy" {
		t.Errorf("Commands = %v", resp.GetCommands())
	}
}

func TestServiceGetCapabilitiesError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) { return nil, errors.New("boom") },
	}
	client := newTestClient(t, p)

	_, err := client.GetCapabilities(t.Context(), &slashcommandv1.GetCapabilitiesRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("GetCapabilities error is not a *status.Status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want %v", st.Code(), codes.Internal)
	}
}

func TestServiceConfigure(t *testing.T) {
	t.Parallel()

	var gotConfig map[string]any
	p := &fakeProvider{
		configureFunc: func(_ context.Context, config map[string]any) error {
			gotConfig = config
			return nil
		},
	}
	client := newTestClient(t, p)

	cfg, err := structpb.NewStruct(map[string]any{"channel": "#releases"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(t.Context(), &slashcommandv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if gotConfig["channel"] != "#releases" {
		t.Errorf("Provider.Configure received config = %v", gotConfig)
	}
}

func TestServiceConfigureRejectsWithError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		configureFunc: func(context.Context, map[string]any) error {
			return &tool.Error{Category: tool.ErrorCategoryInvalidArguments, Message: "missing channel", Retryable: false}
		},
	}
	client := newTestClient(t, p)

	_, err := client.Configure(t.Context(), &slashcommandv1.ConfigureRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Configure error is not a *status.Status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want %v", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "missing channel" {
		t.Errorf("message = %q, want %q", st.Message(), "missing channel")
	}
}

func TestServiceConfigureGenericErrorDefaultsInvalidArgument(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		configureFunc: func(context.Context, map[string]any) error { return errors.New("decode failed") },
	}
	client := newTestClient(t, p)

	_, err := client.Configure(t.Context(), &slashcommandv1.ConfigureRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Configure error is not a *status.Status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want %v", st.Code(), codes.InvalidArgument)
	}
}

func TestServiceInvokeStreamsEvents(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		invokeFunc: func(_ context.Context, call *slashcommand.Call, stream *slashcommand.Stream) error {
			if call.Name != "deploy" {
				t.Errorf("call.Name = %q, want %q", call.Name, "deploy")
			}
			if err := stream.Send(slashcommand.NewOutputChunkEvent(tool.OutputStreamStdout, []byte("hello"))); err != nil {
				return err
			}
			return stream.Send(slashcommand.NewResultEvent(map[string]any{"ok": true}))
		},
	}
	client := newTestClient(t, p)

	args, err := structpb.NewStruct(map[string]any{"env": "prod"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{
		Id:          "call-1",
		Name:        "deploy",
		Arguments:   args,
		CallContext: &commonv1.CallContext{SessionId: "s1", TurnId: "t1", WorkingDirectory: "/work"},
	}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	var events []*slashcommandv1.SlashCommandEvent
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		events = append(events, resp.GetEvent())
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].GetOutputChunk() == nil || string(events[0].GetOutputChunk().GetData()) != "hello" {
		t.Errorf("events[0] = %v, want output_chunk %q", events[0], "hello")
	}
	if events[1].GetResult() == nil || !events[1].GetResult().GetPayload().AsMap()["ok"].(bool) {
		t.Errorf("events[1] = %v, want result ok=true", events[1])
	}
}

func TestServiceInvokeCancellationIsNotSurfacedAsError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		invokeFunc: func(context.Context, *slashcommand.Call, *slashcommand.Stream) error {
			// Simulate a Provider that detects cancellation itself and
			// returns context.Canceled rather than sending a terminal
			// event — README.md#transport--lifecycle: cancellation is
			// normal control flow, never surfaced as an application
			// error.
			return context.Canceled
		},
	}
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream.Recv() after a context.Canceled Invoke = %v, want io.EOF (a clean close, not a failed RPC)", err)
	}
}

func TestServiceInvokeGenericErrorMapsToUnknown(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		invokeFunc: func(context.Context, *slashcommand.Call, *slashcommand.Stream) error {
			return errors.New("provider panic recovered")
		},
	}
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	_, err = stream.Recv()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("stream.Recv() error is not a *status.Status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want %v (ErrorCategoryUnknown maps to codes.Internal)", st.Code(), codes.Internal)
	}
}

func TestServiceInvokeInvalidCallRejected(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: nil})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("stream.Recv() code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestServiceInvokeWithoutTerminalEventFails(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		invokeFunc: func(context.Context, *slashcommand.Call, *slashcommand.Stream) error { return nil },
	}
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("stream.Recv(): want an error (provider never sent a terminal event)")
	}
}

func TestServiceInvokeErrorEventTerminates(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		invokeFunc: func(_ context.Context, _ *slashcommand.Call, stream *slashcommand.Stream) error {
			te, err := tool.NewError(tool.ErrorCategoryNotFound, "no such environment", false, nil)
			if err != nil {
				return err
			}
			return stream.Send(slashcommand.NewErrorEvent(te))
		},
	}
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if resp.GetEvent().GetError() == nil || resp.GetEvent().GetError().GetMessage() != "no such environment" {
		t.Errorf("event = %v, want error \"no such environment\"", resp.GetEvent())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("stream.Recv() after terminal error event = %v, want io.EOF", err)
	}
}

func TestServiceDescribe(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})

	resp, err := client.Describe(t.Context(), &slashcommandv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != "fake-slashcommand" || producer.GetVersion() != "0.0.1" {
		t.Errorf("producer = %v, want name=fake-slashcommand version=0.0.1", producer)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_SLASHCOMMAND {
		t.Errorf("producer.Category = %v, want CATEGORY_SLASHCOMMAND", producer.GetCategory())
	}
}

func TestServiceRenderUnimplementedWithoutRenderer(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})

	_, err := client.Render(t.Context(), &slashcommandv1.RenderRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Render() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}

func TestServicePreviewUnimplementedWithoutPreviewer(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})

	_, err := client.Preview(t.Context(), &slashcommandv1.PreviewRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Preview() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}

func TestServiceRenderAndPreview(t *testing.T) {
	t.Parallel()

	base := &fakeProvider{}
	p := &fakeFullProvider{
		fakeProvider: base,
		renderFunc: func(_ context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
			if string(payload) != "raw" || schemaVersion != "v1" {
				t.Errorf("Render(%q, %q)", payload, schemaVersion)
			}
			return &renderv1.RenderTree{Root: &renderv1.RenderNode{}}, nil
		},
		previewFunc: func(_ context.Context, call *slashcommand.Call) (*renderv1.RenderTree, error) {
			if call.Name != "deploy" {
				t.Errorf("Preview call.Name = %q, want %q", call.Name, "deploy")
			}
			return &renderv1.RenderTree{Root: &renderv1.RenderNode{}}, nil
		},
	}
	client := newTestClient(t, p)

	if _, err := client.Render(t.Context(), &slashcommandv1.RenderRequest{Payload: []byte("raw"), SchemaVersion: "v1"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := client.Preview(t.Context(), &slashcommandv1.PreviewRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "deploy"}}); err != nil {
		t.Fatalf("Preview: %v", err)
	}
}

func TestServicePreviewInvalidCallRejected(t *testing.T) {
	t.Parallel()

	p := &fakeFullProvider{
		fakeProvider: &fakeProvider{},
		previewFunc: func(context.Context, *slashcommand.Call) (*renderv1.RenderTree, error) {
			t.Fatal("Preview should not be called for a nil SlashCommandCall")
			return nil, nil
		},
	}
	client := newTestClient(t, p)

	_, err := client.Preview(t.Context(), &slashcommandv1.PreviewRequest{Call: nil})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Preview() code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestContextWithCallbackRoundTrip(t *testing.T) {
	t.Parallel()

	cb := plugin.NewCallback()
	ctx := slashcommand.ContextWithCallback(t.Context(), cb)

	got, ok := slashcommand.CallbackFromContext(ctx)
	if !ok || got != cb {
		t.Errorf("CallbackFromContext = (%v, %v), want (%v, true)", got, ok, cb)
	}

	if _, ok := slashcommand.CallbackFromContext(t.Context()); ok {
		t.Error("CallbackFromContext on a plain context: want ok=false")
	}
}

func TestServiceInvokeSeesCallback(t *testing.T) {
	t.Parallel()

	var sawCallback bool
	p := &fakeProvider{
		invokeFunc: func(ctx context.Context, _ *slashcommand.Call, stream *slashcommand.Stream) error {
			_, sawCallback = slashcommand.CallbackFromContext(ctx)
			return stream.Send(slashcommand.NewResultEvent(nil))
		},
	}
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &slashcommandv1.InvokeRequest{Call: &slashcommandv1.SlashCommandCall{Id: "c", Name: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
	}
	if !sawCallback {
		t.Error("Provider.Invoke's context did not carry the Service's *plugin.Callback")
	}
}
