package agentprofile

import (
	"errors"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// ErrNoEligibleModel is returned by SelectModel when no candidate in the
// primary+fallback chain — including ones missing from the caller-supplied
// specs lookup — satisfies the turn's requirements.
var ErrNoEligibleModel = errors.New("agentprofile: no eligible model in primary+fallback chain")

// TurnRequirements describes what a single turn actually needs from a
// model, per configuration.md §8.2's "context length needed, tool-use,
// vision, thinking" axes (cross-referencing model.md §9's required-
// capability matrix, which gates exactly these same four capabilities).
type TurnRequirements struct {
	// NeedsToolUse requires the candidate's ModelSpec.SupportsToolUse.
	NeedsToolUse bool
	// NeedsVision requires the candidate's ModelSpec.SupportsVision.
	NeedsVision bool
	// NeedsThinking requires the candidate's ModelSpec.Thinking to be
	// present and report Supported.
	NeedsThinking bool
	// MinContextWindow requires the candidate's ModelSpec.ContextWindow to
	// be at least this many tokens. Zero means no minimum.
	MinContextWindow int64
}

// SelectModel walks block.Primary then block.Fallbacks, in declared order,
// and returns the first ModelRef whose looked-up ModelSpec satisfies req
// per satisfies (configuration.md §8.2). Declaration order is a preference,
// not the sole criterion: a candidate ineligible for this turn's actual
// requirements is skipped even if it comes first, and the kernel falls
// through to the next declared candidate — this is model.md §9's
// capability-aware routing rule, checked mechanically per turn.
//
// specs is caller-supplied: this package owns none of the provider
// communication or capability discovery that populates it. A ModelRef with
// no entry in specs (not found, or its provider/model not loaded this
// session) is treated as not eligible and skipped, not an error — only an
// empty result across the whole chain is an error.
func SelectModel(block ModelBlock, specs map[ModelRef]*modelv1.ModelSpec, req TurnRequirements) (ModelRef, error) {
	candidates := make([]ModelRef, 0, 1+len(block.Fallbacks))
	candidates = append(candidates, block.Primary)
	candidates = append(candidates, block.Fallbacks...)

	for _, ref := range candidates {
		spec, ok := specs[ref]
		if !ok {
			continue
		}
		if satisfies(spec, req) {
			return ref, nil
		}
	}
	return ModelRef{}, ErrNoEligibleModel
}

// satisfies reports whether spec meets every axis of req
// (configuration.md §8.2, model.md §9).
func satisfies(spec *modelv1.ModelSpec, req TurnRequirements) bool {
	if req.NeedsToolUse && !spec.GetSupportsToolUse() {
		return false
	}
	if req.NeedsVision && !spec.GetSupportsVision() {
		return false
	}
	if req.NeedsThinking && (spec.GetThinking() == nil || !spec.GetThinking().GetSupported()) {
		return false
	}
	if spec.GetContextWindow() < req.MinContextWindow {
		return false
	}
	return true
}
