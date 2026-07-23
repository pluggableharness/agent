package agentprofile

import (
	"errors"
	"strings"
	"testing"
)

// sanitizeToolScopeElement strips characters ResolveTools's "<provider>.<tool>"
// grammar treats specially (the "." separator and the "*" wildcard marker),
// and falls back to a safe placeholder for the empty string, so a fuzzed
// string can be used to build a *guaranteed-well-formed* provider or tool
// name.
func sanitizeToolScopeElement(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '.' || r == '*' {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "x"
	}
	return s
}

// FuzzResolveTools exercises ResolveTools with both guaranteed-valid
// constructed scoping entries (concrete, wildcard, and known-provider/
// unknown-tool forms) and the raw fuzzed entry against an empty
// availability set.
//
// Invariants asserted:
//  1. "Valid input is never rejected": a concrete "<provider>.<tool>" entry
//     naming a provider/tool pair that IS advertised MUST always resolve
//     with no error and MUST include exactly that key — configuration.md
//     §8.3's documented happy path, for any provider/tool name.
//  2. The wildcard form "<provider>.*" for the same provider MUST resolve
//     to the same key.
//  3. A concrete entry naming a provider that IS loaded but a tool name
//     that provider does NOT advertise MUST always error, wrapping
//     ErrUnknownTool specifically (agentprofile/CLAUDE.md's documented
//     "typo in agent.hcl" case) — for any provider/tool name.
//  4. No panic on arbitrary fuzzed scoping entries against an empty
//     availability set, and any error returned always wraps
//     ErrMalformedToolScope (the only sentinel reachable with no loaded
//     providers at all).
func FuzzResolveTools(f *testing.F) {
	f.Add("filesystem", "read_file", "filesystem.read_file")
	f.Add("", "", "not.advertised.at.all")

	f.Fuzz(func(t *testing.T, providerRaw, toolRaw, rawEntry string) {
		provider := sanitizeToolScopeElement(providerRaw)
		tool := sanitizeToolScopeElement(toolRaw)
		available := map[string][]string{provider: {tool}}

		// Property 1: concrete entry naming an advertised tool always
		// resolves, never errors.
		concrete := provider + "." + tool
		got, err := ResolveTools([]string{concrete}, available)
		if err != nil {
			t.Fatalf("ResolveTools([%q], %v) unexpected error: %v", concrete, available, err)
		}
		if len(got) != 1 || !got[concrete] {
			t.Fatalf("ResolveTools([%q], %v) = %v, want {%q: true}", concrete, available, got, concrete)
		}

		// Property 2: wildcard form for the same provider resolves to the
		// same key.
		wildcard := provider + ".*"
		got, err = ResolveTools([]string{wildcard}, available)
		if err != nil {
			t.Fatalf("ResolveTools([%q], %v) unexpected error: %v", wildcard, available, err)
		}
		if len(got) != 1 || !got[concrete] {
			t.Fatalf("ResolveTools([%q], %v) = %v, want {%q: true}", wildcard, available, got, concrete)
		}

		// Property 3: a concrete entry for the same (loaded) provider but a
		// tool name that provider does NOT advertise always errors, and
		// specifically wraps ErrUnknownTool — the one sentinel property 4
		// below can never exercise, since that runs with no providers
		// loaded at all. unknownTool is guaranteed to differ from tool: the
		// sanitizer never produces the "_unknown" suffix on its own.
		unknownTool := tool + "_unknown"
		unknownEntry := provider + "." + unknownTool
		_, err = ResolveTools([]string{unknownEntry}, available)
		if !errors.Is(err, ErrUnknownTool) {
			t.Fatalf("ResolveTools([%q], %v) error = %v, want wrapping ErrUnknownTool", unknownEntry, available, err)
		}

		// Property 4: an arbitrary fuzzed entry against an empty
		// availability set must never panic, and any error it produces
		// must wrap ErrMalformedToolScope — with no provider loaded,
		// ErrUnknownTool is unreachable (a concrete entry for an unloaded
		// provider is a silent no-op, per ResolveTools's doc comment).
		_, err = ResolveTools([]string{rawEntry}, map[string][]string{})
		if err != nil && !errors.Is(err, ErrMalformedToolScope) {
			t.Fatalf("ResolveTools([%q], {}) returned error not wrapping ErrMalformedToolScope: %v", rawEntry, err)
		}
	})
}
