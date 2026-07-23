package registry

import "strings"

// MirrorTable redirects specific provider source-address prefixes to
// alternate registry URLs, falling back to Default for anything unmatched.
// configuration.md §10.
type MirrorTable struct {
	// The fallback URL for any source prefix not matched by a Mirror.
	Default string

	// Zero or more prefix redirects. Order doesn't matter for resolution
	// (Resolve picks the longest matching prefix regardless of slice
	// order) but is preserved from the source file for display/audit.
	Mirrors []Mirror
}

// Mirror redirects one source-address prefix to URL, optionally through
// its own auth. configuration.md §10.
type Mirror struct {
	// The source-address prefix this mirror applies to, e.g.
	// "github.com/agentco/".
	Prefix string

	// The URL to fetch matching sources through.
	URL string

	// Optional bearer/auth value. When set, MUST have been an env(...)
	// expression in the source file (internal/hclsecret enforces this at
	// decode time, identically to a provider's sensitive attributes) —
	// literal tokens are forbidden here exactly as in a provider{} block.
	Auth string
}

// Resolve returns the URL a given provider source address should be
// fetched through: the URL of the longest-prefix-matching Mirror, or
// Default if no mirror's prefix matches. Longest-prefix-wins mirrors the
// same "most specific applicable rule governs" philosophy as the policy
// engine's specificity ordering (internal/policy).
func (rm MirrorTable) Resolve(source string) string {
	url := rm.Default
	bestLen := -1
	for _, m := range rm.Mirrors {
		if strings.HasPrefix(source, m.Prefix) && len(m.Prefix) > bestLen {
			bestLen = len(m.Prefix)
			url = m.URL
		}
	}
	return url
}
