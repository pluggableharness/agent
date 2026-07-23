package agentprofile

import "math"

// unboundedDepth stands in for "+inf" in configuration.md §8.4's
// remaining_depth(child) formula when a profile declares no max_depth of
// its own. math.MaxInt is large enough that it never becomes the binding
// term in the min() against any realistic parent budget, while staying a
// plain int so callers never need to special-case an actual infinity value.
const unboundedDepth = math.MaxInt

// RootRemainingDepth implements configuration.md §8.4's
//
//	remaining_depth(root_session) = root_profile.max_depth ?? kernel_default_max_depth
//
// for the profile used by the root/interactive session (configuration.md
// §8.1 — ordinarily the profile named "default"). If profile.MaxDepth is
// unset, kernelDefault (the kernel's own configured default_max_depth)
// applies.
func RootRemainingDepth(profile AgentProfile, kernelDefault int) int {
	if profile.MaxDepth != nil {
		return *profile.MaxDepth
	}
	return kernelDefault
}

// ChildRemainingDepth implements configuration.md §8.4's
//
//	remaining_depth(child) = min(remaining_depth(parent) - 1,
//	                              child_profile.max_depth ?? +inf)
//
// for a sub-agent session spawned from a parent whose own remaining budget
// is parentRemaining. An ancestor's tighter budget always wins going down:
// childProfile.MaxDepth is a ceiling the child never exceeds on its own, but
// it is never a guarantee the child gets that much depth — a permissive
// child profile spawned deep in an already-deep tree cannot use its own
// generous max_depth to smuggle past an ancestor's tighter one. Called once
// per generation, so a grandchild's budget is
// ChildRemainingDepth(ChildRemainingDepth(root, childProfile), grandchildProfile).
func ChildRemainingDepth(parentRemaining int, childProfile AgentProfile) int {
	ownCeiling := unboundedDepth
	if childProfile.MaxDepth != nil {
		ownCeiling = *childProfile.MaxDepth
	}
	return min(parentRemaining-1, ownCeiling)
}
