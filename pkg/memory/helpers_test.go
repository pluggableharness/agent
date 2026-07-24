package memory_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/content"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/memory"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// textBlocks builds the single-element []*ContentBlock a wire request
// carries for text s, mirroring convert.go's contentToProto.
func textBlocks(s string) []*contentv1.ContentBlock {
	return []*contentv1.ContentBlock{content.Text(s)}
}

// imageBlocks builds a non-text []*ContentBlock, used to exercise
// contentFromProto's text-only-in-v1 rejection.
func imageBlocks() []*contentv1.ContentBlock {
	return []*contentv1.ContentBlock{content.Image([]byte("fake-image-bytes"), "image/png")}
}

// fakeProvider is a hand-written memory.Provider fake (go-testing.md:
// fakes, not mocking frameworks, per repo convention). Each method's
// behavior is controlled by a caller-set func field; a nil field returns
// its type's zero value and a nil error.
type fakeProvider struct {
	capabilitiesFunc func(context.Context) (memory.Capabilities, error)
	configureFunc    func(context.Context, *structpb.Struct) error
	recallFunc       func(context.Context, memory.RecallRequest) (memory.RecallResult, error)
	recordFunc       func(context.Context, memory.RecordRequest) (memory.RecordResult, error)
	updateRecordFunc func(context.Context, memory.UpdateRecordRequest) (memory.RecordResult, error)
	deleteRecordFunc func(context.Context, string) (memory.DeleteResult, error)
	listRecordsFunc  func(context.Context, memory.ListRecordsRequest) (memory.ListRecordsResult, error)
	getRecordFunc    func(context.Context, string) (memory.Record, error)
}

func (f *fakeProvider) Capabilities(ctx context.Context) (memory.Capabilities, error) {
	if f.capabilitiesFunc != nil {
		return f.capabilitiesFunc(ctx)
	}
	return memory.Capabilities{}, nil
}

func (f *fakeProvider) Configure(ctx context.Context, cfg *structpb.Struct) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, cfg)
	}
	return nil
}

func (f *fakeProvider) Recall(ctx context.Context, req memory.RecallRequest) (memory.RecallResult, error) {
	if f.recallFunc != nil {
		return f.recallFunc(ctx, req)
	}
	return memory.RecallResult{}, nil
}

func (f *fakeProvider) Record(ctx context.Context, req memory.RecordRequest) (memory.RecordResult, error) {
	if f.recordFunc != nil {
		return f.recordFunc(ctx, req)
	}
	return memory.RecordResult{}, nil
}

func (f *fakeProvider) UpdateRecord(ctx context.Context, req memory.UpdateRecordRequest) (memory.RecordResult, error) {
	if f.updateRecordFunc != nil {
		return f.updateRecordFunc(ctx, req)
	}
	return memory.RecordResult{}, nil
}

func (f *fakeProvider) DeleteRecord(ctx context.Context, id string) (memory.DeleteResult, error) {
	if f.deleteRecordFunc != nil {
		return f.deleteRecordFunc(ctx, id)
	}
	return memory.DeleteResult{}, nil
}

func (f *fakeProvider) ListRecords(ctx context.Context, req memory.ListRecordsRequest) (memory.ListRecordsResult, error) {
	if f.listRecordsFunc != nil {
		return f.listRecordsFunc(ctx, req)
	}
	return memory.ListRecordsResult{}, nil
}

func (f *fakeProvider) GetRecord(ctx context.Context, id string) (memory.Record, error) {
	if f.getRecordFunc != nil {
		return f.getRecordFunc(ctx, id)
	}
	return memory.Record{}, nil
}

var _ memory.Provider = (*fakeProvider)(nil)

// fakeRatifier wraps fakeProvider with both ApproveRecord and RejectRecord,
// satisfying memory.RatificationProvider in full — the "both" half of
// both-or-neither.
type fakeRatifier struct {
	fakeProvider

	approveFunc func(context.Context, string) (memory.RecordResult, error)
	rejectFunc  func(context.Context, string) (memory.DeleteResult, error)
}

func (f *fakeRatifier) ApproveRecord(ctx context.Context, id string) (memory.RecordResult, error) {
	if f.approveFunc != nil {
		return f.approveFunc(ctx, id)
	}
	return memory.RecordResult{}, nil
}

func (f *fakeRatifier) RejectRecord(ctx context.Context, id string) (memory.DeleteResult, error) {
	if f.rejectFunc != nil {
		return f.rejectFunc(ctx, id)
	}
	return memory.DeleteResult{}, nil
}

var _ memory.RatificationProvider = (*fakeRatifier)(nil)

// fakePartialRatifier implements ApproveRecord but deliberately NOT
// RejectRecord — the "neither counts as capable" half of both-or-neither.
// This type does NOT satisfy memory.RatificationProvider (RejectRecord is
// missing), so server.go's structural type assertion in NewService must
// treat it as ratification-incapable regardless of what its own
// Capabilities() claims.
type fakePartialRatifier struct {
	fakeProvider
}

func (f *fakePartialRatifier) ApproveRecord(context.Context, string) (memory.RecordResult, error) {
	return memory.RecordResult{}, nil
}

var _ memory.Provider = (*fakePartialRatifier)(nil)

// fakeRenderer wraps fakeProvider with Render, satisfying memory.Renderer.
type fakeRenderer struct {
	fakeProvider

	renderFunc func(context.Context, []byte, string) (*renderv1.RenderTree, error)
}

func (f *fakeRenderer) Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
	if f.renderFunc != nil {
		return f.renderFunc(ctx, payload, schemaVersion)
	}
	return nil, nil
}

var _ memory.Renderer = (*fakeRenderer)(nil)

// newTestClient starts svc on an in-memory bufconn listener and returns a
// memoryv1.MemoryServiceClient dialed against it — a real gRPC round trip,
// not a hand-rolled interface fake, so these tests exercise the actual wire
// marshaling server.go's translation code produces.
func newTestClient(t *testing.T, svc *memory.Service) memoryv1.MemoryServiceClient {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

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

	return memoryv1.NewMemoryServiceClient(conn)
}
