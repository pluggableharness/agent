package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// attrString evaluates attr as a plain string-typed expression. Used for
// attributes that carry no secrets and need no functions in scope (a nil
// EvalContext is sufficient for a literal string).
func attrString(attr *hcl.Attribute) (string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return "", diags
	}
	if val.Type() != cty.String {
		return "", fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	return val.AsString(), nil
}

func attrBool(attr *hcl.Attribute) (bool, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return false, diags
	}
	if val.Type() != cty.Bool {
		return false, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	return val.True(), nil
}

func attrInt(attr *hcl.Attribute) (int, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return 0, diags
	}
	if val.Type() != cty.Number {
		return 0, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	i, _ := val.AsBigFloat().Int64()
	return int(i), nil
}

func attrFloat(attr *hcl.Attribute) (float64, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return 0, diags
	}
	if val.Type() != cty.Number {
		return 0, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	f, _ := val.AsBigFloat().Float64()
	return f, nil
}

// attrStringMap evaluates attr as an object/map of string-valued fields,
// e.g. `resource_attrs = { env = "dev", region = "us-east" }`.
func attrStringMap(attr *hcl.Attribute) (map[string]string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, diags
	}
	if !val.Type().IsObjectType() && !val.Type().IsMapType() {
		return nil, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	fields := val.AsValueMap()
	result := make(map[string]string, len(fields))
	for k, v := range fields {
		if v.Type() != cty.String {
			return nil, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
		}
		result[k] = v.AsString()
	}
	return result, nil
}

// attrStringList evaluates attr as a list/tuple of strings, e.g.
// `["filesystem.read_file", "search.*"]`.
func attrStringList(attr *hcl.Attribute) ([]string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, diags
	}
	if !val.Type().IsTupleType() && !val.Type().IsListType() {
		return nil, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	var result []string
	for _, v := range val.AsValueSlice() {
		if v.Type() != cty.String {
			return nil, fmt.Errorf("config: attribute %q: %w", attr.Name, ErrInvalidValue)
		}
		result = append(result, v.AsString())
	}
	return result, nil
}
