package modeltest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	"github.com/pluggableharness/agent/pkg/model/modeltest"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func TestWithConfig_reachesConfigure(t *testing.T) {
	t.Parallel()

	cfg, err := structpb.NewStruct(map[string]any{"api_key": "supplied-by-the-caller"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	var got *structpb.Struct
	p := &configRecordingProvider{onConfigure: func(c *structpb.Struct) { got = c }}

	rep := modeltest.Check(t.Context(), p, modeltest.WithConfig(cfg), modeltest.WithCallTimeout(2*time.Second))
	if !rep.OK() {
		t.Fatalf("unexpected failures:\n%s", rep)
	}
	if got.GetFields()["api_key"].GetStringValue() != "supplied-by-the-caller" {
		t.Errorf("Configure received %v, want the caller's config", got)
	}
}

func TestWithStreamRequest_isUsedAndItsModelIDOverwritten(t *testing.T) {
	t.Parallel()

	var seen *modelv1.StreamCompletionRequest
	p := &conformingProvider{stream: func(_ context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
		if seen == nil {
			seen = req
		}
		if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
			return err
		}
		return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
	}}

	custom := &modelv1.StreamCompletionRequest{
		// A deliberately wrong model id: the suite overwrites it with the
		// resolved model, so a caller does not have to keep the two in
		// sync.
		ModelId: "stale-id-the-caller-forgot",
		Messages: []*contentv1.Message{{
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "custom prompt"}},
			}},
		}},
	}

	modeltest.Check(t.Context(), p, modeltest.WithStreamRequest(custom), modeltest.WithCallTimeout(2*time.Second))

	if seen == nil {
		t.Fatal("the provider was never called")
	}
	if seen.GetModelId() != conformingID {
		t.Errorf("model_id = %q, want the resolved %q", seen.GetModelId(), conformingID)
	}
	if got := seen.GetMessages()[0].GetContent()[0].GetText().GetText(); got != "custom prompt" {
		t.Errorf("prompt = %q, want the caller's", got)
	}
	// The caller's request must not be mutated: reusing one across runs is
	// the obvious thing to do, and a rewritten model_id would silently
	// change the second run.
	if custom.GetModelId() != "stale-id-the-caller-forgot" {
		t.Errorf("the caller's request was mutated: model_id is now %q", custom.GetModelId())
	}
}

func TestWithCallTimeout_ignoresNonPositive(t *testing.T) {
	t.Parallel()

	// A caller passing an unset config field gets the default rather than
	// a suite whose every RPC times out instantly.
	rep := modeltest.Check(t.Context(), &conformingProvider{}, modeltest.WithCallTimeout(0))
	if !rep.OK() {
		t.Errorf("a zero timeout was not ignored:\n%s", rep)
	}
	if strings.Contains(rep.String(), "DeadlineExceeded") {
		t.Errorf("a zero timeout took effect:\n%s", rep)
	}
}

// configRecordingProvider is a conforming provider that reports what
// Configure received.
type configRecordingProvider struct {
	conformingProvider
	onConfigure func(*structpb.Struct)
}

func (p *configRecordingProvider) Configure(_ context.Context, cfg *structpb.Struct) error {
	if p.onConfigure != nil {
		p.onConfigure(cfg)
	}
	return nil
}
