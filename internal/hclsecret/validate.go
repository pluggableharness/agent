package hclsecret

import (
	"errors"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ErrInvalidSensitiveExpr is returned when a sensitive attribute's
// expression is anything other than exactly env("NAME") with a single
// string-literal argument.
var ErrInvalidSensitiveExpr = errors.New(`hclsecret: sensitive attribute must be exactly env("NAME")`)

// ValidateSensitiveExpr checks that attr's RAW, unevaluated expression is
// exactly a call to env(name) with a single string-literal name argument —
// no interpolation, no concatenation, no default-fallback wrapping
// (configuration.md §4). This MUST run before the expression is ever
// evaluated: once evaluated to a plain string, a literal value and an
// env() lookup result are indistinguishable, which is exactly the case §4
// says enforcement must prevent.
//
// The argument itself is also required to be a literal string (not, say,
// another variable reference) — a conservative reading of "exactly
// env(name)" that keeps the whole expression free of anything the operator
// could use to smuggle a non-obvious value in.
func ValidateSensitiveExpr(attr *hcl.Attribute) error {
	call, ok := attr.Expr.(*hclsyntax.FunctionCallExpr)
	if !ok {
		return fmt.Errorf("hclsecret: attribute %q: %w", attr.Name, ErrInvalidSensitiveExpr)
	}
	if call.Name != EnvFunctionName {
		return fmt.Errorf("hclsecret: attribute %q: %w", attr.Name, ErrInvalidSensitiveExpr)
	}
	if len(call.Args) != 1 {
		return fmt.Errorf("hclsecret: attribute %q: %w", attr.Name, ErrInvalidSensitiveExpr)
	}
	if _, ok := stringLiteralValue(call.Args[0]); !ok {
		return fmt.Errorf("hclsecret: attribute %q: %w", attr.Name, ErrInvalidSensitiveExpr)
	}
	return nil
}

// stringLiteralValue reports the constant string value of expr, if expr is
// a plain string literal with no interpolation. In hclsyntax, a quoted
// string like "FOO" parses as a *hclsyntax.TemplateExpr containing exactly
// one *hclsyntax.LiteralValueExpr part; a string with interpolation
// ("${x}") or concatenation has more than one part (or a non-literal
// part), which this function correctly refuses.
func stringLiteralValue(expr hclsyntax.Expression) (string, bool) {
	tmpl, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok || len(tmpl.Parts) != 1 {
		return "", false
	}
	lit, ok := tmpl.Parts[0].(*hclsyntax.LiteralValueExpr)
	if !ok || lit.Val.Type() != cty.String {
		return "", false
	}
	return lit.Val.AsString(), true
}
