package modelrequest

import (
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// CacheBreakpoint is a plain alias of the generated wire type,
// *modelv1.CacheBreakpoint — not a second, parallel Go representation.
// data-types.md#cache_breakpoints-and-cache-breakpoint-placement-policy's
// CacheBreakpoint is already a clean, three-variant oneof
// (after_assembled_context / after_tools / after_message_index) with no
// awkward index-based computation this package needs to hide behind an
// intermediate domain type; per go-layout.md's "internal/ MUST consume
// the generated types directly" rule, aliasing it is the correct choice
// over inventing a redundant wrapper.
type CacheBreakpoint = modelv1.CacheBreakpoint

// PlaceCacheBreakpoints computes where the kernel should mark
// StreamCompletionRequest.cache_breakpoints for one request, per
// protocol.md#cache-breakpoint-placement-policy and
// data-types.md#cache_breakpoints-and-cache-breakpoint-placement-policy.
// Placement is a kernel decision, never the plugin's: the plugin's only
// job is translating whatever breakpoints the kernel already decided into
// vendor-native cache-control markers.
//
// Returns nil when spec's CachingSpec.mode is not
// CACHING_MODE_EXPLICIT_MARKERS — the field is meaningless for any other
// mode, and an adapter targeting CACHING_MODE_IMPLICIT_AUTOMATIC or
// CACHING_MODE_NONE MUST ignore it rather than error on it, so there is
// nothing for the kernel to compute either.
//
// Otherwise, it places a single after_assembled_context breakpoint when
// sections has a leading STABILITY_STATIC run — i.e. sections is
// non-empty and its first entry is STABILITY_STATIC — since that is
// exactly the "most commonly: right after assembled_context when its
// leading sections are STABILITY_STATIC" case
// protocol.md#cache-breakpoint-placement-policy calls out as the usual,
// longest-stable-prefix choice (examples.md's full StreamCompletion
// worked example places exactly this breakpoint, against a single
// STABILITY_STATIC section). No breakpoint is placed when sections is
// empty or its first entry is STABILITY_DYNAMIC: the CacheBreakpoint wire
// shape has no per-section marker, only a marker for the assembled_context
// chain as a whole, so there is no natural stable-prefix boundary to name
// unless the chain's leading content is itself stable.
//
// This deliberately does not compute an after_tools breakpoint.
// protocol.md#cache-breakpoint-placement-policy's alternative case — "a
// breakpoint after after_tools when the tool declaration list is stable
// turn to turn" — requires knowing whether the tool declaration list
// actually is stable across turns, which is per-turn history this
// function's fixed inputs (sections, messages, spec) don't carry: there
// is no ToolDeclaration list, and no prior-turn comparison, available
// here. Emitting after_tools unconditionally would not be a computed
// kernel decision, just an assumption; leaving it uncomputed until a
// caller can supply real turn-to-turn tool stability is the honest
// choice given this function's signature.
//
// messages is accepted per this package's exact API and reserved for a
// future message-position-aware placement rule — the policy text notes
// the kernel "knows ... each message's position" — but v1's only
// concrete placement heuristic operates on assembled_context's
// Stability, which messages carries no equivalent of (content.v1.Message
// has no Stability field), so it is currently unused.
func PlaceCacheBreakpoints(sections []*contentv1.ContextSection, messages []*contentv1.Message, spec *modelv1.ModelSpec) []*CacheBreakpoint { //nolint:revive // messages reserved for future message-position-aware placement, see doc comment above
	if !spec.GetCaching().GetExplicitMarkers() {
		return nil
	}

	if len(sections) == 0 || sections[0].GetStability() != contentv1.Stability_STABILITY_STATIC {
		return nil
	}

	return []*CacheBreakpoint{
		{
			Position: &modelv1.CacheBreakpoint_AfterAssembledContext_{
				AfterAssembledContext: &modelv1.CacheBreakpoint_AfterAssembledContext{},
			},
		},
	}
}
