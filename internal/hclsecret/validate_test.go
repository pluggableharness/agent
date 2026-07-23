package hclsecret

import (
	"errors"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// parseAttr parses a single attribute assignment ("x = <src>") and returns
// the resulting attribute, for exercising ValidateSensitiveExpr against
// real parsed HCL expressions rather than hand-built AST nodes.
func parseAttr(t *testing.T, src string) *hcl.Attribute {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte("x = "+src+"\n"), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse %q: %v", src, diags)
	}
	attrs, diags := f.Body.JustAttributes()
	if diags.HasErrors() {
		t.Fatalf("JustAttributes %q: %v", src, diags)
	}
	attr, ok := attrs["x"]
	if !ok {
		t.Fatalf("parse %q: no attribute %q found", src, "x")
	}
	return attr
}

func TestValidateSensitiveExpr(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"valid env call", `env("API_KEY")`, false},
		{"literal string", `"literal-secret"`, true},
		{"wrong function", `coalesce(env("API_KEY"), "default")`, true},
		{"interpolation", `"${env("API_KEY")}-suffix"`, true},
		{"variable argument, not literal", `env(local_var)`, true},
		{"too many args", `env("A", "B")`, true},
		{"no args", `env()`, true},
		{"number literal", `42`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attr := parseAttr(t, tt.src)
			err := ValidateSensitiveExpr(attr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateSensitiveExpr(%q) = nil error, want error", tt.src)
				}
				if !errors.Is(err, ErrInvalidSensitiveExpr) {
					t.Fatalf("ValidateSensitiveExpr(%q) error = %v, want wrapping ErrInvalidSensitiveExpr", tt.src, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSensitiveExpr(%q) unexpected error: %v", tt.src, err)
			}
		})
	}
}
