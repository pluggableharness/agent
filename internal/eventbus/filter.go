package eventbus

import "strings"

// wildcardSuffix is the one wildcard form this package recognizes: a
// filter ending in "*" matches every topic sharing the filter's prefix
// (docs/specifications/event-bus.md#filter-grammar). No mid-string or
// multi-segment wildcard exists — a "*" appearing anywhere but the very
// end of a filter is not treated specially at all, it's simply part of
// the filter string an exact-match lookup would have to hit literally
// (which, in practice, no real topic ever will, since real topics never
// contain "*" — see event-bus.md's topic grammar).
const wildcardSuffix = "*"

// isWildcardFilter reports whether filter is a trailing-wildcard prefix
// filter (ends in "*") rather than an exact-topic filter. This package
// takes the permissive reading — any trailing "*" counts, not only one
// immediately preceded by "." — leaving the stricter wire-level grammar
// (event-bus.md's "ending in .*", whole segments only) to be validated by
// the RPC boundary that constructs filters from a wire SubscribeRequest,
// not by this generic pub/sub primitive.
func isWildcardFilter(filter string) bool {
	return strings.HasSuffix(filter, wildcardSuffix)
}

// wildcardPrefix returns the prefix a wildcard filter matches against —
// filter with its trailing "*" removed. Only meaningful when
// isWildcardFilter(filter) is true.
func wildcardPrefix(filter string) string {
	return strings.TrimSuffix(filter, wildcardSuffix)
}

// matchesFilter reports whether topic satisfies filter — either an exact
// string match, or, for a wildcard filter, a prefix match against
// wildcardPrefix(filter). A bare "*" filter (prefix "") matches every
// topic.
func matchesFilter(topic, filter string) bool {
	if isWildcardFilter(filter) {
		return strings.HasPrefix(topic, wildcardPrefix(filter))
	}
	return topic == filter
}
