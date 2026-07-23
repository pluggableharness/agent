package hclsecret

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// hclQuoteString renders s as an HCL native-syntax double-quoted string
// literal, guaranteed to parse back to exactly s. strconv.Quote is NOT a
// substitute here: it emits Go-only escapes (\xHH, \a, \b, \v, octal) that
// HCL's grammar doesn't recognize, so a Go-quoted control character fails
// to parse as HCL (github.com/hashicorp/hcl/v2/hclsyntax only recognizes
// \n \r \t \" \\ \uXXXX \UXXXXXXXX and the $${ / %%{ interpolation
// escapes). Every character outside that control-character/quote/backslash
// set is legal to embed in an HCL string literal completely unescaped,
// including arbitrary non-ASCII Unicode.
func hclQuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case (r == '$' || r == '%') && i+1 < len(runes) && runes[i+1] == '{':
			// "${" / "%{" begin template interpolation/control sequences;
			// doubling the leading character escapes it per HCL's spec.
			b.WriteRune(r)
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// FuzzValidateSensitiveExpr exercises ValidateSensitiveExpr against both a
// syntactically-guaranteed-valid env(name) call (built from the fuzzed name
// via hclQuoteString, so it always parses as a single string-literal
// argument) and the raw fuzzed source parsed as an arbitrary expression.
//
// Invariants asserted:
//  1. "Valid input is never rejected": for ANY name string, the exact
//     canonical form env("<name>") — configuration.md §4's only legal
//     shape — MUST validate with a nil error. This is the strong property
//     go-testing.md prefers over a bare no-panic check.
//  2. No panic on arbitrary attacker-controlled HCL source, valid or not.
//  3. ValidateSensitiveExpr never returns a non-nil error that doesn't wrap
//     ErrInvalidSensitiveExpr — its documented error contract.
func FuzzValidateSensitiveExpr(f *testing.F) {
	f.Add("ANTHROPIC_API_KEY", `env("ANTHROPIC_API_KEY")`)
	f.Add("", `coalesce(env("X"), "${1+1}")`)

	f.Fuzz(func(t *testing.T, name string, rawExpr string) {
		// Property 1: the canonical env(name) form, for any name
		// whatsoever (including empty, quotes, backslashes, unicode),
		// must always be accepted once safely quoted.
		validSrc := "x = env(" + hclQuoteString(name) + ")\n"
		validFile, diags := hclsyntax.ParseConfig([]byte(validSrc), "fuzz-valid.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("strconv.Quote-ed env() call failed to parse: %q: %v", validSrc, diags)
		}
		attrs, diags := validFile.Body.JustAttributes()
		if diags.HasErrors() {
			t.Fatalf("JustAttributes on canonical form: %v", diags)
		}
		attr, ok := attrs["x"]
		if !ok {
			t.Fatalf("no attribute %q found in canonical form %q", "x", validSrc)
		}
		if err := ValidateSensitiveExpr(attr); err != nil {
			t.Fatalf("ValidateSensitiveExpr(canonical env(%q)) = %v, want nil", name, err)
		}

		// Property 2/3: arbitrary fuzzed expression source must never
		// panic, and any error it does produce must wrap the documented
		// sentinel.
		src := "x = " + rawExpr + "\n"
		rawFile, diags := hclsyntax.ParseConfig([]byte(src), "fuzz-raw.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			return // not our concern here — the parser already rejected it
		}
		attrs, diags = rawFile.Body.JustAttributes()
		if diags.HasErrors() {
			return
		}
		attr, ok = attrs["x"]
		if !ok {
			return
		}
		if err := ValidateSensitiveExpr(attr); err != nil && !errors.Is(err, ErrInvalidSensitiveExpr) {
			t.Fatalf("ValidateSensitiveExpr(%q) returned error not wrapping ErrInvalidSensitiveExpr: %v", rawExpr, err)
		}
	})
}
