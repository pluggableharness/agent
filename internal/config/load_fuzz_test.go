package config

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// FuzzDecode feeds arbitrary bytes through hclsyntax.ParseConfig and, for
// anything that parses as HCL at all, into decode — the in-memory body
// walk LoadFile delegates to once hclparse has produced a body (see
// load.go and this package's CLAUDE.md: decode never does file I/O, so it
// is fuzzable directly without a filesystem round trip).
//
// agent.hcl is authored by whoever controls a project checkout, which
// this package cannot assume is well-formed — decode is exactly the
// "parse/validate untrusted-ish input" surface go-testing.md and the task
// brief ask fuzz targets to cover.
//
// Invariants asserted:
//  1. No panic on any input decode is handed, malformed or not.
//  2. decode's documented (*Config, error) contract is never violated:
//     never (nil, nil), and never a non-nil *Config alongside a non-nil
//     error — go-style.md's "never return nil, nil" rule, checked from
//     the caller's side.
//  3. The known-good minimal fixture (the fuzz seed) always decodes
//     successfully — decode defers provider-body evaluation (see this
//     package's CLAUDE.md), so it needs no environment setup to succeed.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(minimalValidHCL))
	f.Add([]byte(`agent_profile "x" { model { primary { provider = "a" id = "b" } } }` + "\x00\xff"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		file, diags := hclsyntax.ParseConfig(data, "fuzz.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			return // not decode's problem — the parser already rejected it
		}

		cfg, err := decode(file.Body)

		if cfg == nil && err == nil {
			t.Fatalf("decode(%q) = (nil, nil), want a non-nil result or a non-nil error", data)
		}
		if cfg != nil && err != nil {
			t.Fatalf("decode(%q) = (%+v, %v), want exactly one of the two non-nil", data, cfg, err)
		}

		if string(data) == minimalValidHCL && err != nil {
			t.Fatalf("decode(minimalValidHCL) unexpected error: %v", err)
		}
	})
}
