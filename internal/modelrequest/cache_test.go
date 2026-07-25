package modelrequest

import (
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func explicitMarkersSpec() *modelv1.ModelSpec {
	return &modelv1.ModelSpec{Caching: &modelv1.CachingSpec{Supported: true, Mode: modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS}}
}

func staticSection() *contentv1.ContextSection {
	return &contentv1.ContextSection{Provider: "project-context", Label: "CLAUDE.md", Stability: contentv1.Stability_STABILITY_STATIC}
}

func dynamicSection(label string) *contentv1.ContextSection {
	return &contentv1.ContextSection{Provider: "git-status", Label: label, Stability: contentv1.Stability_STABILITY_DYNAMIC}
}

// wantAfterAssembledContext reports whether got is exactly a single
// after_assembled_context breakpoint.
func wantAfterAssembledContext(t *testing.T, got []*CacheBreakpoint) {
	t.Helper()

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %+v", len(got), got)
	}
	if got[0].GetAfterAssembledContext() == nil {
		t.Fatalf("got[0] = %v, want an after_assembled_context breakpoint", got[0])
	}
}

// TestPlaceCacheBreakpointsWorkedExample transcribes
// model/examples.md's "A full StreamCompletion event sequence" request,
// which carries a single STABILITY_STATIC assembled_context section
// (a project-context CLAUDE.md contribution) and expects exactly the
// {after_assembled_context: {}} breakpoint the example's
// cache_breakpoints field shows.
func TestPlaceCacheBreakpointsWorkedExample(t *testing.T) {
	t.Parallel()

	sections := []*contentv1.ContextSection{staticSection()}
	messages := []*contentv1.Message{userMessage(textBlock("What's in main.go?"))}

	got := PlaceCacheBreakpoints(sections, messages, explicitMarkersSpec())
	wantAfterAssembledContext(t, got)
}

func TestPlaceCacheBreakpointsNonExplicitMarkersModes(t *testing.T) {
	t.Parallel()

	sections := []*contentv1.ContextSection{staticSection()}

	modes := []modelv1.CachingMode{
		modelv1.CachingMode_CACHING_MODE_NONE,
		modelv1.CachingMode_CACHING_MODE_IMPLICIT_AUTOMATIC,
		modelv1.CachingMode_CACHING_MODE_UNSPECIFIED,
	}
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()

			spec := &modelv1.ModelSpec{Caching: &modelv1.CachingSpec{Mode: mode}}
			got := PlaceCacheBreakpoints(sections, nil, spec)
			if got != nil {
				t.Fatalf("got %+v, want nil for caching mode %v", got, mode)
			}
		})
	}
}

func TestPlaceCacheBreakpointsNoStaticLeadingSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sections []*contentv1.ContextSection
	}{
		{name: "empty chain", sections: nil},
		{name: "leading section is dynamic", sections: []*contentv1.ContextSection{dynamicSection("git-status")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := PlaceCacheBreakpoints(tt.sections, nil, explicitMarkersSpec())
			if got != nil {
				t.Fatalf("got %+v, want nil", got)
			}
		})
	}
}

func TestPlaceCacheBreakpointsTrailingDynamicSectionStillMarksWholeChain(t *testing.T) {
	t.Parallel()

	// tools -> system -> static-project-context -> conversation-tail
	// ordering means a dynamic section (e.g. git status) can trail a
	// static one within the same assembled_context chain; the only
	// available marker still covers the whole chain.
	sections := []*contentv1.ContextSection{staticSection(), dynamicSection("git-status")}

	got := PlaceCacheBreakpoints(sections, nil, explicitMarkersSpec())
	wantAfterAssembledContext(t, got)
}

func TestPlaceCacheBreakpointsNilSpec(t *testing.T) {
	t.Parallel()

	sections := []*contentv1.ContextSection{staticSection()}
	got := PlaceCacheBreakpoints(sections, nil, nil)
	if got != nil {
		t.Fatalf("got %+v, want nil for a nil spec", got)
	}
}
