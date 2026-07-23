package agentprofile

import "testing"

// intPtr returns a pointer to v, for populating AgentProfile.MaxDepth in
// tests without a named local variable at every call site.
func intPtr(v int) *int {
	return &v
}

func TestRootRemainingDepth(t *testing.T) {
	tests := []struct {
		name          string
		profile       AgentProfile
		kernelDefault int
		want          int
	}{
		{
			name:          "max_depth set, overrides kernel default",
			profile:       AgentProfile{MaxDepth: intPtr(3)},
			kernelDefault: 10,
			want:          3,
		},
		{
			name:          "max_depth unset, falls back to kernel default",
			profile:       AgentProfile{MaxDepth: nil},
			kernelDefault: 10,
			want:          10,
		},
		{
			name:          "max_depth explicitly zero is honored, not treated as unset",
			profile:       AgentProfile{MaxDepth: intPtr(0)},
			kernelDefault: 10,
			want:          0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RootRemainingDepth(tt.profile, tt.kernelDefault)
			if got != tt.want {
				t.Errorf("RootRemainingDepth(%+v, %d) = %d, want %d", tt.profile, tt.kernelDefault, got, tt.want)
			}
		})
	}
}

func TestChildRemainingDepth(t *testing.T) {
	tests := []struct {
		name            string
		parentRemaining int
		childProfile    AgentProfile
		want            int
	}{
		{
			name:            "child has no max_depth of its own, just decrements parent",
			parentRemaining: 5,
			childProfile:    AgentProfile{MaxDepth: nil},
			want:            4,
		},
		{
			name:            "child's own ceiling is tighter than parent's remaining budget",
			parentRemaining: 5,
			childProfile:    AgentProfile{MaxDepth: intPtr(1)},
			want:            1,
		},
		{
			name:            "child's own ceiling is looser than parent's remaining budget - parent wins",
			parentRemaining: 5,
			childProfile:    AgentProfile{MaxDepth: intPtr(100)},
			want:            4,
		},
		{
			name:            "parent already exhausted, child inherits non-positive budget",
			parentRemaining: 0,
			childProfile:    AgentProfile{MaxDepth: intPtr(100)},
			want:            -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ChildRemainingDepth(tt.parentRemaining, tt.childProfile)
			if got != tt.want {
				t.Errorf("ChildRemainingDepth(%d, %+v) = %d, want %d", tt.parentRemaining, tt.childProfile, got, tt.want)
			}
		})
	}
}

// TestDepthAcrossGenerations confirms the three-generation invariant called
// out in the task: the budget only ever shrinks going down the tree, and an
// ancestor's tighter cap propagates all the way down even when a deeper
// profile declares a larger max_depth of its own (configuration.md §8.4).
func TestDepthAcrossGenerations(t *testing.T) {
	t.Parallel()

	rootProfile := AgentProfile{MaxDepth: intPtr(2)} // tight ancestor cap
	childProfile := AgentProfile{MaxDepth: intPtr(50)}
	grandchildProfile := AgentProfile{MaxDepth: intPtr(100)}

	rootRemaining := RootRemainingDepth(rootProfile, 10)
	if rootRemaining != 2 {
		t.Fatalf("root remaining = %d, want 2", rootRemaining)
	}

	childRemaining := ChildRemainingDepth(rootRemaining, childProfile)
	if childRemaining != 1 {
		t.Fatalf("child remaining = %d, want 1 (root's cap - 1, despite child's own max_depth=50)", childRemaining)
	}

	grandchildRemaining := ChildRemainingDepth(childRemaining, grandchildProfile)
	if grandchildRemaining != 0 {
		t.Fatalf("grandchild remaining = %d, want 0 (root's tight cap still wins, despite grandchild's own max_depth=100)", grandchildRemaining)
	}

	if grandchildRemaining >= childRemaining || childRemaining >= rootRemaining {
		t.Fatalf("budget did not strictly shrink each generation: root=%d child=%d grandchild=%d", rootRemaining, childRemaining, grandchildRemaining)
	}
}

func TestChildRemainingDepth_unboundedWhenUnset(t *testing.T) {
	t.Parallel()

	// With no ceiling of its own, the child should be bound purely by the
	// parent's remaining budget, however large that budget is.
	got := ChildRemainingDepth(unboundedDepth, AgentProfile{MaxDepth: nil})
	want := unboundedDepth - 1
	if got != want {
		t.Errorf("ChildRemainingDepth(unboundedDepth, unset) = %d, want %d", got, want)
	}
}
