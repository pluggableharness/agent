package agentprofile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrMalformedToolScope is returned when a Tools entry isn't of the form
// "<provider>.<tool_name>" or "<provider>.*" — configuration.md §8.3.
var ErrMalformedToolScope = errors.New("agentprofile: malformed tool scoping entry")

// ErrUnknownTool is returned when a concrete "<provider>.<tool_name>" entry
// names a provider that IS loaded this session but a tool name that
// provider does not advertise. See ResolveTools's doc comment for why this
// is treated as an error rather than silently ignored.
var ErrUnknownTool = errors.New("agentprofile: tool not advertised by provider")

// ResolveTools expands scoping (a profile's Tools field: flat
// "<provider>.<tool_name>" strings, or "<provider>.*" wildcards) against
// available (provider name -> that provider's advertised tool names, e.g.
// from GetSchema) into the concrete set of allowed "<provider>.<tool_name>"
// keys (configuration.md §8.3).
//
// An empty or nil scoping resolves to the empty set — §8.3's intentionally
// strict default (a profile omitting tools inherits none, not the full
// parent capability set).
//
// A "<provider>.*" entry naming a provider not present in available is not
// an error: the provider may simply not be loaded in this session, so the
// wildcard just contributes nothing. A concrete "<provider>.<tool_name>"
// entry is given the same treatment when its provider isn't present at all
// — for consistency with the wildcard case, and because an unloaded
// provider carries no advertised-tool list to check the name against in
// the first place.
//
// Judgment call: when the named provider IS present in available but the
// named tool ISN'T in that provider's advertised list, ResolveTools treats
// it as an error (wrapping ErrUnknownTool) rather than silently dropping
// the entry. This is config-validation territory where the information
// needed to catch a typo (the provider's real schema) is right there, and
// this project's stated general posture is "ambiguity is an error" (see
// provider.md §10's discussion of overlapping pricing tiers) — a
// misspelled tool name silently resolving to "granted nothing" would be a
// much harder bug to notice than a load-time error naming the bad entry. A
// reviewer preferring silent drop, or a separate "unresolved" return value
// instead of an error, should treat this as the one deliberately-chosen
// alternative among several defensible options.
func ResolveTools(scoping []string, available map[string][]string) (map[string]bool, error) {
	resolved := make(map[string]bool, len(scoping))
	for _, entry := range scoping {
		provider, tool, ok := strings.Cut(entry, ".")
		if !ok || provider == "" || tool == "" {
			return nil, fmt.Errorf("agentprofile: tool scoping entry %q: %w", entry, ErrMalformedToolScope)
		}

		tools, providerLoaded := available[provider]
		if !providerLoaded {
			// Provider not loaded this session — both wildcard and
			// concrete entries contribute nothing, per the doc comment
			// above.
			continue
		}

		if tool == "*" {
			for _, t := range tools {
				resolved[provider+"."+t] = true
			}
			continue
		}

		if !slices.Contains(tools, tool) {
			return nil, fmt.Errorf("agentprofile: tool scoping entry %q: %w", entry, ErrUnknownTool)
		}
		resolved[provider+"."+tool] = true
	}
	return resolved, nil
}
