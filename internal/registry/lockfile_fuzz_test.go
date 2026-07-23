package registry

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// sanitizeLockAttrString restricts a fuzzed string to printable ASCII
// (0x20-0x7e), so the byte-exact round-trip assertion below is sound: HCL's
// scanner NFC-normalizes string-literal content on parse, so an arbitrary
// Unicode value (e.g. a bare combining mark) is not guaranteed to survive
// parse unchanged even when correctly quoted — restricting the corpus to
// ASCII sidesteps that without pulling in a normalization dependency
// (x/text/unicode/norm is off the table — no new dependencies).
func sanitizeLockAttrString(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "x"
	}
	return s
}

// hclQuoteLockString renders an already-ASCII-sanitized s as an HCL
// native-syntax double-quoted string literal, guaranteed to parse back to
// exactly s. Mirrors internal/hclsecret/validate_fuzz_test.go's
// hclQuoteString: "\"" and "\\" need backslash-escaping, and a literal "${"
// or "%{" would otherwise be read as the start of template interpolation/a
// template directive, so the leading character is doubled ("$${" / "%%{")
// to escape it per HCL's spec. sanitizeLockAttrString already excludes
// control characters, so no \u-escape fallback is needed here.
func hclQuoteLockString(s string) string {
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
			b.WriteRune(r)
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// FuzzDecodeLockedProvider exercises decodeLockedProvider — the pure,
// I/O-free per-provider-block decode step LoadLockFile delegates to (see
// lockfile.go). A committed .agent/agent.lock.hcl can be hand-edited or
// merge-conflicted, so this is real untrusted-ish structured input, not a
// synthetic target: this package's own CLAUDE.md notes the lock file
// format is otherwise only exercised through file-backed tests.
//
// Invariants asserted:
//  1. "Valid input is never rejected": for ANY printable-ASCII source/version
//     string (see sanitizeLockAttrString — arbitrary Unicode is excluded
//     from the round-trip claim specifically because HCL's scanner
//     normalizes it, not because decodeLockedProvider itself is ASCII-only)
//     and ANY byte slice used as the checksum payload, the constructed
//     checksums/source/version/resolved_at block — built with a
//     guaranteed-valid RFC3339 timestamp derived from the fuzzed unix
//     seconds — MUST decode successfully and MUST round-trip Source,
//     Version, and the "linux_amd64" checksum exactly.
//  2. resolved_at is parsed with time.Parse(time.RFC3339, ...) — the
//     decoded ResolvedAt MUST equal the constructed time to the second
//     (RFC3339's own precision floor).
//  3. No panic on any input, well-formed or not.
func FuzzDecodeLockedProvider(f *testing.F) {
	f.Add("github.com/agentco/provider-anthropic", "1.2.4", int64(1785000000), []byte{0x1a, 0x2b, 0x3c})
	f.Add("", "", int64(0), []byte{})

	f.Fuzz(func(t *testing.T, sourceRaw, versionRaw string, unixSec int64, checksumBytes []byte) {
		source := sanitizeLockAttrString(sourceRaw)
		version := sanitizeLockAttrString(versionRaw)
		// Clamp to roughly ±200 years around the epoch: wide enough to
		// exercise both historical and future dates, narrow enough that
		// Format/Parse always round-trips through a plain 4-digit year
		// (an unbounded int64 second count can format a >4-digit year,
		// which isn't the property under test here).
		const secondsIn200Years = 200 * 365 * 24 * 3600
		sec := unixSec % secondsIn200Years
		resolvedAt := time.Unix(sec, 0).UTC().Format(time.RFC3339)
		checksumHex := hex.EncodeToString(checksumBytes)
		if checksumHex == "" {
			checksumHex = "00"
		}

		src := "source = " + hclQuoteLockString(source) + "\n" +
			"version = " + hclQuoteLockString(version) + "\n" +
			"resolved_at = \"" + resolvedAt + "\"\n" +
			"checksums = { \"linux_amd64\" = \"sha256:" + checksumHex + "\" }\n"

		file, diags := hclsyntax.ParseConfig([]byte(src), "fuzz.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("constructed lock provider body failed to parse: %q: %v", src, diags)
		}

		provider, err := decodeLockedProvider(file.Body)
		if err != nil {
			t.Fatalf("decodeLockedProvider(%q) unexpected error: %v", src, err)
		}
		if provider.Source != source {
			t.Fatalf("Source = %q, want %q", provider.Source, source)
		}
		if provider.Version != version {
			t.Fatalf("Version = %q, want %q", provider.Version, version)
		}
		wantTime := time.Unix(sec, 0).UTC()
		if !provider.ResolvedAt.Equal(wantTime) {
			t.Fatalf("ResolvedAt = %v, want %v", provider.ResolvedAt, wantTime)
		}
		wantChecksum := "sha256:" + checksumHex
		if provider.Checksums["linux_amd64"] != wantChecksum {
			t.Fatalf("Checksums[linux_amd64] = %q, want %q", provider.Checksums["linux_amd64"], wantChecksum)
		}
	})
}
