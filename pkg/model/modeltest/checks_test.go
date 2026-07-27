package modeltest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	"github.com/pluggableharness/agent/pkg/model/modeltest"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// rawProvider returns a hand-built Capabilities, bypassing
// model.NewCapabilities' own validation.
//
// That bypass is the point: NewCapabilities rejects most malformed
// advertisements before they ever reach the wire, so without it the
// suite's own declarative checks are unreachable from a Go provider. A
// provider written in another language has no such guard, and RunBinary
// is exactly how it would be checked — so these checks have to work, and
// have to be tested.
type rawProvider struct {
	caps   *model.Capabilities
	stream func(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error
}

func (p *rawProvider) Capabilities(context.Context) (*model.Capabilities, error) {
	return p.caps, nil
}

func (p *rawProvider) Configure(context.Context, *structpb.Struct) error { return nil }

func (p *rawProvider) StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	if p.stream != nil {
		return p.stream(ctx, req, sink)
	}
	if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
		return err
	}
	return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
}

var _ model.Provider = (*rawProvider)(nil)

// rawCaps wraps one Spec into a Capabilities with a schema present, so a
// case fails on the property it names rather than on a missing schema.
func rawCaps(spec model.Spec) *model.Capabilities {
	return &model.Capabilities{Models: []model.Spec{spec}, ConfigSchema: &configv1.ConfigSchema{}}
}

// baseSpec is a valid Spec each case bends in exactly one way.
func baseSpec() model.Spec {
	return model.Spec{
		ID:              "raw-1",
		ContextWindow:   1000,
		MaxOutputTokens: 100,
		Pricing:         model.Pricing{Currency: "USD", Free: true},
	}
}

func TestCheck_declarativeViolations(t *testing.T) {
	t.Parallel()

	tokens := func(v int64) *int64 { return &v }

	tests := map[string]struct {
		caps    *model.Capabilities
		wantMsg string
	}{
		"no config schema": {
			caps:    &model.Capabilities{Models: []model.Spec{baseSpec()}},
			wantMsg: "no ConfigSchema is advertised",
		},
		"no models": {
			caps:    &model.Capabilities{ConfigSchema: &configv1.ConfigSchema{}},
			wantMsg: "unroutable",
		},
		"empty model id": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.ID = ""
				return rawCaps(s)
			}(),
			wantMsg: "empty id",
		},
		"duplicate model id": {
			caps: &model.Capabilities{
				Models:       []model.Spec{baseSpec(), baseSpec()},
				ConfigSchema: &configv1.ConfigSchema{},
			},
			wantMsg: "more than once",
		},
		"max output not positive": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.MaxOutputTokens = 0
				return rawCaps(s)
			}(),
			wantMsg: "max_output_tokens",
		},
		"thinking unsupported but a control is declared": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{Effort: &model.EffortControl{Levels: []string{"low"}, Default: "low"}}
				return rawCaps(s)
			}(),
			wantMsg: "thinking is unsupported but a reasoning control is declared",
		},
		"thinking unsupported but adaptive": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{AdaptiveByDefault: true}
				return rawCaps(s)
			}(),
			wantMsg: "adaptive_by_default is set",
		},
		"thinking unsupported but disable claims otherwise": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{Disable: modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS}
				return rawCaps(s)
			}(),
			wantMsg: "disable claims reasoning can be turned off",
		},
		"effort control with no levels": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Effort:    &model.EffortControl{Default: "low"},
					Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
				return rawCaps(s)
			}(),
			wantMsg: "no levels",
		},
		"effort control with no default": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Effort:    &model.EffortControl{Levels: []string{"low"}},
					Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
				return rawCaps(s)
			}(),
			wantMsg: "no default level",
		},
		"budget control with no range": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Budget:    &model.BudgetControl{},
					Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
				return rawCaps(s)
			}(),
			wantMsg: "admits no usable budget",
		},
		"budget range inverted": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Budget:    &model.BudgetControl{Range: model.ThinkingBudgetRange{Min: 90, Max: 10}},
					Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
				return rawCaps(s)
			}(),
			wantMsg: "inverted",
		},
		"budget default outside the range": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Thinking = model.ThinkingSpec{
					Supported: true,
					Budget: &model.BudgetControl{
						Range:   model.ThinkingBudgetRange{Min: 10, Max: 90},
						Default: tokens(500),
					},
					Disable: modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
				}
				return rawCaps(s)
			}(),
			wantMsg: "outside the declared range",
		},
		"caching unsupported but a mechanism is declared": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Caching = model.CachingSpec{ExplicitMarkers: true}
				return rawCaps(s)
			}(),
			wantMsg: "caching is unsupported but a caching mechanism is declared",
		},
		"no pricing currency": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Pricing = model.Pricing{Free: true}
				return rawCaps(s)
			}(),
			wantMsg: "no pricing currency",
		},
		"no tiers and not free": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Pricing = model.Pricing{Currency: "USD"}
				return rawCaps(s)
			}(),
			wantMsg: "no pricing tiers",
		},
		"negative rate": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Pricing = model.Pricing{
					Currency: "USD",
					Tiers:    []model.PricingTier{{InputPerMtok: -1, OutputPerMtok: 5}},
				}
				return rawCaps(s)
			}(),
			wantMsg: "negative rate",
		},
		"caching supported but tier omits cache rates": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Caching = model.CachingSpec{Supported: true, ExplicitMarkers: true}
				s.Pricing = model.Pricing{
					Currency: "USD",
					Tiers:    []model.PricingTier{{InputPerMtok: 1, OutputPerMtok: 5}},
				}
				return rawCaps(s)
			}(),
			wantMsg: "omits a cache rate",
		},
		// Disjoint on time but overlapping on input size is still an
		// overlap only if both dimensions overlap — these do.
		"tiers overlapping on both dimensions": {
			caps: func() *model.Capabilities {
				s := baseSpec()
				s.Pricing = model.Pricing{
					Currency: "USD",
					Tiers: []model.PricingTier{
						{InputPerMtok: 1, OutputPerMtok: 5, InputTokensFrom: tokens(0), InputTokensUntil: tokens(100)},
						{InputPerMtok: 2, OutputPerMtok: 6, InputTokensFrom: tokens(50), InputTokensUntil: tokens(200)},
					},
				}
				return rawCaps(s)
			}(),
			wantMsg: "overlap",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := modeltest.Check(t.Context(), &rawProvider{caps: tt.caps},
				modeltest.WithCallTimeout(2*time.Second))
			if rep.OK() {
				t.Fatalf("the suite passed a violating advertisement; report:\n%s", rep)
			}
			if !strings.Contains(rep.String(), tt.wantMsg) {
				t.Errorf("no finding mentioned %q; the suite reported:\n%s", tt.wantMsg, rep)
			}
		})
	}
}

// TestCheck_disjointTiersAreNotAnOverlap is the negative control for the
// tier-overlap rule: adjacent half-open ranges are exactly what a correct
// provider declares, and flagging them would make the check unusable.
func TestCheck_disjointTiersAreNotAnOverlap(t *testing.T) {
	t.Parallel()

	from := int64(0)
	mid := int64(100)

	s := baseSpec()
	s.Pricing = model.Pricing{
		Currency: "USD",
		Tiers: []model.PricingTier{
			{InputPerMtok: 1, OutputPerMtok: 5, InputTokensFrom: &from, InputTokensUntil: &mid},
			{InputPerMtok: 2, OutputPerMtok: 6, InputTokensFrom: &mid},
		},
	}

	rep := modeltest.Check(t.Context(), &rawProvider{caps: rawCaps(s)}, modeltest.WithCallTimeout(2*time.Second))
	if strings.Contains(rep.String(), "overlap") {
		t.Errorf("adjacent half-open tiers were reported as overlapping:\n%s", rep)
	}
}

// TestCheck_streamViolations covers the stream checks a conforming
// provider never reaches.
func TestCheck_streamViolations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		stream  func(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error
		wantMsg string
	}{
		"error event with an unspecified category": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				return sink.Error(&model.Error{Message: "something went wrong"})
			},
			wantMsg: "unspecified category",
		},
		"unknown error without raw detail": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				return sink.Error(&model.Error{
					Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN,
					Message:  "unclassifiable",
				})
			},
			wantMsg: "omits raw_detail",
		},
		"error event with no message": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				return sink.Error(&model.Error{
					Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED,
				})
			},
			wantMsg: "no message",
		},
		"tool call started but never closed": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.ToolCallStart("call-1", "read"); err != nil {
					return err
				}
				if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
			},
			wantMsg: "every started call must be closed",
		},
		"tool call delta with no start": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.ToolCallDelta("orphan", `{"a":1}`); err != nil {
					return err
				}
				if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
			},
			wantMsg: "no preceding tool_call_start",
		},
		"rate-limit snapshot with no kind": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.Usage(model.Usage{
					InputTokens:  1,
					OutputTokens: 1,
					RateLimits:   []model.RateLimitSnapshot{{}},
				}); err != nil {
					return err
				}
				return sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, "")
			},
			wantMsg: "names no budget kind",
		},
		"matched_stop_sequence set for the wrong reason": {
			stream: func(_ context.Context, _ *modelv1.StreamCompletionRequest, sink *model.Sink) error {
				if err := sink.Usage(model.Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
					return err
				}
				// Sink only forwards the sequence for STOP_SEQUENCE, so the
				// violation is produced by claiming STOP_SEQUENCE with an
				// empty sequence instead.
				return sink.Stop(modelv1.StopReason_STOP_REASON_STOP_SEQUENCE, "")
			},
			wantMsg: "matched_stop_sequence is empty",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := modeltest.Check(t.Context(),
				&rawProvider{caps: rawCaps(baseSpec()), stream: tt.stream},
				modeltest.WithCallTimeout(2*time.Second))
			if !strings.Contains(rep.String(), tt.wantMsg) {
				t.Errorf("no finding mentioned %q; the suite reported:\n%s", tt.wantMsg, rep)
			}
		})
	}
}

// TestCheck_identityExpectationsAreSkippedInProcess asserts the suite
// says so rather than passing a check it cannot make.
//
// In-process, modeltest supplies the identity the service reports, so
// comparing it against the caller's expectation would only ever compare
// modeltest against itself — a check that can never fail is worse than
// no check, because it reads as coverage.
func TestCheck_identityExpectationsAreSkippedInProcess(t *testing.T) {
	t.Parallel()

	rep := modeltest.Check(t.Context(), &rawProvider{caps: rawCaps(baseSpec())},
		modeltest.WithCallTimeout(2*time.Second),
		modeltest.WithExpectedIdentity("not-the-name", "9.9.9", "not-the-source"))

	var found bool
	for _, f := range rep.Findings {
		if strings.Contains(f.Check, "expected-identity") {
			found = true
			if f.Severity != modeltest.SeveritySkip {
				t.Errorf("the identity expectation was reported as %v, want a skip", f.Severity)
			}
		}
	}
	if !found {
		t.Errorf("the unverifiable identity expectation was not reported at all:\n%s", rep)
	}
}

// TestCheck_withoutStreamCompletionReportsTheGap asserts that opting out
// of the behavioral checks is visible rather than quiet — it is a real
// reduction in coverage, not a pass.
func TestCheck_withoutStreamCompletionReportsTheGap(t *testing.T) {
	t.Parallel()

	rep := modeltest.Check(t.Context(), &conformingProvider{}, modeltest.WithoutStreamCompletion())
	if !rep.OK() {
		t.Fatalf("skipping behavioral checks produced failures:\n%s", rep)
	}
	if !strings.Contains(rep.String(), "WithoutStreamCompletion") {
		t.Errorf("the skipped behavioral checks were not reported:\n%s", rep)
	}
}

// TestCheck_unknownModelIDIsReported covers WithModelID naming a model
// the provider does not advertise.
func TestCheck_unknownModelIDIsReported(t *testing.T) {
	t.Parallel()

	rep := modeltest.Check(t.Context(), &conformingProvider{}, modeltest.WithModelID("not-advertised"))
	if !strings.Contains(rep.String(), "not advertised") {
		t.Errorf("selecting an unadvertised model was not reported:\n%s", rep)
	}
}

// TestCheck_configureThatOnlyWorksOnceIsCaught guards the requirement
// whose failure is most expensive to discover late: the kernel calls
// Configure once at bring-up, so a provider that cannot take a second
// call looks perfectly healthy until a credential rotation needs one.
func TestCheck_configureThatOnlyWorksOnceIsCaught(t *testing.T) {
	t.Parallel()

	p := &singleUseConfigureProvider{}
	rep := modeltest.Check(t.Context(), p, modeltest.WithCallTimeout(2*time.Second))

	if !strings.Contains(rep.String(), "re-callable") {
		t.Errorf("the suite did not catch a Configure that only works once:\n%s", rep)
	}
}

// singleUseConfigureProvider accepts Configure exactly once.
type singleUseConfigureProvider struct {
	conformingProvider
	configured bool
}

func (p *singleUseConfigureProvider) Configure(context.Context, *structpb.Struct) error {
	if p.configured {
		return &model.Error{
			Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
			Message:  "already configured",
		}
	}
	p.configured = true
	return nil
}
