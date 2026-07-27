package tool_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestServiceGetSchema(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newFakeProvider(newFakeTool("read_file")))

	resp, err := client.GetSchema(t.Context(), &toolv1.GetSchemaRequest{})
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if len(resp.GetTools()) != 1 || resp.GetTools()[0].GetName() != "read_file" {
		t.Errorf("Tools = %v", resp.GetTools())
	}
}

func TestServiceGetSchemaListsEveryTool(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(newFakeTool("file_read"), newFakeTool("file_write"), newFakeTool("file_delete"))
	client := newTestClient(t, p)

	resp, err := client.GetSchema(t.Context(), &toolv1.GetSchemaRequest{})
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}

	got := make([]string, 0, len(resp.GetTools()))
	for _, s := range resp.GetTools() {
		got = append(got, s.GetName())
	}
	want := []string{"file_read", "file_write", "file_delete"}
	if !slices.Equal(got, want) {
		t.Errorf("tool names = %v, want %v (declaration order preserved)", got, want)
	}
}

// TestNewServiceRejectsMalformedToolSets covers the construction-time
// validation NewService performs: every one of these would otherwise become
// a malformed advertisement the kernel only discovers on its first
// GetSchema, or an ambiguous dispatch it discovers mid-turn.
func TestNewServiceRejectsMalformedToolSets(t *testing.T) {
	t.Parallel()

	schemaErr := errors.New("boom")

	tests := []struct {
		name    string
		tools   []tool.Tool
		wantErr error
	}{
		{
			name:    "nil tool",
			tools:   []tool.Tool{nil},
			wantErr: tool.ErrNilTool,
		},
		{
			name:    "schema error",
			tools:   []tool.Tool{&fakeTool{schemaErr: schemaErr}},
			wantErr: schemaErr,
		},
		{
			name:    "nil schema",
			tools:   []tool.Tool{&fakeTool{}},
			wantErr: tool.ErrNilSchema,
		},
		{
			name:    "empty name",
			tools:   []tool.Tool{newFakeTool("")},
			wantErr: tool.ErrEmptyName,
		},
		{
			name:    "duplicate name",
			tools:   []tool.Tool{newFakeTool("file_read"), newFakeTool("file_read")},
			wantErr: tool.ErrDuplicateToolName,
		},
		{
			name: "invalid schema",
			tools: []tool.Tool{&fakeTool{schema: &tool.Schema{
				Name:        "file_read",
				Kind:        tool.KindDataSource,
				Risk:        tool.RiskClassReadOnly,
				Description: "missing both I/O schemas",
			}}},
			wantErr: tool.ErrNilInputSchema,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tool.NewService(newFakeProvider(tt.tools...), plugin.Identity{Name: "fake-tool"}, plugin.NewCallback())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewService() error = %v, want wrapping %v", err, tt.wantErr)
			}
		})
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

	cfg, err := structpb.NewStruct(map[string]any{"root": "/tmp"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(t.Context(), &toolv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if gotConfig["root"] != "/tmp" {
		t.Errorf("Provider.Configure received config = %v", gotConfig)
	}
}

func TestServiceConfigureRejectsWithError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		configureFunc: func(context.Context, map[string]any) error {
			return &tool.Error{Category: tool.ErrorCategoryInvalidArguments, Message: "missing root", Retryable: false}
		},
	}
	client := newTestClient(t, p)

	_, err := client.Configure(t.Context(), &toolv1.ConfigureRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Configure error is not a *status.Status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want %v", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "missing root" {
		t.Errorf("message = %q, want %q", st.Message(), "missing root")
	}
}

func TestServiceConfigureGenericErrorDefaultsInvalidArgument(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		configureFunc: func(context.Context, map[string]any) error { return errors.New("decode failed") },
	}
	client := newTestClient(t, p)

	_, err := client.Configure(t.Context(), &toolv1.ConfigureRequest{})
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

	p := newFakeProvider(invokingTool("read_file", func(_ context.Context, call *tool.Call, stream *tool.Stream) error {
		if call.ToolName != "read_file" {
			t.Errorf("call.ToolName = %q, want %q", call.ToolName, "read_file")
		}
		if err := stream.Send(tool.NewOutputChunkEvent(tool.OutputStreamStdout, []byte("hello"))); err != nil {
			return err
		}
		return stream.Send(tool.NewResultEvent(map[string]any{"ok": true}))
	}))
	client := newTestClient(t, p)

	args, err := structpb.NewStruct(map[string]any{"path": "a.go"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{
		Id:          "call-1",
		ToolName:    "read_file",
		Arguments:   args,
		CallContext: &commonv1.CallContext{SessionId: "s1", TurnId: "t1", WorkingDirectory: "/work"},
	}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	var events []*toolv1.ToolEvent
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

	p := newFakeProvider(invokingTool("op", func(context.Context, *tool.Call, *tool.Stream) error {
		// Simulate a Tool that detects cancellation itself and returns
		// context.Canceled rather than sending a terminal event —
		// README.md#transport--lifecycle: cancellation is normal control
		// flow, never surfaced as an application error.
		return context.Canceled
	}))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "op"}})
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

	p := newFakeProvider(invokingTool("op", func(context.Context, *tool.Call, *tool.Stream) error {
		return errors.New("tool panic recovered")
	}))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "op"}})
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

	client := newTestClient(t, newFakeProvider(newFakeTool("op")))

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: nil})
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

	p := newFakeProvider(invokingTool("op", func(context.Context, *tool.Call, *tool.Stream) error { return nil }))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "op"}})
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

	p := newFakeProvider(invokingTool("op", func(_ context.Context, _ *tool.Call, stream *tool.Stream) error {
		te, err := tool.NewError(tool.ErrorCategoryNotFound, "no such file", false, nil)
		if err != nil {
			return err
		}
		return stream.Send(tool.NewErrorEvent(te))
	}))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "op"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if resp.GetEvent().GetError() == nil || resp.GetEvent().GetError().GetMessage() != "no such file" {
		t.Errorf("event = %v, want error \"no such file\"", resp.GetEvent())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("stream.Recv() after terminal error event = %v, want io.EOF", err)
	}
}

func TestServiceDescribe(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newFakeProvider())

	resp, err := client.Describe(t.Context(), &toolv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != "fake-tool" || producer.GetVersion() != "0.0.1" {
		t.Errorf("producer = %v, want name=fake-tool version=0.0.1", producer)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_TOOL {
		t.Errorf("producer.Category = %v, want CATEGORY_TOOL", producer.GetCategory())
	}
}

func TestServiceRenderUnimplementedWithoutRenderer(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newFakeProvider())

	_, err := client.Render(t.Context(), &toolv1.RenderRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Render() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}

// TestServicePreviewIsPerTool exercises protocol.md#preview's MAY at the
// granularity it is actually written: one operation previewing while its
// sibling in the same plugin does not.
func TestServicePreviewIsPerTool(t *testing.T) {
	t.Parallel()

	previewed := previewingTool("edit_file", func(_ context.Context, call *tool.Call) (*renderv1.RenderTree, error) {
		if call.ToolName != "edit_file" {
			t.Errorf("Preview call.ToolName = %q, want %q", call.ToolName, "edit_file")
		}
		return &renderv1.RenderTree{Root: &renderv1.RenderNode{}}, nil
	})
	client := newTestClient(t, newFakeProvider(previewed, newFakeTool("read_file")))

	if _, err := client.Preview(t.Context(), &toolv1.PreviewRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "edit_file"}}); err != nil {
		t.Fatalf("Preview(edit_file): %v", err)
	}

	_, err := client.Preview(t.Context(), &toolv1.PreviewRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "read_file"}})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Preview(read_file) code = %v, want %v (that tool does not implement Previewer)", status.Code(err), codes.Unimplemented)
	}
}

func TestServiceRender(t *testing.T) {
	t.Parallel()

	p := &fakeFullProvider{
		fakeProvider: newFakeProvider(newFakeTool("edit_file")),
		renderFunc: func(_ context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
			if string(payload) != "raw" || schemaVersion != "v1" {
				t.Errorf("Render(%q, %q)", payload, schemaVersion)
			}
			return &renderv1.RenderTree{Root: &renderv1.RenderNode{}}, nil
		},
	}
	client := newTestClient(t, p)

	if _, err := client.Render(t.Context(), &toolv1.RenderRequest{Payload: []byte("raw"), SchemaVersion: "v1"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

// TestServiceDispatchesToNamedTool is the whole point of the Tool-shaped
// SDK: three tools in one provider, each reached by name, with none of them
// switching on Call.ToolName themselves.
func TestServiceDispatchesToNamedTool(t *testing.T) {
	t.Parallel()

	invoked := make(chan string, 3)
	record := func(name string) *fakeTool {
		return invokingTool(name, func(_ context.Context, _ *tool.Call, stream *tool.Stream) error {
			invoked <- name
			return stream.Send(tool.NewResultEvent(map[string]any{"tool": name}))
		})
	}
	client := newTestClient(t, newFakeProvider(record("file_read"), record("file_write"), record("file_delete")))

	for _, want := range []string{"file_delete", "file_read", "file_write"} {
		t.Run(want, func(t *testing.T) {
			stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: want}})
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatalf("stream.Recv: %v", err)
			}
			if got := resp.GetEvent().GetResult().GetPayload().AsMap()["tool"]; got != want {
				t.Errorf("result payload tool = %v, want %q", got, want)
			}
			if got := <-invoked; got != want {
				t.Errorf("Invoke reached tool %q, want %q", got, want)
			}
		})
	}
}

func TestServiceInvokeUnknownToolRejected(t *testing.T) {
	t.Parallel()

	reached := false
	p := newFakeProvider(invokingTool("file_read", func(_ context.Context, _ *tool.Call, stream *tool.Stream) error {
		reached = true
		return stream.Send(tool.NewResultEvent(nil))
	}))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "file_teleport"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if _, err = stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("stream.Recv() code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if reached {
		t.Error("a call naming an unknown tool reached a Tool implementation")
	}
}

func TestServicePreviewUnknownToolRejected(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newFakeProvider(newFakeTool("file_read")))

	_, err := client.Preview(t.Context(), &toolv1.PreviewRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "file_teleport"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Preview() code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestContextWithCallbackRoundTrip(t *testing.T) {
	t.Parallel()

	cb := plugin.NewCallback()
	ctx := tool.ContextWithCallback(t.Context(), cb)

	got, ok := tool.CallbackFromContext(ctx)
	if !ok || got != cb {
		t.Errorf("CallbackFromContext = (%v, %v), want (%v, true)", got, ok, cb)
	}

	if _, ok := tool.CallbackFromContext(t.Context()); ok {
		t.Error("CallbackFromContext on a plain context: want ok=false")
	}
}

func TestServiceInvokeSeesCallback(t *testing.T) {
	t.Parallel()

	var sawCallback bool
	p := newFakeProvider(invokingTool("op", func(ctx context.Context, _ *tool.Call, stream *tool.Stream) error {
		_, sawCallback = tool.CallbackFromContext(ctx)
		return stream.Send(tool.NewResultEvent(nil))
	}))
	client := newTestClient(t, p)

	stream, err := client.Invoke(t.Context(), &toolv1.InvokeRequest{Call: &toolv1.ToolCall{Id: "c", ToolName: "op"}})
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
