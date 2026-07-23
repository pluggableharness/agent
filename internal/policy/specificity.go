package policy

import (
	"errors"
	"fmt"
)

// ErrConflictingRules is returned by ValidateRules when two rules have an
// identical specificity tuple and agree on every match field they both
// specify — configuration.md §7.2's refined conflict rule.
var ErrConflictingRules = errors.New("policy: conflicting rules")

// specificity reports, for each of Match's four fields in
// most-to-least-specific order (tool_name, provider, risk, kind — per
// configuration.md §7.2), whether m specifies that field. The result is
// compared with moreSpecific to pick a winner among several rules matching
// the same call.
func specificity(m Match) [4]bool {
	return [4]bool{m.ToolName != nil, m.Provider != nil, m.Risk != nil, m.Kind != nil}
}

// moreSpecific reports whether a wins over b under configuration.md §7.2's
// specificity ordering: a lexicographic comparison over the 4-tuple, most
// specific field first, where specifying a field (true) beats not
// specifying it (false) at the first position the two tuples differ. Equal
// tuples report false either way — neither is more specific than the
// other; ValidateRules is what rejects that case for rules that would
// otherwise both match the same call.
func moreSpecific(a, b [4]bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i]
		}
	}
	return false
}

// Conflicts reports whether match criteria a and b are conflicting and
// indistinguishable per configuration.md §7.2's corrected rule: they
// conflict if and only if they share an identical specificity tuple AND,
// for every field both a and b specify, the values are equal. A field only
// one of the two specifies never disqualifies a conflict; only shared
// fields must agree. This intentionally does NOT flag rules like
// `tool_name = "read_file"` vs. `tool_name = "write_file"` (same tuple,
// disjoint values — a real call can only ever match one of them) as
// conflicting, correcting the false positive the original, unrefined §7.2
// wording would have produced.
func Conflicts(a, b Match) bool {
	if specificity(a) != specificity(b) {
		return false
	}
	if a.ToolName != nil && b.ToolName != nil && *a.ToolName != *b.ToolName {
		return false
	}
	if a.Provider != nil && b.Provider != nil && *a.Provider != *b.Provider {
		return false
	}
	if a.Risk != nil && b.Risk != nil && *a.Risk != *b.Risk {
		return false
	}
	if a.Kind != nil && b.Kind != nil && *a.Kind != *b.Kind {
		return false
	}
	return true
}

// ValidateRules scans rules pairwise (O(n²) — policy rule counts are small
// enough that nothing cleverer is warranted) and returns an error wrapping
// ErrConflictingRules, naming both rules involved, if any pair conflicts
// per Conflicts. If more than one pair conflicts, ValidateRules returns as
// soon as it finds the first one rather than enumerating every conflicting
// pair — configuration.md §7.2 only requires that a conflict be caught at
// config-load time, not that every conflict be reported in one pass.
func ValidateRules(rules []Rule) error {
	for i := range rules {
		for j := i + 1; j < len(rules); j++ {
			if Conflicts(rules[i].Match, rules[j].Match) {
				return fmt.Errorf("policy: rules %q and %q have conflicting, indistinguishable match criteria: %w",
					rules[i].Name, rules[j].Name, ErrConflictingRules)
			}
		}
	}
	return nil
}

// IsEmptyMatch reports whether m specifies no fields at all — a legal
// `match = {}` that matches every call (configuration.md §7.1). This
// package has no logging facility of its own (doc.go); a caller decoding
// policy blocks is expected to call IsEmptyMatch on each rule's Match and
// log the config-load-time warning configuration.md §7.1 requires.
func IsEmptyMatch(m Match) bool {
	return m.ToolName == nil && m.Provider == nil && m.Risk == nil && m.Kind == nil
}
