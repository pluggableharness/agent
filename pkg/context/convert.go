package context

import (
	"errors"
	"fmt"
	"strings"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
)

// ErrNonTextContent is returned when a Section's wire content
// carries a non-text ContentBlock. v1 is text-only
// (data-types.md#contextsection, conformance.md's summary matrix): the
// kernel MUST reject a non-text block, not silently drop it, and this SDK
// treats the same condition as an invalid_request-shaped translation
// failure rather than attempting a lossy conversion.
var ErrNonTextContent = errors.New("context: non-text content block (text-only in v1)")

// stabilityToProto converts a domain Stability to its wire enum value.
func stabilityToProto(s Stability) contentv1.Stability {
	switch s {
	case StabilityStatic:
		return contentv1.Stability_STABILITY_STATIC
	case StabilityDynamic:
		return contentv1.Stability_STABILITY_DYNAMIC
	default:
		return contentv1.Stability_STABILITY_UNSPECIFIED
	}
}

// stabilityFromProto converts a wire Stability enum value to its domain
// equivalent.
func stabilityFromProto(s contentv1.Stability) Stability {
	switch s {
	case contentv1.Stability_STABILITY_STATIC:
		return StabilityStatic
	case contentv1.Stability_STABILITY_DYNAMIC:
		return StabilityDynamic
	default:
		return StabilityUnspecified
	}
}

// contentBlocksToText concatenates a text-only ContentBlock chain's text
// into a single string, per data-types.md#contextsection's "text-only in
// v1" constraint. Returns ErrNonTextContent if any block isn't a text
// block.
func contentBlocksToText(blocks []*contentv1.ContentBlock) (string, error) {
	var sb strings.Builder
	for i, b := range blocks {
		text := b.GetText()
		if text == nil {
			return "", fmt.Errorf("context: block %d: %w", i, ErrNonTextContent)
		}
		sb.WriteString(text.GetText())
	}
	return sb.String(), nil
}

// textToContentBlocks wraps text into the single-element text-only
// ContentBlock slice the wire Section.Content field expects. Empty
// text produces a nil (empty) slice rather than a zero-length text block.
func textToContentBlocks(text string) []*contentv1.ContentBlock {
	if text == "" {
		return nil
	}
	return []*contentv1.ContentBlock{
		{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}}},
	}
}

// sectionToProto converts a domain Section to its wire
// representation. Returns nil for a nil input.
func sectionToProto(s *Section) *contentv1.ContextSection {
	if s == nil {
		return nil
	}
	return &contentv1.ContextSection{
		Provider:  s.Provider,
		Label:     s.Label,
		Content:   textToContentBlocks(s.Content),
		Tokens:    s.Tokens,
		Stability: stabilityToProto(s.Stability),
		Truncated: s.Truncated,
	}
}

// sectionFromProto converts a wire Section to its domain
// representation, rejecting a non-text content block per
// data-types.md#contextsection. Returns nil, nil for a nil input.
func sectionFromProto(s *contentv1.ContextSection) (*Section, error) {
	if s == nil {
		return nil, nil
	}
	text, err := contentBlocksToText(s.GetContent())
	if err != nil {
		return nil, fmt.Errorf("context: section %q: %w", s.GetProvider(), err)
	}
	return &Section{
		Provider:  s.GetProvider(),
		Label:     s.GetLabel(),
		Content:   text,
		Tokens:    s.GetTokens(),
		Stability: stabilityFromProto(s.GetStability()),
		Truncated: s.GetTruncated(),
	}, nil
}

// sectionsToProto converts a domain Section chain to its wire
// representation, in order.
func sectionsToProto(sections []*Section) []*contentv1.ContextSection {
	if sections == nil {
		return nil
	}
	out := make([]*contentv1.ContextSection, len(sections))
	for i, s := range sections {
		out[i] = sectionToProto(s)
	}
	return out
}

// sectionsFromProto converts a wire Section chain to its domain
// representation, in order, propagating the first ErrNonTextContent
// found.
func sectionsFromProto(sections []*contentv1.ContextSection) ([]*Section, error) {
	if sections == nil {
		return nil, nil
	}
	out := make([]*Section, len(sections))
	for i, s := range sections {
		converted, err := sectionFromProto(s)
		if err != nil {
			return nil, err
		}
		out[i] = converted
	}
	return out, nil
}

// capabilitiesToProto converts a domain Capabilities to its wire
// representation.
func capabilitiesToProto(c *Capabilities) *contextv1.ContextCapabilities {
	if c == nil {
		return nil
	}
	return &contextv1.ContextCapabilities{
		DefaultTokenBudget:  c.DefaultTokenBudget,
		Stability:           stabilityToProto(c.Stability),
		Compactor:           c.Compactor,
		SlashCommands:       c.SlashCommands,
		ConfigSchema:        c.ConfigSchema,
		SupportedHookPoints: c.SupportedHookPoints,
	}
}

// capabilitiesFromProto converts a wire Capabilities to its domain
// representation.
func capabilitiesFromProto(c *contextv1.ContextCapabilities) *Capabilities {
	if c == nil {
		return nil
	}
	return &Capabilities{
		DefaultTokenBudget:  c.GetDefaultTokenBudget(),
		Stability:           stabilityFromProto(c.GetStability()),
		Compactor:           c.GetCompactor(),
		SlashCommands:       c.GetSlashCommands(),
		ConfigSchema:        c.GetConfigSchema(),
		SupportedHookPoints: c.GetSupportedHookPoints(),
	}
}

// requestFromProto converts a wire Request to its domain
// representation. CountTokens is left nil — Service.Contribute sets it
// once it has dialed the kernel callback client.
func requestFromProto(r *contextv1.ContextRequest) (*Request, error) {
	if r == nil {
		return nil, nil
	}
	prior, err := sectionsFromProto(r.GetPriorSections())
	if err != nil {
		return nil, err
	}
	return &Request{
		SessionID:               r.GetSessionId(),
		ParentSessionID:         r.GetParentSessionId(),
		TurnID:                  r.GetTurnId(),
		TokenBudget:             r.GetTokenBudget(),
		ModelTarget:             r.GetModelTarget(),
		FilesTouched:            r.GetFilesTouched(),
		WorkingDirectory:        r.GetWorkingDirectory(),
		PriorSections:           prior,
		ConversationHistory:     r.GetConversationHistory(),
		HistoryTokens:           r.GetHistoryTokens(),
		AssembledTokensLastTurn: r.GetAssembledTokensLastTurn(),
	}, nil
}

// requestToProto converts a domain Request to its wire
// representation. Provided for symmetry and round-trip testing; server.go
// itself only ever needs requestFromProto since Request arrives
// off the wire, never leaves via it.
func requestToProto(r *Request) *contextv1.ContextRequest {
	if r == nil {
		return nil
	}
	return &contextv1.ContextRequest{
		SessionId:               r.SessionID,
		ParentSessionId:         r.ParentSessionID,
		TurnId:                  r.TurnID,
		TokenBudget:             r.TokenBudget,
		ModelTarget:             r.ModelTarget,
		FilesTouched:            r.FilesTouched,
		WorkingDirectory:        r.WorkingDirectory,
		PriorSections:           sectionsToProto(r.PriorSections),
		ConversationHistory:     r.ConversationHistory,
		HistoryTokens:           r.HistoryTokens,
		AssembledTokensLastTurn: r.AssembledTokensLastTurn,
	}
}

// contributionToProto converts a domain Contribution to its wire
// representation. A nil input converts to an empty, non-nil
// *contextv1.ContextContribution so Service.Contribute never returns a
// nil RPC response on a nil Provider.Contribute result.
func contributionToProto(c *Contribution) *contextv1.ContextContribution {
	if c == nil {
		return &contextv1.ContextContribution{}
	}
	return &contextv1.ContextContribution{
		Sections:         sectionsToProto(c.Sections),
		RewrittenHistory: c.RewrittenHistory,
	}
}

// contributionFromProto converts a wire Contribution to its domain
// representation. Provided for symmetry and round-trip testing.
func contributionFromProto(c *contextv1.ContextContribution) (*Contribution, error) {
	if c == nil {
		return nil, nil
	}
	sections, err := sectionsFromProto(c.GetSections())
	if err != nil {
		return nil, err
	}
	return &Contribution{
		Sections:         sections,
		RewrittenHistory: c.GetRewrittenHistory(),
	}, nil
}
