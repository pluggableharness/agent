package modeltest_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	"github.com/pluggableharness/agent/pkg/model/modeltest"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

const conformingID = "conforming-1"

// conformingProvider satisfies every MUST the suite checks. It is the
// positive control: if the suite fails against this, the suite is wrong.
//
// mutate bends one declaration per case, so a negative control differs
// from the positive one in exactly the property under test.
type conformingProvider struct {
	mutate func(*model.Spec)
	stream func(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error
}

func (p *conformingProvider) Capabilities(context.Context) (*model.Capabilities, error) {
	spec := model.Spec{
		ID:              conformingID,
		ContextWindow:   200_000,
		MaxOutputTokens: 4096,
		Thinking:        model.ThinkingSpec{},
		Caching:         model.CachingSpec{},
		Pricing:         model.Pricing{Currency: "USD", Free: true},
	}
	if p.mutate != nil {
		p.mutate(&spec)
	}
	return model.NewCapabilities([]model.Spec{spec}, &configv1.ConfigSchema{})
}

func (p *conformingProvider) Configure(context.Context, *structpb.Struct) error { return nil }

func (p *conformingProvider) StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	if p.stream != nil {
		return p.stream(ctx, req, sink)
	}
	if req.GetModelId() != conformingID {
		return &model.Error{
			Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
			Message:  "unknown model",
		}
	}
	// Reject content this model declares no support for, rather than
	// silently dropping it.
	for _, m := range req.GetMessages() {
		for _, b := range m.GetContent() {
			switch b.GetBlock().(type) {
			case *contentv1.ContentBlock_Image, *contentv1.ContentBlock_Document:
				return &model.Error{
					Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
					Message:  "unsupported content block",
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sink.TextDelta("pong"); err != nil {
		return err
	}
	if err := sink.Usage(model.Usage{InputTokens: 3, OutputTokens: 1}); err != nil {
		return err
	}
	return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
}

var _ model.Provider = (*conformingProvider)(nil)

func TestCheck_conformingProviderHasNoFailures(t *testing.T) {
	t.Parallel()

	rep := modeltest.Check(t.Context(), &conformingProvider{})
	if !rep.OK() {
		t.Fatalf("a conforming provider produced failures:\n%s", rep)
	}
	// Skips are expected — the suite cannot force this provider to emit a
	// tool call or an encrypted reasoning block — but they must be
	// reported rather than silently omitted, so an unexercised check never
	// reads as a pass.
	if len(rep.Skips()) == 0 {
		t.Error("no skips were reported; unreachable checks must be visible, not omitted")
	}
}

func TestRun_conformingProviderPasses(t *testing.T) {
	t.Parallel()
	modeltest.Run(t, &conformingProvider{})
}

// TestCheck_catchesRealViolations is the suite's own regression guard. A
// conformance suite that cannot be shown to fail is worth very little, so
// each case is a provider with one genuine defect and the suite must name
// it.
func TestCheck_catchesRealViolations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		provider model.Provider
		wantMsg  string
	}{
		// The worst failure mode: to the kernel this looks like a clean
		// turn that produced nothing, which is indistinguishable from
		// success.
		"no terminal event": {
			provider: &conformingProvider{stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				return sink.TextDelta("no stop follows this")
			}},
			wantMsg: "no terminal event",
		},
		"no usage event": {
			provider: &conformingProvider{stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.TextDelta("pong"); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
			}},
			wantMsg: "no usage event",
		},
		"unspecified stop reason": {
			provider: &conformingProvider{stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_UNSPECIFIED, "")
			}},
			wantMsg: "STOP_REASON_UNSPECIFIED",
		},
		// Accepts anything, including an image on a model declaring no
		// vision support.
		"unsupported content accepted": {
			provider: &conformingProvider{stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
			}},
			wantMsg: "MUST be rejected",
		},
		// Caught by model.NewCapabilities before the suite ever sees the
		// advertisement, so the suite reports the failed RPC. That is the
		// right outcome — a provider cannot ship this — and the assertion
		// tracks the message that is actually produced.
		"thinking supported without a disable value": {
			provider: &conformingProvider{mutate: func(s *model.Spec) {
				s.Thinking = model.ThinkingSpec{Supported: true}
			}},
			wantMsg: "disable required when thinking is supported",
		},
		"effort default is not a declared level": {
			provider: &conformingProvider{mutate: func(s *model.Spec) {
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Effort:    &model.EffortControl{Levels: []string{"low", "high"}, Default: "medium"},
					Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
			}},
			wantMsg: "not one of the declared levels",
		},
		"caching supported with no mechanism": {
			provider: &conformingProvider{mutate: func(s *model.Spec) {
				s.Caching = model.CachingSpec{Supported: true}
			}},
			wantMsg: "neither explicit_markers nor implicit_automatic declared",
		},
		"context window is not positive": {
			provider: &conformingProvider{mutate: func(s *model.Spec) { s.ContextWindow = 0 }},
			wantMsg:  "context_window",
		},
		"overlapping pricing tiers": {
			provider: &conformingProvider{mutate: func(s *model.Spec) {
				s.Pricing = model.Pricing{
					Currency: "USD",
					Tiers: []model.PricingTier{
						{InputPerMtok: 3, OutputPerMtok: 15},
						{InputPerMtok: 4, OutputPerMtok: 20},
					},
				}
			}},
			wantMsg: "overlap",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := modeltest.Check(t.Context(), tt.provider)
			if rep.OK() {
				t.Fatalf("the suite passed a provider that violates the spec; report:\n%s", rep)
			}
			if !strings.Contains(rep.String(), tt.wantMsg) {
				t.Errorf("no finding mentioned %q; the suite reported:\n%s", tt.wantMsg, rep)
			}
		})
	}
}

// TestCheck_cancellationIsNotReportedAsAFailure asserts the positive
// case, which is what this check can actually establish from a black-box
// client. Detecting a provider that IGNORES cancellation is not possible
// from this side — gRPC returns to the canceling client immediately,
// whatever the server does — and checkCancellation's own comment records
// that limit rather than implying coverage it does not have.
func TestCheck_cancellationIsNotReportedAsAFailure(t *testing.T) {
	t.Parallel()

	rep := modeltest.Check(t.Context(), &conformingProvider{})
	for _, f := range rep.Findings {
		if strings.HasPrefix(f.Check, "Cancellation/") && f.Severity == modeltest.SeverityFail {
			t.Errorf("a well-behaved provider produced a cancellation failure: %s", f)
		}
	}
}

func TestReport_stringIsDeterministic(t *testing.T) {
	t.Parallel()

	// Two runs over the same provider must produce byte-identical output,
	// or a report diff is unreadable noise.
	first := modeltest.Check(t.Context(), &conformingProvider{}).String()
	second := modeltest.Check(t.Context(), &conformingProvider{}).String()
	if first != second {
		t.Errorf("two runs produced different reports:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
