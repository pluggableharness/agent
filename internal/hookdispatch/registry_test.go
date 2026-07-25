package hookdispatch

import (
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/providercatalog"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

const testDefaultTimeout = 2 * time.Second

func TestNewRegistryDeclarationOrder(t *testing.T) {
	t.Parallel()

	// One file, four subscriptions declared out of catalog order: two
	// implicit (positioned by their provider{} block) interleaved with two
	// explicit (positioned by their hook{} block).
	cat := newCatalog(t,
		catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL}},
		catalogEntry{provider: "memory", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL}},
		catalogEntry{provider: "redactor", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL}},
		catalogEntry{provider: "tracer", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL}},
	)

	ranges := map[string]hcl.Range{
		"memory": rangeAt("agent.hcl", 300),
		"tracer": rangeAt("agent.hcl", 100),
	}
	implicit := []Implicit{
		{Provider: "memory", Point: commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, Mode: hookv1.HookMode_HOOK_MODE_OBSERVE},
		{Provider: "tracer", Point: commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, Mode: hookv1.HookMode_HOOK_MODE_OBSERVE},
	}
	hooks := []config.Hook{
		{Point: "post-tool-call", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 400)},
		{Point: "post-tool-call", Provider: "redactor", Mode: "observe", Range: rangeAt("agent.hcl", 200)},
	}

	reg, err := NewRegistry(cat, implicit, hooks, ranges, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	want := []string{"tracer", "redactor", "memory", "audit"}
	assertChainOrder(t, reg, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, want)

	chain := reg.Subscribers(commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL)
	wantOrigins := []Origin{OriginImplicit, OriginExplicit, OriginImplicit, OriginExplicit}
	for i, sub := range chain {
		if sub.Origin != wantOrigins[i] {
			t.Errorf("chain[%d] (%s) origin = %s, want %s", i, sub.Provider, sub.Origin, wantOrigins[i])
		}
		if sub.Timeout != testDefaultTimeout {
			t.Errorf("chain[%d] (%s) timeout = %s, want %s", i, sub.Provider, sub.Timeout, testDefaultTimeout)
		}
	}
}

func TestNewRegistryMultiFileOrder(t *testing.T) {
	t.Parallel()

	// architecture.md's XDG layout permits other *.hcl in the project dir.
	// Ordering across them is lexicographic by filename first, byte offset
	// second — never filesystem enumeration order. Declaring the
	// late-sorting file first proves the sort, not the input order.
	cat := newCatalog(t,
		catalogEntry{provider: "zeta", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}},
		catalogEntry{provider: "alpha", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}},
		catalogEntry{provider: "middle", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}},
	)

	hooks := []config.Hook{
		// zed.hcl sorts last despite being declared first and starting at
		// byte 0.
		{Point: "session-start", Provider: "zeta", Mode: "observe", Range: rangeAt("zed.hcl", 0)},
		// A far byte offset in the first-sorting file still precedes byte
		// 0 of a later-sorting file.
		{Point: "session-start", Provider: "alpha", Mode: "observe", Range: rangeAt("agent.hcl", 9000)},
		{Point: "session-start", Provider: "middle", Mode: "observe", Range: rangeAt("extra.hcl", 10)},
	}

	reg, err := NewRegistry(cat, nil, hooks, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	assertChainOrder(t, reg, commonv1.HookPoint_HOOK_POINT_SESSION_START, []string{"alpha", "middle", "zeta"})

	chain := reg.Subscribers(commonv1.HookPoint_HOOK_POINT_SESSION_START)
	wantFileIndex := []int{0, 1, 2}
	for i, sub := range chain {
		if sub.Position.FileIndex != wantFileIndex[i] {
			t.Errorf("chain[%d] (%s) FileIndex = %d, want %d", i, sub.Provider, sub.Position.FileIndex, wantFileIndex[i])
		}
	}
}

func TestNewRegistryPerPointChains(t *testing.T) {
	t.Parallel()

	// One provider subscribing at two points is not a duplicate, and each
	// point gets its own chain.
	cat := newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{
		commonv1.HookPoint_HOOK_POINT_SESSION_START,
		commonv1.HookPoint_HOOK_POINT_SESSION_END,
	}})

	hooks := []config.Hook{
		{Point: "session-start", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 10)},
		{Point: "session-end", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 40)},
	}

	reg, err := NewRegistry(cat, nil, hooks, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	assertChainOrder(t, reg, commonv1.HookPoint_HOOK_POINT_SESSION_START, []string{"audit"})
	assertChainOrder(t, reg, commonv1.HookPoint_HOOK_POINT_SESSION_END, []string{"audit"})
	assertChainOrder(t, reg, commonv1.HookPoint_HOOK_POINT_PLAN_READY, nil)
}

func TestNewRegistryTimeoutOverride(t *testing.T) {
	t.Parallel()

	cat := newCatalog(t, catalogEntry{provider: "slow", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})

	override := 250
	zero := 0
	hooks := []config.Hook{
		{Point: "session-start", Provider: "slow", Mode: "observe", TimeoutMS: &override, Range: rangeAt("agent.hcl", 10)},
	}

	reg, err := NewRegistry(cat, nil, hooks, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := reg.Subscribers(commonv1.HookPoint_HOOK_POINT_SESSION_START)[0].Timeout; got != 250*time.Millisecond {
		t.Errorf("timeout = %s, want 250ms", got)
	}

	// timeout_ms = 0 is a declaration, not an omission — it must not fall
	// back to the default (internal/config's Hook.TimeoutMS is *int for
	// exactly this reason).
	hooks[0].TimeoutMS = &zero
	reg, err = NewRegistry(cat, nil, hooks, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := reg.Subscribers(commonv1.HookPoint_HOOK_POINT_SESSION_START)[0].Timeout; got != 0 {
		t.Errorf("timeout = %s, want 0", got)
	}
}

func TestNewRegistryRejections(t *testing.T) {
	t.Parallel()

	negative := -1

	tests := []struct {
		name     string
		cat      func(t *testing.T) providercatalog.Catalog
		implicit []Implicit
		hooks    []config.Hook
		ranges   map[string]hcl.Range
		timeout  time.Duration
		want     error
	}{
		{
			name: "point not advertised by provider",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "post-tool-call", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 10)}},
			want:  ErrPointNotAdvertised,
		},
		{
			name: "veto at non-veto-bearing point",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "gate", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_APPLY}})
			},
			hooks: []config.Hook{{Point: "post-apply", Provider: "gate", Mode: "veto", Range: rangeAt("agent.hcl", 10)}},
			want:  ErrVetoNotPermitted,
		},
		{
			name: "duplicate provider at one point across implicit and explicit",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "memory", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_END}})
			},
			implicit: []Implicit{{Provider: "memory", Point: commonv1.HookPoint_HOOK_POINT_SESSION_END, Mode: hookv1.HookMode_HOOK_MODE_OBSERVE}},
			hooks:    []config.Hook{{Point: "session-end", Provider: "memory", Mode: "observe", Range: rangeAt("agent.hcl", 50)}},
			ranges:   map[string]hcl.Range{"memory": rangeAt("agent.hcl", 10)},
			want:     ErrDuplicateSubscription,
		},
		{
			name: "duplicate explicit subscription",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_END}})
			},
			hooks: []config.Hook{
				{Point: "session-end", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 10)},
				{Point: "session-end", Provider: "audit", Mode: "transform", Range: rangeAt("agent.hcl", 60)},
			},
			want: ErrDuplicateSubscription,
		},
		{
			name: "unknown hook point label",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "pre-flight-check", Provider: "audit", Mode: "observe", Range: rangeAt("agent.hcl", 10)}},
			want:  ErrUnknownPoint,
		},
		{
			name: "context-assemble is not dispatchable here",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "ctx", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "context-assemble", Provider: "ctx", Mode: "transform", Range: rangeAt("agent.hcl", 10)}},
			want:  ErrUnknownPoint,
		},
		{
			name: "unknown mode",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "session-start", Provider: "audit", Mode: "advise", Range: rangeAt("agent.hcl", 10)}},
			want:  ErrUnknownMode,
		},
		{
			name: "implicit mode unset",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "memory", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_END}})
			},
			implicit: []Implicit{{Provider: "memory", Point: commonv1.HookPoint_HOOK_POINT_SESSION_END}},
			ranges:   map[string]hcl.Range{"memory": rangeAt("agent.hcl", 10)},
			want:     ErrUnknownMode,
		},
		{
			name: "implicit point unset",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "memory", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_END}})
			},
			implicit: []Implicit{{Provider: "memory", Mode: hookv1.HookMode_HOOK_MODE_OBSERVE}},
			ranges:   map[string]hcl.Range{"memory": rangeAt("agent.hcl", 10)},
			want:     ErrUnknownPoint,
		},
		{
			name: "implicit subscription with no provider range",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "memory", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_END}})
			},
			implicit: []Implicit{{Provider: "memory", Point: commonv1.HookPoint_HOOK_POINT_SESSION_END, Mode: hookv1.HookMode_HOOK_MODE_OBSERVE}},
			want:     ErrMissingPosition,
		},
		{
			name: "explicit subscription with no range",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "session-start", Provider: "audit", Mode: "observe"}},
			want:  ErrMissingPosition,
		},
		{
			name: "negative per-subscriber timeout",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t, catalogEntry{provider: "audit", points: []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START}})
			},
			hooks: []config.Hook{{Point: "session-start", Provider: "audit", Mode: "observe", TimeoutMS: &negative, Range: rangeAt("agent.hcl", 10)}},
			want:  ErrInvalidTimeout,
		},
		{
			name: "non-positive default timeout",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t)
			},
			timeout: -1,
			want:    ErrInvalidTimeout,
		},
		{
			name: "provider absent from catalog",
			cat: func(t *testing.T) providercatalog.Catalog {
				return newCatalog(t)
			},
			hooks: []config.Hook{{Point: "session-start", Provider: "ghost", Mode: "observe", Range: rangeAt("agent.hcl", 10)}},
			want:  providercatalog.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			timeout := tt.timeout
			if timeout == 0 {
				timeout = testDefaultTimeout
			}

			reg, err := NewRegistry(tt.cat(t), tt.implicit, tt.hooks, tt.ranges, timeout)
			if err == nil {
				t.Fatalf("NewRegistry returned nil error, want %v", tt.want)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("NewRegistry error = %v, want errors.Is %v", err, tt.want)
			}
			if reg != nil {
				t.Errorf("NewRegistry returned a non-nil registry alongside an error")
			}
		})
	}
}

func TestNewRegistryNilCatalog(t *testing.T) {
	t.Parallel()

	if _, err := NewRegistry(nil, nil, nil, nil, testDefaultTimeout); err == nil {
		t.Fatal("NewRegistry with a nil catalog returned nil error")
	}
}

func TestNewRegistryVetoAtVetoBearingPoints(t *testing.T) {
	t.Parallel()

	for _, point := range []commonv1.HookPoint{
		commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
	} {
		text, _ := PointText(point)
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			cat := newCatalog(t, catalogEntry{provider: "gate", points: []commonv1.HookPoint{point}})
			hooks := []config.Hook{{Point: text, Provider: "gate", Mode: "veto", Range: rangeAt("agent.hcl", 10)}}

			reg, err := NewRegistry(cat, nil, hooks, nil, testDefaultTimeout)
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			if got := reg.Subscribers(point)[0].Mode; got != hookv1.HookMode_HOOK_MODE_VETO {
				t.Errorf("mode = %v, want HOOK_MODE_VETO", got)
			}
		})
	}
}

func TestRegistryPin(t *testing.T) {
	t.Parallel()

	reg, err := NewRegistry(newCatalog(t), nil, nil, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if _, ok := reg.PinnedVeto(commonv1.HookPoint_HOOK_POINT_PLAN_READY); ok {
		t.Fatal("PinnedVeto reported a veto before Pin was called")
	}

	v := &fakeVeto{name: "policy", decision: hookv1.HookDecision_HOOK_DECISION_ALLOW}
	reg.Pin(commonv1.HookPoint_HOOK_POINT_PLAN_READY, v)

	got, ok := reg.PinnedVeto(commonv1.HookPoint_HOOK_POINT_PLAN_READY)
	if !ok {
		t.Fatal("PinnedVeto reported no veto after Pin")
	}
	if got.Name() != "policy" {
		t.Errorf("pinned veto name = %q, want %q", got.Name(), "policy")
	}
}

func TestRegistryPinPanicsAtNonVetoBearingPoint(t *testing.T) {
	t.Parallel()

	reg, err := NewRegistry(newCatalog(t), nil, nil, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Error("Pin at a non-veto-bearing point did not panic")
		}
	}()
	reg.Pin(commonv1.HookPoint_HOOK_POINT_POST_APPLY, &fakeVeto{name: "policy"})
}

func TestPointAndModeVocabulary(t *testing.T) {
	t.Parallel()

	// The eight dispatchable points of hook-dispatch.md#hook-points —
	// every HookPoint except context-assemble.
	for _, point := range []commonv1.HookPoint{
		commonv1.HookPoint_HOOK_POINT_SESSION_START,
		commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
		commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE,
		commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
		commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		commonv1.HookPoint_HOOK_POINT_POST_APPLY,
		commonv1.HookPoint_HOOK_POINT_SESSION_END,
	} {
		text, ok := PointText(point)
		if !ok {
			t.Errorf("PointText(%v) reported not ok", point)
			continue
		}
		back, err := pointFromText(text)
		if err != nil {
			t.Errorf("pointFromText(%q): %v", text, err)
			continue
		}
		if back != point {
			t.Errorf("round trip of %v via %q yielded %v", point, text, back)
		}
	}

	if _, ok := PointText(commonv1.HookPoint_HOOK_POINT_UNSPECIFIED); ok {
		t.Error("PointText reported ok for HOOK_POINT_UNSPECIFIED")
	}

	for _, mode := range []hookv1.HookMode{
		hookv1.HookMode_HOOK_MODE_OBSERVE,
		hookv1.HookMode_HOOK_MODE_TRANSFORM,
		hookv1.HookMode_HOOK_MODE_VETO,
	} {
		text, ok := ModeText(mode)
		if !ok {
			t.Errorf("ModeText(%v) reported not ok", mode)
			continue
		}
		back, err := modeFromText(text)
		if err != nil {
			t.Errorf("modeFromText(%q): %v", text, err)
			continue
		}
		if back != mode {
			t.Errorf("round trip of %v via %q yielded %v", mode, text, back)
		}
	}

	if _, ok := ModeText(hookv1.HookMode_HOOK_MODE_UNSPECIFIED); ok {
		t.Error("ModeText reported ok for HOOK_MODE_UNSPECIFIED")
	}
}

func TestIsVetoBearing(t *testing.T) {
	t.Parallel()

	tests := map[commonv1.HookPoint]bool{
		commonv1.HookPoint_HOOK_POINT_PLAN_READY:          true,
		commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL:       true,
		commonv1.HookPoint_HOOK_POINT_SESSION_START:       false,
		commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL:      false,
		commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE: false,
		commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL:      false,
		commonv1.HookPoint_HOOK_POINT_POST_APPLY:          false,
		commonv1.HookPoint_HOOK_POINT_SESSION_END:         false,
		commonv1.HookPoint_HOOK_POINT_UNSPECIFIED:         false,
	}

	for point, want := range tests {
		if got := IsVetoBearing(point); got != want {
			t.Errorf("IsVetoBearing(%v) = %t, want %t", point, got, want)
		}
	}
}

func TestOriginString(t *testing.T) {
	t.Parallel()

	tests := map[Origin]string{
		OriginImplicit: "implicit",
		OriginExplicit: "explicit",
		OriginKernel:   "kernel",
		Origin(42):     "Origin(42)",
	}

	for origin, want := range tests {
		if got := origin.String(); got != want {
			t.Errorf("Origin(%d).String() = %q, want %q", int(origin), got, want)
		}
	}
}

// assertChainOrder checks that point's chain names exactly want, in
// order.
func assertChainOrder(t *testing.T, reg *Registry, point commonv1.HookPoint, want []string) {
	t.Helper()

	chain := reg.Subscribers(point)
	if len(chain) != len(want) {
		t.Fatalf("chain length = %d, want %d (%v)", len(chain), len(want), providerNames(chain))
	}
	for i, name := range want {
		if chain[i].Provider != name {
			t.Fatalf("chain order = %v, want %v", providerNames(chain), want)
		}
	}
}

// providerNames lists a chain's provider names, for failure messages.
func providerNames(chain []Subscriber) []string {
	out := make([]string, 0, len(chain))
	for _, sub := range chain {
		out = append(out, sub.Provider)
	}
	return out
}
