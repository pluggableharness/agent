package model_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	model "github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// fakeProvider is a hand-written model.Provider (go-testing.md: fakes, not
// mocking frameworks). Each RPC's behavior is controlled by a caller-set
// func field; a nil field falls back to a minimal default. Embedding
// pointers to *bool-style toggles keeps the zero value ("all defaults")
// usable without every test having to populate every field.
type fakeProvider struct {
	capabilitiesFunc     func(ctx context.Context) (*model.Capabilities, error)
	configureFunc        func(ctx context.Context, config *structpb.Struct) error
	streamCompletionFunc func(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error
}

func (f *fakeProvider) Capabilities(ctx context.Context) (*model.Capabilities, error) {
	if f.capabilitiesFunc != nil {
		return f.capabilitiesFunc(ctx)
	}
	return model.NewCapabilities([]model.Spec{{
		ID:       "fake-model",
		Thinking: model.ThinkingSpec{Mode: modelv1.ThinkingMode_THINKING_MODE_NONE},
		Caching:  model.CachingSpec{Mode: modelv1.CachingMode_CACHING_MODE_NONE},
		Pricing:  model.Pricing{Currency: "USD", Free: true},
	}}, &configv1.ConfigSchema{})
}

func (f *fakeProvider) Configure(ctx context.Context, config *structpb.Struct) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, config)
	}
	return nil
}

func (f *fakeProvider) StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	if f.streamCompletionFunc != nil {
		return f.streamCompletionFunc(ctx, req, sink)
	}
	if err := sink.TextDelta("hello"); err != nil {
		return err
	}
	return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
}

var _ model.Provider = (*fakeProvider)(nil)

// fakeTokenCounterProvider embeds fakeProvider and additionally implements
// model.TokenCounter, so server_test.go can exercise both "provider
// implements the optional RPC" and "provider doesn't" without two
// unrelated fake types.
type fakeTokenCounterProvider struct {
	fakeProvider
	countTokensFunc func(ctx context.Context, text, modelID string) (int64, error)
}

func (f *fakeTokenCounterProvider) CountTokens(ctx context.Context, text, modelID string) (int64, error) {
	if f.countTokensFunc != nil {
		return f.countTokensFunc(ctx, text, modelID)
	}
	return int64(len(text)), nil
}

var _ model.TokenCounter = (*fakeTokenCounterProvider)(nil)

// fakeRendererProvider is Render's analog to fakeTokenCounterProvider.
type fakeRendererProvider struct {
	fakeProvider
	renderFunc func(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
}

func (f *fakeRendererProvider) Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
	if f.renderFunc != nil {
		return f.renderFunc(ctx, payload, schemaVersion)
	}
	return &renderv1.RenderTree{}, nil
}

var _ model.Renderer = (*fakeRendererProvider)(nil)

// newTestClient starts a Service wrapping p on an in-memory bufconn
// listener and returns a modelv1.ModelServiceClient dialed against it — a
// real gRPC round trip, not a hand-rolled interface fake, per
// pkg/kernel/helpers_test.go's newTestClient shape.
func newTestClient(t *testing.T, p model.Provider) modelv1.ModelServiceClient {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	svc := model.NewService(p, plugin.Identity{Name: "fake", Version: "1.0.0", Source: "github.com/pluggableharness/agent/pkg/model/testdata"}, plugin.NewCallback())

	gs := grpc.NewServer()
	svc.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return modelv1.NewModelServiceClient(conn)
}

func TestService_Describe(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})
	resp, err := client.Describe(t.Context(), &modelv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe() = %v, want nil error", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != "fake" || producer.GetVersion() != "1.0.0" {
		t.Errorf("producer = %+v, want name=fake version=1.0.0", producer)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_MODEL {
		t.Errorf("producer.Category = %v, want CATEGORY_MODEL", producer.GetCategory())
	}
}

func TestService_GetCapabilities(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})
	resp, err := client.GetCapabilities(t.Context(), &modelv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities() = %v, want nil error", err)
	}
	models := resp.GetCapabilities().GetModels()
	if len(models) != 1 || models[0].GetId() != "fake-model" {
		t.Errorf("models = %+v, want one model fake-model", models)
	}
}

func TestService_GetCapabilities_ProviderError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) (*model.Capabilities, error) {
			return nil, &model.Error{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, Message: "vendor down"}
		},
	}
	client := newTestClient(t, p)
	_, err := client.GetCapabilities(t.Context(), &modelv1.GetCapabilitiesRequest{})
	if grpcstatus.Code(err) != codes.Unavailable {
		t.Errorf("code = %v, want codes.Unavailable", grpcstatus.Code(err))
	}
}

func TestService_Configure(t *testing.T) {
	t.Parallel()

	var gotConfig *structpb.Struct
	p := &fakeProvider{
		configureFunc: func(_ context.Context, config *structpb.Struct) error {
			gotConfig = config
			return nil
		},
	}
	client := newTestClient(t, p)
	cfg, err := structpb.NewStruct(map[string]any{"api_key": "secret"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(t.Context(), &modelv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure() = %v, want nil error", err)
	}
	if gotConfig.GetFields()["api_key"].GetStringValue() != "secret" {
		t.Errorf("provider did not receive the config it was sent")
	}
}

func TestService_Configure_MissingRequiredField(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return &model.Error{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, Message: "missing api_key"}
		},
	}
	client := newTestClient(t, p)
	_, err := client.Configure(t.Context(), &modelv1.ConfigureRequest{Config: &structpb.Struct{}})
	if grpcstatus.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want codes.Unauthenticated", grpcstatus.Code(err))
	}
}

func TestService_StreamCompletion_HappyPath(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})
	stream, err := client.StreamCompletion(t.Context(), &modelv1.StreamCompletionRequest{ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("StreamCompletion() = %v, want nil error", err)
	}

	var events []*modelv1.StreamEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv() = %v, want nil or io.EOF", err)
		}
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].GetTextDelta().GetText() != "hello" {
		t.Errorf("events[0].TextDelta.Text = %q, want %q", events[0].GetTextDelta().GetText(), "hello")
	}
	if events[1].GetStop().GetReason() != modelv1.StopReason_STOP_REASON_END_TURN {
		t.Errorf("events[1].Stop.Reason = %v, want STOP_REASON_END_TURN", events[1].GetStop().GetReason())
	}
}

func TestService_StreamCompletion_InBandError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		streamCompletionFunc: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
			return sink.Error(&model.Error{
				Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED,
				Message:  "conversation too long",
			})
		},
	}
	client := newTestClient(t, p)
	stream, err := client.StreamCompletion(t.Context(), &modelv1.StreamCompletionRequest{ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("StreamCompletion() = %v, want nil error", err)
	}
	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv() = %v, want nil", err)
	}
	if ev.GetError().GetError().GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED {
		t.Errorf("category = %v, want MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED", ev.GetError().GetError().GetCategory())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("stream.Recv() after the in-band error = %v, want io.EOF", err)
	}
}

func TestService_StreamCompletion_ProviderReturnsModelError(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		streamCompletionFunc: func(context.Context, *modelv1.StreamCompletionRequest, *model.Sink) error {
			return &model.Error{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, Message: "malformed request"}
		},
	}
	client := newTestClient(t, p)
	stream, err := client.StreamCompletion(t.Context(), &modelv1.StreamCompletionRequest{ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("StreamCompletion() = %v, want nil error", err)
	}
	_, err = stream.Recv()
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want codes.InvalidArgument", grpcstatus.Code(err))
	}
}

// TestService_StreamCompletion_Cancellation exercises the streaming
// cancellation path explicitly, per the task brief: a Provider.StreamCompletion
// that blocks until ctx.Done() and the test cancels the client-side
// context, confirming codes.Canceled propagates cleanly, not as an
// internal error.
func TestService_StreamCompletion_Cancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	p := &fakeProvider{
		streamCompletionFunc: func(ctx context.Context, _ *modelv1.StreamCompletionRequest, _ *model.Sink) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := newTestClient(t, p)

	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.StreamCompletion(ctx, &modelv1.StreamCompletionRequest{ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("StreamCompletion() = %v, want nil error", err)
	}

	go func() {
		<-started
		cancel()
	}()

	_, err = stream.Recv()
	if grpcstatus.Code(err) != codes.Canceled {
		t.Errorf("code = %v, want codes.Canceled", grpcstatus.Code(err))
	}
}

// TestService_StreamCompletion_CapabilityGatedContentRejection exercises
// docs/specifications/model/data-types.md#canonical-message--content-block-schema's
// rule that image/document content sent to a model without the matching
// capability flag MUST be rejected with a structured error, not silently
// dropped or a panic — driven through a fakeProvider that performs exactly
// this check against its own advertised Spec before generating,
// mirroring what a real adapter is expected to do.
func TestService_StreamCompletion_CapabilityGatedContentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content *contentv1.ContentBlock
	}{
		{
			name: "image without supports_vision",
			content: &contentv1.ContentBlock{
				Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{Data: []byte("png"), MediaType: "image/png"}},
			},
		},
		{
			name: "document without supports_documents",
			content: &contentv1.ContentBlock{
				Block: &contentv1.ContentBlock_Document{Document: &contentv1.DocumentBlock{Data: []byte("pdf"), MediaType: "application/pdf"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := &fakeProvider{
				streamCompletionFunc: func(_ context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
					for _, msg := range req.GetMessages() {
						for _, block := range msg.GetContent() {
							if block.GetImage() != nil {
								return sink.Error(&model.Error{
									Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
									Message:  "model does not support image content",
								})
							}
							if block.GetDocument() != nil {
								return sink.Error(&model.Error{
									Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
									Message:  "model does not support document content",
								})
							}
						}
					}
					return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
				},
			}
			client := newTestClient(t, p)
			req := &modelv1.StreamCompletionRequest{
				ModelId: "fake-model",
				Messages: []*contentv1.Message{
					{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{tt.content}},
				},
			}
			stream, err := client.StreamCompletion(t.Context(), req)
			if err != nil {
				t.Fatalf("StreamCompletion() = %v, want nil error", err)
			}
			ev, err := stream.Recv()
			if err != nil {
				t.Fatalf("stream.Recv() = %v, want nil", err)
			}
			if ev.GetError() == nil {
				t.Fatalf("event = %+v, want an in-band error event, not a silent drop", ev)
			}
			if ev.GetError().GetError().GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
				t.Errorf("category = %v, want MODEL_ERROR_CATEGORY_INVALID_REQUEST", ev.GetError().GetError().GetCategory())
			}
		})
	}
}

func TestService_CountTokens_Implemented(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeTokenCounterProvider{})
	resp, err := client.CountTokens(t.Context(), &modelv1.CountTokensRequest{Text: "hello world", ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("CountTokens() = %v, want nil error", err)
	}
	if resp.GetCount() != int64(len("hello world")) {
		t.Errorf("Count = %d, want %d", resp.GetCount(), len("hello world"))
	}
}

func TestService_CountTokens_NotImplemented(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})
	_, err := client.CountTokens(t.Context(), &modelv1.CountTokensRequest{Text: "hi", ModelId: "fake-model"})
	if grpcstatus.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want codes.Unimplemented", grpcstatus.Code(err))
	}
}

func TestService_CountTokens_ProviderError(t *testing.T) {
	t.Parallel()

	p := &fakeTokenCounterProvider{
		countTokensFunc: func(context.Context, string, string) (int64, error) {
			return 0, &model.Error{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, Message: "unknown model"}
		},
	}
	client := newTestClient(t, p)
	_, err := client.CountTokens(t.Context(), &modelv1.CountTokensRequest{Text: "hi", ModelId: "nope"})
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want codes.InvalidArgument", grpcstatus.Code(err))
	}
}

func TestService_Render_Implemented(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeRendererProvider{})
	resp, err := client.Render(t.Context(), &modelv1.RenderRequest{Payload: []byte("{}"), SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("Render() = %v, want nil error", err)
	}
	if resp.GetTree() == nil {
		t.Errorf("Tree = nil, want a RenderTree")
	}
}

func TestService_Render_NotImplemented(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &fakeProvider{})
	_, err := client.Render(t.Context(), &modelv1.RenderRequest{Payload: []byte("{}"), SchemaVersion: "v1"})
	if grpcstatus.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want codes.Unimplemented", grpcstatus.Code(err))
	}
}

func TestService_Render_ProviderError(t *testing.T) {
	t.Parallel()

	p := &fakeRendererProvider{
		renderFunc: func(context.Context, []byte, string) (*renderv1.RenderTree, error) {
			return nil, errors.New("unknown schema version")
		},
	}
	client := newTestClient(t, p)
	_, err := client.Render(t.Context(), &modelv1.RenderRequest{Payload: []byte("{}"), SchemaVersion: "v999"})
	if grpcstatus.Code(err) != codes.Internal {
		t.Errorf("code = %v, want codes.Internal", grpcstatus.Code(err))
	}
}

// TestService_StreamCompletion_NonStreamingBackend exercises
// docs/specifications/model/protocol.md#streamcompletion's rule that a
// batch-only backend still implements the streaming RPC shape, emitting
// its full response as a single terminal burst of events followed by a
// Stop — modeled here as a Provider that sends every event before
// returning, with no incremental delay.
func TestService_StreamCompletion_NonStreamingBackend(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		streamCompletionFunc: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
			if err := sink.TextDelta("the whole response at once"); err != nil {
				return err
			}
			one := int64(1)
			if err := sink.Usage(model.Usage{InputTokens: 10, OutputTokens: 6, ReasoningTokens: &one}); err != nil {
				return err
			}
			return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
		},
	}
	client := newTestClient(t, p)
	stream, err := client.StreamCompletion(t.Context(), &modelv1.StreamCompletionRequest{ModelId: "fake-model"})
	if err != nil {
		t.Fatalf("StreamCompletion() = %v, want nil error", err)
	}

	var events []*modelv1.StreamEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv() = %v, want nil or io.EOF", err)
		}
		events = append(events, ev)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3 (text_delta, usage, stop)", len(events))
	}
	if events[2].GetStop() == nil {
		t.Errorf("final event = %+v, want a Stop", events[2])
	}
}

func TestNewService(t *testing.T) {
	t.Parallel()

	svc := model.NewService(&fakeProvider{}, plugin.Identity{Name: "x"}, plugin.NewCallback())
	if svc == nil {
		t.Fatal("NewService() = nil")
	}
}
