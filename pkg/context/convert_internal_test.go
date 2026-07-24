package context

import (
	"errors"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// This file is a white-box (package context, not context_test) test
// deliberately: convert.go's functions are all unexported translation
// helpers between this package's domain types and the generated
// pkg/context/proto/v1 (plus content/v1) wire types — there is nothing
// for an external caller to reach, so they're tested directly here,
// mirroring pkg/plugin's own *_internal_test.go convention.

func TestStabilityRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proto contentv1.Stability
		want  Stability
	}{
		{"static", contentv1.Stability_STABILITY_STATIC, StabilityStatic},
		{"dynamic", contentv1.Stability_STABILITY_DYNAMIC, StabilityDynamic},
		{"unspecified", contentv1.Stability_STABILITY_UNSPECIFIED, StabilityUnspecified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := stabilityFromProto(tt.proto); got != tt.want {
				t.Errorf("stabilityFromProto(%v) = %v, want %v", tt.proto, got, tt.want)
			}
			if got := stabilityToProto(tt.want); got != tt.proto {
				t.Errorf("stabilityToProto(%v) = %v, want %v", tt.want, got, tt.proto)
			}
		})
	}
}

func TestContentBlocksToText(t *testing.T) {
	t.Parallel()

	t.Run("text-only concatenates", func(t *testing.T) {
		t.Parallel()

		blocks := []*contentv1.ContentBlock{
			{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "hello "}}},
			{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "world"}}},
		}
		got, err := contentBlocksToText(blocks)
		if err != nil {
			t.Fatalf("contentBlocksToText() error = %v, want nil", err)
		}
		if want := "hello world"; got != want {
			t.Errorf("contentBlocksToText() = %q, want %q", got, want)
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		got, err := contentBlocksToText(nil)
		if err != nil {
			t.Fatalf("contentBlocksToText(nil) error = %v, want nil", err)
		}
		if got != "" {
			t.Errorf("contentBlocksToText(nil) = %q, want empty", got)
		}
	})

	t.Run("non-text block rejected", func(t *testing.T) {
		t.Parallel()

		blocks := []*contentv1.ContentBlock{
			{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}},
		}
		_, err := contentBlocksToText(blocks)
		if !errors.Is(err, ErrNonTextContent) {
			t.Fatalf("contentBlocksToText() error = %v, want wrapping ErrNonTextContent", err)
		}
	})
}

func TestTextToContentBlocks(t *testing.T) {
	t.Parallel()

	if got := textToContentBlocks(""); got != nil {
		t.Errorf("textToContentBlocks(\"\") = %v, want nil", got)
	}

	got := textToContentBlocks("hi")
	if len(got) != 1 {
		t.Fatalf("textToContentBlocks(\"hi\") len = %d, want 1", len(got))
	}
	if got[0].GetText().GetText() != "hi" {
		t.Errorf("textToContentBlocks(\"hi\")[0].Text = %q, want %q", got[0].GetText().GetText(), "hi")
	}
}

func TestSectionRoundTrip(t *testing.T) {
	t.Parallel()

	if got := sectionToProto(nil); got != nil {
		t.Errorf("sectionToProto(nil) = %v, want nil", got)
	}
	got, err := sectionFromProto(nil)
	if err != nil || got != nil {
		t.Errorf("sectionFromProto(nil) = %v, %v, want nil, nil", got, err)
	}

	section := &Section{
		Provider:  "claude-md",
		Label:     "Project conventions (CLAUDE.md)",
		Content:   "This repo uses...",
		Tokens:    480,
		Stability: StabilityStatic,
		Truncated: false,
	}
	proto := sectionToProto(section)
	if proto.GetProvider() != section.Provider || proto.GetLabel() != section.Label || proto.GetTokens() != section.Tokens {
		t.Errorf("sectionToProto(%+v) = %+v, fields mismatch", section, proto)
	}
	if len(proto.GetContent()) != 1 || proto.GetContent()[0].GetText().GetText() != section.Content {
		t.Errorf("sectionToProto(%+v).Content = %v, want single text block %q", section, proto.GetContent(), section.Content)
	}

	back, err := sectionFromProto(proto)
	if err != nil {
		t.Fatalf("sectionFromProto() error = %v, want nil", err)
	}
	if !sectionEqual(back, section) {
		t.Errorf("sectionFromProto(sectionToProto(%+v)) = %+v, want round trip", section, back)
	}
}

func TestSectionFromProto_nonTextRejected(t *testing.T) {
	t.Parallel()

	proto := &contentv1.ContextSection{
		Provider: "bad",
		Label:    "bad",
		Content:  []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}}},
	}
	_, err := sectionFromProto(proto)
	if !errors.Is(err, ErrNonTextContent) {
		t.Fatalf("sectionFromProto() error = %v, want wrapping ErrNonTextContent", err)
	}
}

func TestSectionsRoundTrip(t *testing.T) {
	t.Parallel()

	if got := sectionsToProto(nil); got != nil {
		t.Errorf("sectionsToProto(nil) = %v, want nil", got)
	}
	got, err := sectionsFromProto(nil)
	if err != nil || got != nil {
		t.Errorf("sectionsFromProto(nil) = %v, %v, want nil, nil", got, err)
	}

	sections := []*Section{
		{Provider: "a", Label: "A", Content: "one", Tokens: 1, Stability: StabilityStatic},
		{Provider: "b", Label: "B", Content: "two", Tokens: 2, Stability: StabilityDynamic},
	}
	proto := sectionsToProto(sections)
	if len(proto) != 2 {
		t.Fatalf("sectionsToProto() len = %d, want 2", len(proto))
	}
	back, err := sectionsFromProto(proto)
	if err != nil {
		t.Fatalf("sectionsFromProto() error = %v, want nil", err)
	}
	if len(back) != 2 || !sectionEqual(back[0], sections[0]) || !sectionEqual(back[1], sections[1]) {
		t.Errorf("sectionsFromProto(sectionsToProto(%+v)) = %+v, want round trip", sections, back)
	}
}

func TestSectionsFromProto_propagatesError(t *testing.T) {
	t.Parallel()

	proto := []*contentv1.ContextSection{
		{Provider: "bad", Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}}}},
	}
	_, err := sectionsFromProto(proto)
	if !errors.Is(err, ErrNonTextContent) {
		t.Fatalf("sectionsFromProto() error = %v, want wrapping ErrNonTextContent", err)
	}
}

func TestCapabilitiesRoundTrip(t *testing.T) {
	t.Parallel()

	if got := capabilitiesToProto(nil); got != nil {
		t.Errorf("capabilitiesToProto(nil) = %v, want nil", got)
	}
	if got := capabilitiesFromProto(nil); got != nil {
		t.Errorf("capabilitiesFromProto(nil) = %v, want nil", got)
	}

	schema, err := configSchemaForTest()
	if err != nil {
		t.Fatalf("configSchemaForTest() error = %v", err)
	}
	caps := &Capabilities{
		DefaultTokenBudget:  2000,
		Stability:           StabilityStatic,
		Compactor:           true,
		SlashCommands:       []*commonv1.PromptExpansionSpec{{Name: "review", Template: "review {{.arg}}"}},
		ConfigSchema:        schema,
		SupportedHookPoints: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START},
	}
	proto := capabilitiesToProto(caps)
	if proto.GetDefaultTokenBudget() != caps.DefaultTokenBudget || !proto.GetCompactor() {
		t.Errorf("capabilitiesToProto(%+v) = %+v, fields mismatch", caps, proto)
	}
	back := capabilitiesFromProto(proto)
	if back.DefaultTokenBudget != caps.DefaultTokenBudget ||
		back.Stability != caps.Stability ||
		back.Compactor != caps.Compactor ||
		len(back.SlashCommands) != len(caps.SlashCommands) ||
		len(back.SupportedHookPoints) != len(caps.SupportedHookPoints) {
		t.Errorf("capabilitiesFromProto(capabilitiesToProto(%+v)) = %+v, want round trip", caps, back)
	}
}

func TestRequestRoundTrip(t *testing.T) {
	t.Parallel()

	if got, err := requestFromProto(nil); got != nil || err != nil {
		t.Errorf("requestFromProto(nil) = %v, %v, want nil, nil", got, err)
	}
	if got := requestToProto(nil); got != nil {
		t.Errorf("requestToProto(nil) = %v, want nil", got)
	}

	req := &Request{
		SessionID:        "sess_01",
		ParentSessionID:  "sess_00",
		TurnID:           "turn_01",
		TokenBudget:      2000,
		ModelTarget:      &modelv1.ModelTarget{Id: "claude-opus-5", ContextWindow: 200000, EffectiveCeiling: 176000},
		FilesTouched:     []string{"src/auth/validator.py"},
		WorkingDirectory: "/repo",
		PriorSections: []*Section{
			{Provider: "claude-md", Label: "CLAUDE.md", Content: "conventions", Tokens: 10, Stability: StabilityStatic},
		},
		HistoryTokens:           500,
		AssembledTokensLastTurn: 1200,
	}
	proto := requestToProto(req)
	if proto.GetSessionId() != req.SessionID || proto.GetTurnId() != req.TurnID || proto.GetTokenBudget() != req.TokenBudget {
		t.Errorf("requestToProto(%+v) = %+v, fields mismatch", req, proto)
	}

	back, err := requestFromProto(proto)
	if err != nil {
		t.Fatalf("requestFromProto() error = %v, want nil", err)
	}
	if back.SessionID != req.SessionID || back.TurnID != req.TurnID || back.TokenBudget != req.TokenBudget ||
		back.HistoryTokens != req.HistoryTokens || back.AssembledTokensLastTurn != req.AssembledTokensLastTurn ||
		len(back.PriorSections) != len(req.PriorSections) || len(back.FilesTouched) != len(req.FilesTouched) {
		t.Errorf("requestFromProto(requestToProto(%+v)) = %+v, want round trip", req, back)
	}
}

func TestRequestFromProto_propagatesSectionError(t *testing.T) {
	t.Parallel()

	proto := &contextv1.ContextRequest{
		PriorSections: []*contentv1.ContextSection{
			{Provider: "bad", Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}}}},
		},
	}
	_, err := requestFromProto(proto)
	if !errors.Is(err, ErrNonTextContent) {
		t.Fatalf("requestFromProto() error = %v, want wrapping ErrNonTextContent", err)
	}
}

func TestContributionRoundTrip(t *testing.T) {
	t.Parallel()

	if got := contributionToProto(nil); got == nil {
		t.Errorf("contributionToProto(nil) = nil, want non-nil empty contribution")
	}
	if got, err := contributionFromProto(nil); got != nil || err != nil {
		t.Errorf("contributionFromProto(nil) = %v, %v, want nil, nil", got, err)
	}

	contribution := &Contribution{
		Sections: []*Section{
			{Provider: "claude-md", Label: "CLAUDE.md", Content: "conventions", Tokens: 10, Stability: StabilityStatic},
		},
	}
	proto := contributionToProto(contribution)
	if len(proto.GetSections()) != 1 {
		t.Fatalf("contributionToProto() sections len = %d, want 1", len(proto.GetSections()))
	}
	back, err := contributionFromProto(proto)
	if err != nil {
		t.Fatalf("contributionFromProto() error = %v, want nil", err)
	}
	if len(back.Sections) != 1 || !sectionEqual(back.Sections[0], contribution.Sections[0]) {
		t.Errorf("contributionFromProto(contributionToProto(%+v)) = %+v, want round trip", contribution, back)
	}
}

func TestContributionFromProto_propagatesSectionError(t *testing.T) {
	t.Parallel()

	proto := &contextv1.ContextContribution{
		Sections: []*contentv1.ContextSection{
			{Provider: "bad", Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}}}},
		},
	}
	_, err := contributionFromProto(proto)
	if !errors.Is(err, ErrNonTextContent) {
		t.Fatalf("contributionFromProto() error = %v, want wrapping ErrNonTextContent", err)
	}
}

// configSchemaForTest builds a minimal, valid *configv1.ConfigSchema for
// tests that need a non-nil ConfigSchema value without depending on
// pkg/config from within a white-box test file.
func configSchemaForTest() (*configv1.ConfigSchema, error) {
	return &configv1.ConfigSchema{}, nil
}
