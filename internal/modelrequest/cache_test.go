package modelrequest

import (
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func explicitMarkersSpec() *modelv1.ModelSpec {
	return &modelv1.ModelSpec{Caching: &modelv1.CachingSpec{Supported: true, ExplicitMarkers: true}}
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

func TestPlaceCacheBreakpointsWithoutExplicitMarkers(t *testing.T) {
	t.Parallel()

	sections := []*contentv1.ContextSection{staticSection()}

	specs := map[string]*modelv1.CachingSpec{
		"no caching at all":  {},
		"implicit only":      {Supported: true, ImplicitAutomatic: true},
		"supported but bare": {Supported: true},
	}
	for name, caching := range specs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := &modelv1.ModelSpec{Caching: caching}
			got := PlaceCacheBreakpoints(sections, nil, spec)
			if got != nil {
				t.Fatalf("got %+v, want nil when explicit_markers is false", got)
			}
		})
	}
}

func TestPlaceCacheBreakpointsBothCachingAxesStillPlacesBreakpoints(t *testing.T) {
	t.Parallel()

	// A model running implicit caching by default AND accepting explicit
	// markers must still get breakpoints. Under the earlier single-mode
	// enum this model could only declare IMPLICIT_AUTOMATIC, which gated
	// breakpoints off entirely and silently discarded a cache discount it
	// was eligible for.
	sections := []*contentv1.ContextSection{staticSection()}
	spec := &modelv1.ModelSpec{Caching: &modelv1.CachingSpec{
		Supported:         true,
		ExplicitMarkers:   true,
		ImplicitAutomatic: true,
	}}

	got := PlaceCacheBreakpoints(sections, nil, spec)
	wantAfterAssembledContext(t, got)
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
