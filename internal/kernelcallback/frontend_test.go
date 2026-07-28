package kernelcallback

import (
	"context"
	"testing"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/metadata"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metabuilders "github.com/pluggableharness/agent/pkg/metadata"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"

	"google.golang.org/grpc/codes"
)

func TestServer_PublishListRetractMetadata(t *testing.T) {
	t.Parallel()

	store := metadata.NewStore()
	f := newTestServer(t, testProducer(), func(cfg *Config) {
		cfg.Metadata = store
	})
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	block := metabuilders.KeyValue("branch", "branch", "main")
	pub, err := f.server.PublishMetadata(context.Background(), &kernelv1.PublishMetadataRequest{
		SessionId: sessionID,
		Block:     block,
	})
	if err != nil {
		t.Fatalf("PublishMetadata: %v", err)
	}
	if pub.GetBlock().GetProducer().GetName() != testProducer().GetName() {
		t.Errorf("producer = %v", pub.GetBlock().GetProducer())
	}
	if pub.GetBlock().GetLiveness() != metadatav1.Liveness_LIVENESS_LIVE {
		t.Errorf("liveness = %v", pub.GetBlock().GetLiveness())
	}

	list, err := f.server.ListMetadata(context.Background(), &kernelv1.ListMetadataRequest{SessionId: sessionID})
	if err != nil {
		t.Fatalf("ListMetadata: %v", err)
	}
	if len(list.GetBlocks()) != 1 {
		t.Fatalf("List = %d blocks", len(list.GetBlocks()))
	}

	ret, err := f.server.RetractMetadata(context.Background(), &kernelv1.RetractMetadataRequest{
		SessionId: sessionID,
		BlockId:   "branch",
	})
	if err != nil {
		t.Fatalf("RetractMetadata: %v", err)
	}
	if ret.GetBlock().GetLiveness() != metadatav1.Liveness_LIVENESS_DISCONNECTED {
		t.Errorf("retract liveness = %v", ret.GetBlock().GetLiveness())
	}
	list, err = f.server.ListMetadata(context.Background(), &kernelv1.ListMetadataRequest{SessionId: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetBlocks()) != 1 {
		t.Fatalf("retract deleted block")
	}
}

func TestServer_PublishMetadata_requiresAuthz(t *testing.T) {
	t.Parallel()

	f := newTestServer(t, testProducer(), func(cfg *Config) {
		cfg.Metadata = metadata.NewStore()
	})
	_, err := f.server.PublishMetadata(context.Background(), &kernelv1.PublishMetadataRequest{
		SessionId: "no-such-session",
		Block:     metabuilders.KeyValue("x", "k", "v"),
	})
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_GetSessionState(t *testing.T) {
	t.Parallel()

	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	res, err := f.server.GetSessionState(context.Background(), &kernelv1.GetSessionStateRequest{
		SessionId: sessionID,
	})
	if err != nil {
		t.Fatalf("GetSessionState: %v", err)
	}
	if res.GetState().GetInfo().GetSessionId() != sessionID {
		t.Errorf("session_id = %q, want %q", res.GetState().GetInfo().GetSessionId(), sessionID)
	}
	if res.GetState().GetElapsed() == nil {
		t.Error("elapsed is nil")
	}
}

func TestDeltaHub_PublishNoSubscribers(t *testing.T) {
	t.Parallel()

	hub := NewDeltaHub()
	// Must not panic with zero subscribers.
	hub.Publish(&kernelv1.TokenDelta{SessionId: "s1", TargetId: "t", Text: "hi"})
}
